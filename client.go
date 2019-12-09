package gosr

import (
	"errors"
	"io"
	"log"
	"sync"
)

// Call represents an active RPC.
type Call struct {
	ServiceMethod interface{}
	Args          interface{} // The argument to the function (*struct).
	Reply         interface{} // The reply from the function (*struct).
	Error         error       // After completion, the error status.
	Done          chan *Call  // Strobes when call is complete.
	seq           interface{}
	cb            func(*Call)
}

// Request ...
type Request struct {
	Seq           interface{}
	ServiceMethod interface{}
	Context       interface{}
}

// Response ...
type Response struct {
	Seq           interface{}
	ServiceMethod interface{}
	Error         error
	Context       interface{}
}

// A ClientCodec implements writing of RPC requests and
// reading of RPC responses for the p side of an RPC session.
// The p calls WriteRequest to write a request to the connection
// and calls ReadResponseHeader and ReadResponseBody in pairs
// to read responses. The p calls Close when finished with the
// connection. ReadResponseBody may be called with a nil
// argument to force the body of the response to be read and then
// discarded.
// See NewClient's comment for information about concurrent access.
type ClientCodec interface {
	GetSeq(req *Request) (seq interface{})
	WriteRequest(req *Request, args interface{}) error
	ReadResponseHeader(*Response) error
	ReadResponseBody(resp *Response, reply interface{}) error
	Close() error
}

// Client ...
type Client interface {
	Close() error
	Go(serviceMethod, args interface{}, reply interface{}, done chan *Call) *Call
	Call(serviceMethod, args interface{}, reply interface{}) error
	CallAsync(serviceMethod, args, reply interface{}, cb func(*Call))
	CallWithNoReply(serviceMethod, args interface{}) error
	Wait()
}

// client represents an RPC client.
// There may be multiple outstanding Calls associated
// with a single client, and a client may be used by
// multiple goroutines simultaneously.
type client struct {
	codec    ClientCodec
	mutex    sync.Mutex // protects following
	pending  map[interface{}]*Call
	closing  bool // user has called Close
	shutdown bool // server has told us to stop
	waited   sync.WaitGroup
}

// Wait ...
func (p *client) Wait() {
	p.waited.Wait()
}

// NewClientWithCodec is like NewClient but uses the specified
// codec to encode requests and decode responses.
func NewClientWithCodec(codec ClientCodec) Client {
	p := newClientWithCodec(codec)

	p.waited.Add(1)
	go p.input()

	return p
}

func newClientWithCodec(codec ClientCodec) *client {
	return &client{
		codec:   codec,
		pending: make(map[interface{}]*Call),
	}
}

// Close calls the underlying codec's Close method. If the connection is already
// shutting down, ErrClosed is returned.
func (p *client) Close() error {
	p.mutex.Lock()
	if p.closing {
		p.mutex.Unlock()
		return ErrClosed
	}
	p.closing = true
	p.mutex.Unlock()
	return p.codec.Close()
}

// Go invokes the function asynchronously. It returns the Call structure representing
// the invocation. The done channel will signal when the call is complete by returning
// the same Call object. If done is nil, Go will allocate a new channel.
// If non-nil, done must be buffered or Go will deliberately crash.
func (p *client) Go(serviceMethod, args interface{}, reply interface{}, done chan *Call) *Call {
	call := new(Call)
	call.ServiceMethod = serviceMethod
	call.Args = args
	call.Reply = reply
	if done == nil {
		done = make(chan *Call, 10) // buffered.
	} else {
		// If caller passes done != nil, it must arrange that
		// done has enough buffer for the number of simultaneous
		// RPCs that will be using that channel. If the channel
		// is totally unbuffered, it's best not to run at all.
		if cap(done) == 0 {
			log.Panic("rpc: done channel is unbuffered")
		}
	}
	call.Done = done
	p.send(call)
	return call
}

// Call invokes the named function, waits for it to complete, and returns its error status.
func (p *client) Call(serviceMethod, args, reply interface{}) error {
	call := <-p.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	return call.Error
}

func (p *client) CallAsync(serviceMethod, args, reply interface{}, cb func(*Call)) {
	call := new(Call)
	call.ServiceMethod = serviceMethod
	call.Args = args
	call.Reply = reply
	call.Done = make(chan *Call, 1)
	call.cb = cb
	p.send(call)
	return
}

func (p *client) CallWithNoReply(serviceMethod, args interface{}) error {
	req := Request{
		ServiceMethod: serviceMethod,
	}
	req.Seq = p.codec.GetSeq(&req)
	return p.codec.WriteRequest(&req, args)
}

func (p *client) send(call *Call) {
	req := Request{
		ServiceMethod: call.ServiceMethod,
	}
	seq := p.codec.GetSeq(&req)
	req.Seq = seq
	call.seq = seq
	// Register this call.
	p.mutex.Lock()
	if p.shutdown || p.closing {
		p.mutex.Unlock()
		call.Error = ErrClosed
		call.done()
		return
	}
	p.pending[seq] = call
	p.mutex.Unlock()
	// Encode and send the request.
	err := p.codec.WriteRequest(&req, call.Args)
	if err != nil {
		p.mutex.Lock()
		call = p.pending[seq]
		delete(p.pending, seq)
		p.mutex.Unlock()
		if call != nil {
			call.Error = err
			call.done()
		}
	}
}

func (p *client) dealResp(response *Response) (err error) {
	seq := response.Seq
	p.mutex.Lock()
	call := p.pending[seq]
	delete(p.pending, seq)
	p.mutex.Unlock()

	switch {
	case call == nil:
		// We've got no pending call. That usually means that
		// WriteRequest partially failed, and call was already
		// removed; response is a server telling us about an
		// error reading request body. We should still attempt
		// to read error body, but there's no one to give it to.
		err = p.codec.ReadResponseBody(response, nil)
		if err != nil {
			err = errors.New("reading error body: " + err.Error())
		}
	case response.Error != nil:
		// We've got an error response. Give this to the request;
		// any subsequent requests will get the ReadResponseBody
		// error if there is one.
		call.Error = response.Error
		err = p.codec.ReadResponseBody(response, nil)
		if err != nil {
			err = errors.New("reading error body: " + err.Error())
		}
		call.done()
	default:
		err = p.codec.ReadResponseBody(response, call.Reply)
		if err != nil {
			call.Error = errors.New("reading body " + err.Error())
		}
		call.done()
	}
	return
}

func (p *client) dealClose(err error) {
	// Terminate pending calls.
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.shutdown {
		return
	}
	p.shutdown = true
	closing := p.closing
	if err == io.EOF {
		if closing {
			err = ErrClosed
		} else {
			err = io.ErrUnexpectedEOF
		}
	}
	for _, call := range p.pending {
		call.Error = err
		call.done()
	}
	if debugLog && err != io.EOF && !closing {
		log.Println("rpc: p protocol error:", err)
	}
}

func (p *client) input() {
	defer p.waited.Done()
	var err error
	var response Response
	for err == nil {
		response = Response{}
		err = p.codec.ReadResponseHeader(&response)
		if err != nil {
			break
		}
		err = p.dealResp(&response)
	}
	p.dealClose(err)
}

// If set, print log statements for internal and I/O errors.
var debugLog = false

func (call *Call) done() {
	select {
	case call.Done <- call:
		// ok
		if call.cb != nil {
			call.cb(call)
		}
	default:
		// We don't want to block here. It is the caller's responsibility to make
		// sure the channel has enough buffer space. See comment in Go().
		if debugLog {
			log.Println("rpc: discarding Call reply due to insufficient Done chan capacity")
		}
	}
}
