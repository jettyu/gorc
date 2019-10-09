package gosr

import (
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jettyu/gotimer"
)

// Message ...
type Message interface {
	Seq() interface{}
	Data() interface{}
}

// Codec ...
type Codec interface {
	BuildMessage(data interface{}) (Message, error)
	SendMessage(Message) error
	RecvMessage() (Message, error)
	Close() error
}

// Client ...
type Client interface {
	Call(req interface{}) (resp interface{}, err error)
	AsyncCall(req interface{}) (resp <-chan interface{}, err error)
	io.Closer
	IsClosed() bool
	Err() error
	SetErr(err error)
	GetCodec() Codec
}

// client ...
type client struct {
	recvChans map[interface{}]chan interface{}
	codec     Codec
	timeout   time.Duration
	err       error
	errLock   sync.RWMutex
	sync.Mutex
	status int32
}

// NewClient ...
func NewClient(codec Codec, timeout time.Duration) Client {
	c := &client{
		recvChans: make(map[interface{}]chan interface{}),
		codec:     codec,
		timeout:   timeout,
	}
	go c.run()
	return c
}

// Err ...
func (p *client) Err() error {
	p.errLock.RLock()
	err := p.err
	p.errLock.RUnlock()
	return err
}

// SetErr ...
func (p *client) SetErr(err error) {
	p.errLock.Lock()
	p.err = err
	p.errLock.Unlock()
}

// GetCodec ...
func (p *client) GetCodec() Codec {
	return p.codec
}

// IsClosed ...
func (p *client) IsClosed() bool {
	return atomic.LoadInt32(&p.status) == 1
}

// Call ...
func (p *client) Call(req interface{}) (resp interface{}, err error) {
	respChan, e := p.AsyncCall(req)
	if e != nil {
		err = e
		return
	}
	resp = <-respChan
	return
}

// AsyncCall ...
func (p *client) AsyncCall(req interface{}) (resp <-chan interface{}, err error) {
	//	if self.IsClosed() {
	//		return nil, ErrorClosed
	//	}

	reqMsg, e := p.codec.BuildMessage(req)
	if e != nil {
		err = e
		return
	}
	id := reqMsg.Seq()

	recvChan := make(chan interface{}, 1)
	p.Lock()
	if _, ok := p.recvChans[id]; ok {
		err = Errof("[chanrpc] repeated id, id=%v", id)
	} else {
		p.recvChans[id] = recvChan
	}
	p.Unlock()
	if err != nil {
		return nil, err
	}

	f := func() {
		p.Lock()
		r, ok := p.recvChans[id]
		if ok && r == recvChan {
			close(recvChan)
			delete(p.recvChans, id)
		}
		p.Unlock()
	}
	gotimer.AfterFunc(p.timeout, func() {
		go f()
	},
	)

	err = p.codec.SendMessage(reqMsg)
	if err != nil {
		return nil, err
	}
	return recvChan, nil
}

// Close ...
func (p *client) Close() error {
	if !atomic.CompareAndSwapInt32(&p.status, 0, 1) {
		return nil
	}

	go func() {
		<-gotimer.After(time.Second)
		flag := true
		for flag {
			p.Lock()
			if len(p.recvChans) == 0 {
				flag = false
				p.codec.Close()
			}
			p.Unlock()
			if flag {
				time.Sleep(time.Second)
			}
		}
	}()
	return nil
}

func (p *client) run() {
	for {
		resp, err := p.codec.RecvMessage()
		if err != nil {
			p.errLock.Lock()
			p.err = err
			p.errLock.Unlock()
			p.Lock()
			for k, v := range p.recvChans {
				delete(p.recvChans, k)
				close(v)
			}
			p.recvChans = nil
			p.Unlock()
			break
		}
		id := resp.Seq()
		p.Lock()
		recvChan, ok := p.recvChans[id]
		if ok {
			select {
			case recvChan <- resp.Data():
			default:
			}
			delete(p.recvChans, id)
		}
		p.Unlock()
		//	}(buf, self)
	}
}
