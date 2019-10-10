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

func (p testMessage) Err() error {
	return nil
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

func (p *testTCPClient) BuildRequest(data interface{}) (msg gosr.Request, err error) {
	id := fmt.Sprintf("%04d", atomic.AddUint32(&p.id, 1))
	msg = testMessage(id)
	return
	// return id, buffer.Bytes(), nil
}

func (p *testTCPClient) WriteRequest(msg gosr.Request, args interface{}) (err error) {
	var buffer bytes.Buffer
	buffer.WriteString(string(msg.(testMessage)))
	buffer.WriteString(args.(string))
	buffer.WriteByte(0x3)
	n, e := p.conn.Write(buffer.Bytes())
	if e != nil {
		err = e
		log.Println(err)
		return
	}
	if n != buffer.Len() {
		err = fmt.Errorf("write not complete, bufLen=%d, writeN=%d", len(msg.(testMessage)), n)
		log.Println(err)
		return
	}
	return
}

func (p *testTCPClient) ReadResponseHeader() (msg gosr.Response, err error) {
	idbuf := make([]byte, 4)
	_, err = p.br.Read(idbuf)
	if err != nil {
		log.Println(err)
		return
	}

	msg = testMessage(idbuf)
	return
}

// ReadResponseBody ...
func (p *testTCPClient) ReadResponseBody(header gosr.Response, reply interface{}) (err error) {
	buf, e := p.br.ReadBytes(0x3)
	if e != nil {
		log.Println(e)
		err = e
		return
	}
	*reply.(*string) = string(buf[:len(buf)-1])
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
	_testClient = gosr.NewClientWithCodec(&_testTCPCodec, 0)
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
			recvStr := ""
			err := _testClient.Call(sendStr, &recvStr)
			if err != nil {
				t.Error(err)
				return
			}
			if sendStr != recvStr {
				t.Errorf("sendLen=%d and recvLen=%d", len(sendStr), len(recvStr))
				t.Errorf("sendStr=%s and recvStr=%s", sendStr, recvStr)
			}
		}(i)
	}
	wg.Wait()
}
