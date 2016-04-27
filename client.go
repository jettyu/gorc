package gorc

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/jettyu/gotimer"
)

type ClientConnInterface interface {
	Encode(interface{}) (id interface{}, encodeData interface{}, err error)
	Decode(interface{}) (id interface{}, decodeData interface{}, err error)
	Send(interface{}) (err error)
	Recv() (buf interface{}, err error)
	Close() error
}

type Client struct {
	recvChans map[interface{}]chan interface{}
	handler   ClientConnInterface
	timeout   time.Duration
	err       error
	errLock   sync.RWMutex
	sync.Mutex
	status int32
}

func NewClient(handler ClientConnInterface, timeout time.Duration) *Client {
	c := &Client{
		recvChans: make(map[interface{}]chan interface{}),
		handler:   handler,
		timeout:   timeout,
	}
	go c.run()
	return c
}

func (self *Client) Err() error {
	self.errLock.RLock()
	err := self.err
	self.errLock.RUnlock()
	return err
}

func (self *Client) SetErr(err error) {
	self.errLock.Lock()
	self.err = err
	self.errLock.Unlock()
}

func (self *Client) GetHandler() ClientConnInterface {
	return self.handler
}

func (self *Client) IsClosed() bool {
	return atomic.LoadInt32(&self.status) == 1
}

func (self *Client) Call(sendData interface{}) (recvData interface{}, err error) {
	//	if self.IsClosed() {
	//		return nil, ErrorClosed
	//	}
	var (
		id         interface{}
		encodeData interface{}
	)
	id, encodeData, err = self.handler.Encode(sendData)
	if err != nil {
		return nil, err
	}

	recvChan := make(chan interface{}, 1)
	self.Lock()
	if _, ok := self.recvChans[id]; ok {
		err = Errof("[chanrpc] repeated id, id=%v", id)
	} else {
		self.recvChans[id] = recvChan
	}
	self.Unlock()
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
		self.Lock()
		_, ok := self.recvChans[id]
		if ok {
			close(recvChan)
			delete(self.recvChans, id)
		}
		self.Unlock()
	}
	gotimer.AfterFunc(self.timeout, func() {
		go f()
	},
	)
	err = self.handler.Send(encodeData)
	if err != nil {
		return nil, err
	}
	ok := true
	select {
	case recvData, ok = <-recvChan:
		if !ok {
			err = self.Err()
			if err == nil {
				err = ErrorTimeOut
			}
		}
	}
	close(closeChan)
	return recvData, err
}

func (self *Client) CallAsync(sendData interface{}) (<-chan interface{}, error) {
	//	if self.IsClosed() {
	//		return nil, ErrorClosed
	//	}
	var (
		id         interface{}
		encodeData interface{}
		err        error
	)
	id, encodeData, err = self.handler.Encode(sendData)
	if err != nil {
		return nil, err
	}

	recvChan := make(chan interface{}, 1)
	self.Lock()
	if _, ok := self.recvChans[id]; ok {
		err = Errof("[chanrpc] repeated id, id=%v", id)
	} else {
		self.recvChans[id] = recvChan
	}
	self.Unlock()
	if err != nil {
		return nil, err
	}

	f := func() {
		self.Lock()
		_, ok := self.recvChans[id]
		if ok {
			close(recvChan)
			delete(self.recvChans, id)
		}
		self.Unlock()
	}
	gotimer.AfterFunc(self.timeout, func() {
		go f()
	},
	)

	err = self.handler.Send(encodeData)
	if err != nil {
		return nil, err
	}
	return recvChan, nil
}

func (self *Client) Close() error {
	if self.IsClosed() {
		return nil
	}

	atomic.StoreInt32(&self.status, 1)
	go func() {
		<-gotimer.After(time.Second)
		flag := true
		for flag {
			self.Lock()
			if len(self.recvChans) == 0 {
				flag = false
				self.handler.Close()
			}
			self.Unlock()
			if flag {
				time.Sleep(time.Second)
			}
		}
	}()
	return nil
}

func (self *Client) run() {
	for {
		buf, err := self.handler.Recv()
		if err != nil {
			self.errLock.Lock()
			self.err = err
			self.errLock.Unlock()
			self.Lock()
			for k, v := range self.recvChans {
				delete(self.recvChans, k)
				close(v)
			}
			self.recvChans = nil
			self.Unlock()
			break
		}
		//	go func(buf interface{}, self *Client) {
		id, decodeData, e := self.handler.Decode(buf)
		if e != nil {
			return
		}
		self.Lock()
		recvChan, ok := self.recvChans[id]
		if ok {
			select {
			case recvChan <- decodeData:
			default:
			}
			delete(self.recvChans, id)
		}
		self.Unlock()
		//	}(buf, self)
	}
}
