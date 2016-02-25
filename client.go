package gorc

import (
	"github.com/jettyu/gotimer"
	"sync"
	"time"
)

type ClientConnInterface interface {
	Send(interface{}) (rpcid interface{}, err error)
	Recv() (buf interface{}, rpcid interface{}, err error)
}

type Client struct {
	recvChans map[interface{}]chan interface{}
	handler   ClientConnInterface
	timeout   time.Duration
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

func (self *Client) Call(sendData interface{}) (recvData interface{}, err error) {
	var (
		rpcid interface{}
	)
	rpcid, err = self.handler.Send(sendData)
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
	select {
	case recvData = <-recvChan:
	case <-gotimer.After(self.timeout):
		return nil, ErrorTimeOut
	}
	return recvData, err
}

func (self *Client) CallAsync(sendData interface{}) (<-chan interface{}, error) {
	var (
		rpcid interface{}
		err   error
	)
	rpcid, err = self.handler.Send(sendData)
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
	gotimer.AfterFunc(self.timeout, func(){delete(self.recvChans, rpcid)})
	return recvChan, nil
}

func (self *Client) run() {
	for {
		buf, rpcid, err := self.handler.Recv()
		if err != nil {
			break
		}
		self.Lock()
		recvChan, ok := self.recvChans[rpcid]
		if ok {
			recvChan <- buf
			delete(self.recvChans, rpcid)
		}
		self.Unlock()
	}
}
