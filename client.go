package gorc

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/jettyu/gotimer"
)

// ClientConnInterface ...
type ClientConnInterface interface {
	Encode(interface{}) (id interface{}, encodeData interface{}, err error)
	Decode(interface{}) (id interface{}, decodeData interface{}, err error)
	Send(interface{}) (err error)
	Recv() (buf interface{}, err error)
	Close() error
}

// Client ...
type Client struct {
	recvChans map[interface{}]chan interface{}
	handler   ClientConnInterface
	timeout   time.Duration
	err       error
	errLock   sync.RWMutex
	sync.Mutex
	status int32
}

// NewClient ...
func NewClient(handler ClientConnInterface, timeout time.Duration) *Client {
	c := &Client{
		recvChans: make(map[interface{}]chan interface{}),
		handler:   handler,
		timeout:   timeout,
	}
	go c.run()
	return c
}

// Err ...
func (p *Client) Err() error {
	p.errLock.RLock()
	err := p.err
	p.errLock.RUnlock()
	return err
}

// SetErr ...
func (p *Client) SetErr(err error) {
	p.errLock.Lock()
	p.err = err
	p.errLock.Unlock()
}

// GetHandler ...
func (p *Client) GetHandler() ClientConnInterface {
	return p.handler
}

// IsClosed ...
func (p *Client) IsClosed() bool {
	return atomic.LoadInt32(&p.status) == 1
}

// Call ...
func (p *Client) Call(sendData interface{}) (recvData interface{}, err error) {
	//	if self.IsClosed() {
	//		return nil, ErrorClosed
	//	}
	var (
		id         interface{}
		encodeData interface{}
	)
	id, encodeData, err = p.handler.Encode(sendData)
	if err != nil {
		return nil, err
	}

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
	closeChan := make(chan struct{})
	f := func() {
		select {
		case <-closeChan:
			return
		default:
		}
		p.Lock()
		_, ok := p.recvChans[id]
		if ok {
			close(recvChan)
			delete(p.recvChans, id)
		}
		p.Unlock()
	}
	gotimer.AfterFunc(p.timeout, func() {
		go f()
	},
	)
	err = p.handler.Send(encodeData)
	if err != nil {
		return nil, err
	}
	ok := true
	select {
	case recvData, ok = <-recvChan:
		if !ok {
			err = p.Err()
			if err == nil {
				err = ErrorTimeOut
			}
		}
	}
	close(closeChan)
	return recvData, err
}

// CallAsync ...
func (p *Client) CallAsync(sendData interface{}) (<-chan interface{}, error) {
	//	if self.IsClosed() {
	//		return nil, ErrorClosed
	//	}
	var (
		id         interface{}
		encodeData interface{}
		err        error
	)
	id, encodeData, err = p.handler.Encode(sendData)
	if err != nil {
		return nil, err
	}

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

	err = p.handler.Send(encodeData)
	if err != nil {
		return nil, err
	}
	return recvChan, nil
}

// Close ...
func (p *Client) Close() error {
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
				p.handler.Close()
			}
			p.Unlock()
			if flag {
				time.Sleep(time.Second)
			}
		}
	}()
	return nil
}

func (p *Client) run() {
	for {
		buf, err := p.handler.Recv()
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
		//	go func(buf interface{}, self *Client) {
		id, decodeData, e := p.handler.Decode(buf)
		if e != nil {
			return
		}
		p.Lock()
		recvChan, ok := p.recvChans[id]
		if ok {
			select {
			case recvChan <- decodeData:
			default:
			}
			delete(p.recvChans, id)
		}
		p.Unlock()
		//	}(buf, self)
	}
}
