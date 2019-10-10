package gosr_test

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

	"github.com/jettyu/gosr"
)

var (
	_testTCPServer testTCPServer
	_testTCPCodec  testTCPClient
	_testClient    gosr.Client
	wg             sync.WaitGroup
)

type testMessage string

func (p testMessage) GetSeq() interface{} {
	return string(p)[:4]
}

func (p testMessage) GetData() interface{} {
	return string(p)[4 : len(p)-1]
}

type testTCPServer struct {
	l net.Listener
}

type testTCPClient struct {
	conn net.Conn
	br   *bufio.Reader
	id   uint32
}

func (p *testTCPClient) Close() error {
	return p.conn.Close()
}

func (p *testTCPClient) BuildMessage(data interface{}) (msg gosr.Message, err error) {
	id := fmt.Sprintf("%04d", atomic.AddUint32(&p.id, 1))
	var buffer bytes.Buffer
	buffer.WriteString(id)
	buffer.WriteString(data.(string))
	buffer.WriteByte(0x3)
	msg = testMessage(buffer.String())
	return
	// return id, buffer.Bytes(), nil
}

func (p *testTCPClient) SendMessage(msg gosr.Message) (err error) {
	n, e := p.conn.Write([]byte(msg.(testMessage)))
	if e != nil {
		err = e
		log.Println(err)
		return
	}
	if n != len(msg.(testMessage)) {
		err = fmt.Errorf("write not complete, bufLen=%d, writeN=%d", len(msg.(testMessage)), n)
		log.Println(err)
		return
	}
	return
}

func (p *testTCPClient) RecvMessage() (msg gosr.Message, err error) {
	buf, e := p.br.ReadBytes(0x3)
	if e != nil {
		log.Println(e)
		err = e
		return
	}
	msg = testMessage(buf)
	return
}

var (
	tcpServerStarted bool
)

func testTCPServerStart(addr string) error {
	if tcpServerStarted {
		return nil
	}
	tcpServerStarted = true
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	_testTCPServer.l = l

	go func() {
		for {
			conn, err := _testTCPServer.l.Accept()
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

func testTCPServerStop() {
	if !tcpServerStarted {
		return
	}
	tcpServerStarted = false
	if _testTCPServer.l != nil {
		if err := _testTCPServer.l.Close(); err != nil {
			panic(err)
		}
		_testTCPServer.l = nil
	}
}

func testTCPClientConn(addr string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	_testTCPCodec.conn = conn
	_testTCPCodec.br = bufio.NewReader(conn)
	return nil
}

func testTCPClientClose() {
	if _testTCPCodec.conn != nil {
		_testTCPCodec.conn.Close()
	}
}

func testStart(t *testing.T) {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	if err := testTCPServerStart(":10010"); err != nil {
		t.Fatal(err)
	}
	if err := testTCPClientConn("127.0.0.1:10010"); err != nil {
		t.Fatal(err)
	}
	wg.Add(1)
	defer wg.Done()
	_testClient = gosr.NewClient(&_testTCPCodec, time.Second*2)
}

func testStop(t *testing.T) {
	wg.Wait()
	testTCPClientClose()
	testTCPServerStop()
}

func TestClient(t *testing.T) {
	testStart(t)
	defer testStop(t)
	wg.Add(1)
	defer wg.Done()
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sendStr := fmt.Sprint("hello", i)
			recvData, err := _testClient.Call(sendStr)
			if err != nil {
				t.Error(err)
				return
			}
			recvStr := recvData.(string)
			if sendStr != recvStr {
				t.Errorf("sendLen=%d and recvLen=%d", len(sendStr), len(recvStr))
				t.Errorf("sendStr=%s and recvStr=%s", sendStr, recvStr)
			}
		}(i)
	}
	wg.Wait()
}

func TestCallAsync(t *testing.T) {
	testStart(t)
	defer testStop(t)
	wg.Add(1)
	defer wg.Done()
	var (
		sendStr1 = "hello0"
		sendStr2 = "hello1"
		sendStr3 = "hello2"
	)
	recv1, err1 := _testClient.AsyncCall(sendStr1)
	if err1 != nil {
		t.Error(err1)
		return
	}
	recv2, err2 := _testClient.AsyncCall(sendStr2)
	if err2 != nil {
		t.Error(err2)
		return
	}
	recv3, err3 := _testClient.AsyncCall(sendStr3)
	if err3 != nil {
		t.Error(err3)
		return
	}
	for i := 0; i < 3; i++ {
		select {
		case data, ok := <-recv1:
			if !ok {
				if err := _testClient.Err(); err != nil {
					t.Errorf("recv1 recv failed err=%s", err.Error())
					break
				}
				t.Errorf("recv1 recv failed timeout")
			} else {
				recvStr1 := data.(string)
				if recvStr1 != sendStr1 {
					t.Errorf("sendLen=%d and recvLen=%d", len(sendStr1), len(recvStr1))
					t.Errorf("sendStr=%s and recvStr=%s", sendStr1, recvStr1)
				}
			}
		case data, ok := <-recv2:
			if !ok {
				if err := _testClient.Err(); err != nil {
					t.Errorf("recv2 recv failed err=%s", err.Error())
					break
				}
				t.Errorf("recv2 recv failed timeout")
			} else {
				recvStr2 := data.(string)
				if recvStr2 != sendStr2 {
					t.Errorf("sendLen=%d and recvLen=%d", len(sendStr2), len(recvStr2))
					t.Errorf("sendStr=%s and recvStr=%s", sendStr2, recvStr2)
				}
			}
		case data, ok := <-recv3:
			if !ok {
				if err := _testClient.Err(); err != nil {
					t.Errorf("recv3 recv failed err=%s", err.Error())
					break
				}
				t.Errorf("recv3 recv failed timeout")
			} else {
				recvStr3 := data.(string)
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
