package gorc

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	_testTcpServer  testTcpServer
	_testTcpClient  testTcpClient
	_testGorcClient *Client
	wg              sync.WaitGroup
)

type testTcpServer struct {
	l net.Listener
}

type testTcpClient struct {
	conn   net.Conn
	bw     *bufio.Writer
	br     *bufio.Reader
	id     uint32
	err    error
	closed bool
	rLock  sync.Mutex
	wLock  sync.Mutex
	eLock  sync.RWMutex
	cLock  sync.RWMutex
}

func (self *testTcpClient) SetErr(err error) {
	self.eLock.Lock()
	self.err = err
	self.eLock.Unlock()
}

func (self *testTcpClient) Err() error {
	self.eLock.Lock()
	err := self.err
	self.eLock.Unlock()
	return err
}

func (self *testTcpClient) Close() error {
	self.cLock.Lock()
	defer self.cLock.Unlock()
	if self.closed {
		return nil
	}
	self.closed = true
	return self.conn.Close()
}

func (self *testTcpClient) Closed() bool {
	self.cLock.RLock()
	closed := self.closed
	self.cLock.RUnlock()
	return closed
}

func (self *testTcpClient) Encode(data interface{}) (id interface{}, encodeData interface{}, err error) {
	id = fmt.Sprintf("%04d", atomic.AddUint32(&self.id, 1))
	var buffer bytes.Buffer
	buffer.WriteString(id.(string))
	buffer.Write(data.([]byte))
	buffer.WriteByte(0x3)
	return id, buffer.Bytes(), nil
}

func (self *testTcpClient) Decode(data interface{}) (id interface{}, encodeData interface{}, err error) {
	buf := data.([]byte)
	if len(buf) < 4 {
		err := fmt.Errorf("conn Recv wrong size, min size=%d, buf size=%d", 4, len(buf))
		return nil, nil, err
	}
	id = string(buf[:4])
	return id, buf[4 : len(buf)-1], nil
}

func (self *testTcpClient) Send(data interface{}) (err error) {
	buf := data.([]byte)
	self.wLock.Lock()
	defer self.wLock.Unlock()
	n, e := self.bw.Write(buf)
	if e != nil {
		return e
	}
	if n != len(buf) {
		return fmt.Errorf("write not complete, bufLen=%d, writeN=%d", len(buf), n)
	}
	return self.bw.Flush()
}

func (self *testTcpClient) Recv() (data interface{}, err error) {
	self.rLock.Lock()
	defer self.rLock.Unlock()
	buf, e := self.br.ReadBytes(0x3)
	return buf, e
}

func testTcpServerStart(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	_testTcpServer.l = l

	go func() {
		for {
			conn, err := _testTcpServer.l.Accept()
			if err != nil {
				log.Println(err)
				break
			}
			go func() {
				var buf [1024]byte
				for {
					n, e := conn.Read(buf[:])
					if e != nil {
						log.Println(e)
						break
					}
					n, e = conn.Write(buf[:n])
					if e != nil {
						break
					}
				}
			}()
		}
	}()
	return nil
}

func testTcpServerStop() {
	if _testTcpServer.l != nil {
		if err := _testTcpServer.l.Close(); err != nil {
			panic(err)
		}
		_testTcpServer.l = nil
	}
}

func testTcpClientConn(addr string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	_testTcpClient.conn = conn
	_testTcpClient.bw = bufio.NewWriter(conn)
	_testTcpClient.br = bufio.NewReader(conn)
	return nil
}

func testTcpClientClose() {
	if _testTcpClient.conn != nil {
		_testTcpClient.conn.Close()
	}
}

func TestStart(t *testing.T) {
	if err := testTcpServerStart(":10010"); err != nil {
		t.Fatal(err)
	}
	if err := testTcpClientConn("127.0.0.1:10010"); err != nil {
		t.Fatal(err)
	}
	wg.Add(1)
	defer wg.Done()
	_testGorcClient = NewClient(&_testTcpClient, time.Second*2)
}

func TestClient(t *testing.T) {
	wg.Add(1)
	defer wg.Done()
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sendStr := fmt.Sprint("hello", i)
			recvData, err := _testGorcClient.Call([]byte(sendStr))
			if err != nil {
				t.Error(err)
				return
			}
			recvStr := string(recvData.([]byte))
			if sendStr != recvStr {
				t.Errorf("sendLen=%d and recvLen=%d", len(sendStr), len(recvStr))
				t.Errorf("sendStr=%s and recvStr=%s", sendStr, recvStr)
			}
		}(i)
	}
	wg.Wait()
}

func TestCallAsync(t *testing.T) {
	wg.Add(1)
	defer wg.Done()
	var (
		sendStr1 = "hello0"
		sendStr2 = "hello1"
		sendStr3 = "hello2"
	)
	recv1, err1 := _testGorcClient.CallAsync([]byte(sendStr1))
	if err1 != nil {
		t.Error(err1)
		return
	}
	recv2, err2 := _testGorcClient.CallAsync([]byte(sendStr2))
	if err2 != nil {
		t.Error(err2)
		return
	}
	recv3, err3 := _testGorcClient.CallAsync([]byte(sendStr3))
	if err3 != nil {
		t.Error(err3)
		return
	}
	for i := 0; i < 3; i++ {
		select {
		case data, ok := <-recv1:
			if !ok {
				if err := _testGorcClient.Err(); err != nil {
					t.Errorf("recv1 recv failed err=%s", err.Error())
					break
				}
				t.Errorf("recv1 recv failed timeout")
			} else {
				recvStr1 := string(data.([]byte))
				if recvStr1 != sendStr1 {
					t.Errorf("sendLen=%d and recvLen=%d", len(sendStr1), len(recvStr1))
					t.Errorf("sendStr=%s and recvStr=%s", sendStr1, recvStr1)
				}
			}
		case data, ok := <-recv2:
			if !ok {
				if err := _testGorcClient.Err(); err != nil {
					t.Errorf("recv2 recv failed err=%s", err.Error())
					break
				}
				t.Errorf("recv2 recv failed timeout")
			} else {
				recvStr2 := string(data.([]byte))
				if recvStr2 != sendStr2 {
					t.Errorf("sendLen=%d and recvLen=%d", len(sendStr2), len(recvStr2))
					t.Errorf("sendStr=%s and recvStr=%s", sendStr2, recvStr2)
				}
			}
		case data, ok := <-recv3:
			if !ok {
				if err := _testGorcClient.Err(); err != nil {
					t.Errorf("recv3 recv failed err=%s", err.Error())
					break
				}
				t.Errorf("recv3 recv failed timeout")
			} else {
				recvStr3 := string(data.([]byte))
				if recvStr3 != sendStr3 {
					t.Errorf("sendLen=%d and recvLen=%d", len(sendStr3), len(recvStr3))
					t.Errorf("sendStr=%s and recvStr=%s", sendStr3, recvStr3)
				}
			}
		case <-time.After(time.Second):
			t.Error("timeout")
		}

	}
}

func TestStop(t *testing.T) {
	wg.Wait()
	testTcpClientClose()
	testTcpServerStop()
}

//func TestCall(t *testing.T) {
//	testTcpServerStop()
//	testTcpClientClose()

//	if err := testTcpServerStart(":10011"); err != nil {
//		t.Fatal(err)
//	}
//	defer testTcpServerStop()
//	if err := testTcpClientConn("127.0.0.1:10011"); err != nil {
//		t.Fatal(err)
//	}
//	defer testTcpClientClose()
//	c := NewClient(&_testTcpClient, time.Second*2)
//	var wg sync.WaitGroup
//	failedNum := int32(0)
//	begin := time.Now().UnixNano()
//	for i := 0; i < 1000; i++ {
//		wg.Add(1)
//		go func(i int) {
//			defer wg.Done()
//			sendStr := fmt.Sprint("hello", i)
//			recvData, err := c.Call([]byte(sendStr))
//			if err != nil {
//				t.Log(err)
//				atomic.AddInt32(&failedNum, 1)
//				return
//			}
//			recvStr := string(recvData.([]byte))
//			if sendStr != recvStr {
//				t.Errorf("sendLen=%d and recvLen=%d", len(sendStr), len(recvStr))
//				t.Errorf("sendStr=%s and recvStr=%s", sendStr, recvStr)
//			}
//		}(i)
//	}
//	wg.Wait()
//	end := time.Now().UnixNano()
//	t.Log((end - begin)/1000, failedNum)
//}
