package gorc

import (
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
	_testTcpServer testTcpServer
	_testTcpClient testTcpClient
	_testGorcClient *Client
)

type testTcpServer struct {
	l net.Listener
}

type testTcpClient struct {
	conn       net.Conn
	recvBuf    bytes.Buffer
	notifyChan chan bool
	rpcid      uint32
	err        error
}

func (self *testTcpClient) run() {
	var buf [1024]byte
	go func() {
		for {
			n, err := self.conn.Read(buf[:])

			if n > 0 {
				self.recvBuf.Write(buf[:n])
			}
			self.notifyChan <- true
			if err != nil {
				self.err = err
				break
			}

		}
	}()
}

func (self *testTcpClient) Send(data interface{}) (rpcid interface{}, err error) {
	rpcid = fmt.Sprintf("%04d", atomic.AddUint32(&self.rpcid, 1))
	var buffer bytes.Buffer
	buffer.WriteString(rpcid.(string))
	buffer.Write(data.([]byte))
	buffer.WriteByte(0x3)
	buf := buffer.Bytes()
	n, e := self.conn.Write(buf)
	if e != nil {
		return nil, e
	}
	if n != len(buf) {
		return nil, fmt.Errorf("write not complete, bufLen=%d, writeN=%d", len(buf), n)
	}

	return rpcid, nil
}

func (self *testTcpClient) Recv() (data interface{}, rpcid interface{}, err error) {
	var (
		buf []byte
		e   error
	)
	for {
		<-self.notifyChan
		buf, e = self.recvBuf.ReadBytes(0x3)
		if e != nil {
			continue
		}

		if self.recvBuf.Len() > 0 {
			self.notifyChan <- true
		}

		break
	}

	if len(buf) < 4 {
		if err == nil {
			err = fmt.Errorf("conn Recv wrong size, min size=%d, buf size=%d", 4, len(buf))
			return nil, nil, err
		}
	}
	rpcid = string(buf[:4])
	return buf[4 : len(buf)-1], rpcid, err
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
	_testTcpClient.notifyChan = make(chan bool, 1024)
	_testTcpClient.run()
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
	_testGorcClient = NewClient(&_testTcpClient, time.Second*2)
}

func TestClient(t *testing.T) {

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
	for i:=0; i<3; i++ {
		select {
			case data := <-recv1:
				recvStr1 := string(data.([]byte))
				if recvStr1 != sendStr1 {
					t.Errorf("sendLen=%d and recvLen=%d", len(sendStr1), len(recvStr1))
					t.Errorf("sendStr=%s and recvStr=%s", sendStr1, recvStr1)
				}
			case data := <-recv2:
				recvStr2 := string(data.([]byte))
				if recvStr2 != sendStr2 {
					t.Errorf("sendLen=%d and recvLen=%d", len(sendStr2), len(recvStr2))
					t.Errorf("sendStr=%s and recvStr=%s", sendStr2, recvStr2)
				}
			case data := <-recv3:
				recvStr3 := string(data.([]byte))
				if recvStr3 != sendStr3 {
					t.Errorf("sendLen=%d and recvLen=%d", len(sendStr3), len(recvStr3))
					t.Errorf("sendStr=%s and recvStr=%s", sendStr3, recvStr3)
				}
		}
	}
}

func TestStop(t *testing.T) {
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
