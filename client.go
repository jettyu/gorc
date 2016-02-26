package gorc

import (
	"github.com/jettyu/gotimer"
	"sync"
	"time"
)

type ClientConnInterface interface {
	Encode(interface{}) (rpcid interface{}, encodeData interface{}, err error)
	Decode(interface{}) (rpcid interface{}, decodeData interface{}, err error)
	Send(interface{}) (err error)
	Recv() (buf interface{}, err error)
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
	self.errLock.Unlock()
	return err
}

func (self *Client) Call(sendData interface{}) (recvData interface{}, err error) {
	var (
		rpcid      interface{}
		encodeData interface{}
	)
	rpcid, encodeData, err = self.handler.Encode(sendData)
	if err != nil {
		return nil, err
	}

	recvChan := make(chan interface{})
	self.Lock()
	if _, ok := self.recvChans[rpcid]; ok {
		err = Errof("[chanrpc] repeated rpcid, rpcid=%v", rpcid)
	} else {
		self.recvChans[rpcid] = recvChan
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
		delete(self.recvChans, rpcid)
		self.Unlock()
		return nil, ErrorTimeOut
	}
	return recvData, err
}

func (self *Client) CallAsync(sendData interface{}) (<-chan interface{}, error) {
	var (
		rpcid      interface{}
		encodeData interface{}
		err        error
	)
	rpcid, encodeData, err = self.handler.Encode(sendData)
	if err != nil {
		return nil, err
	}

	recvChan := make(chan interface{})
	self.Lock()
	if _, ok := self.recvChans[rpcid]; ok {
		err = Errof("[chanrpc] repeated rpcid, rpcid=%v", rpcid)
	} else {
		self.recvChans[rpcid] = recvChan
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
		delete(self.recvChans, rpcid)
		self.Unlock()
	}
	gotimer.AfterFunc(self.timeout, func() {
		go f()
	},
	)
	return recvChan, nil
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
		rpcid, decodeData, e := self.handler.Decode(buf)
		if e != nil {
			continue
		}
		self.Lock()
		recvChan, ok := self.recvChans[rpcid]
		if ok {
			recvChan <- decodeData
			delete(self.recvChans, rpcid)
		}
		self.Unlock()
	}
}
