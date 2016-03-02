package gorc

import (
	"github.com/jettyu/gotimer"
	"sync"
	"time"
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

func (self *Client) Call(sendData interface{}) (recvData interface{}, err error) {
	var (
		id         interface{}
		encodeData interface{}
	)
	id, encodeData, err = self.handler.Encode(sendData)
	if err != nil {
		return nil, err
	}

	recvChan := make(chan interface{})
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
	err = self.handler.Send(encodeData)
	if err != nil {
		return nil, err
	}
	select {
	case recvData = <-recvChan:
	case <-gotimer.After(self.timeout):
		self.Lock()
		close(recvChan)
		delete(self.recvChans, id)
		self.Unlock()
		return nil, ErrorTimeOut
	}
	return recvData, err
}

func (self *Client) CallAsync(sendData interface{}) (<-chan interface{}, error) {
	var (
		id         interface{}
		encodeData interface{}
		err        error
	)
	id, encodeData, err = self.handler.Encode(sendData)
	if err != nil {
		return nil, err
	}

	recvChan := make(chan interface{})
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
	err = self.handler.Send(encodeData)
	if err != nil {
		return nil, err
	}
	f := func() {
		self.Lock()
		close(recvChan)
		delete(self.recvChans, id)
		self.Unlock()
	}
	gotimer.AfterFunc(self.timeout, func() {
		go f()
	},
	)
	return recvChan, nil
}

func (self *Client) Close() error {
	return self.handler.Close()
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
				close(v)
				delete(self.recvChans, k)
			}
			self.Unlock()
			break
		}
		go func(buf interface{}, self *Client) {
			id, decodeData, e := self.handler.Decode(buf)
			if e != nil {
				return
			}
			self.Lock()
			recvChan, ok := self.recvChans[id]
			if ok {
				recvChan <- decodeData
				delete(self.recvChans, id)
			}
			self.Unlock()
		}(buf, self)
	}
}
