package gosr_test

import (
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jettyu/gosr"
)

type testReq struct {
	Seq    int         `json:"seq"`
	Method string      `json:"method"`
	Data   interface{} `json:"data"`
}

type testRsp struct {
	Seq  int         `json:"seq"`
	Data interface{} `json:"data"`
}

// [:4] - id, end with 0x0
type testClientCodec struct {
	rwc io.ReadWriteCloser
	de  *json.Decoder
	en  *json.Encoder
	buf json.RawMessage
	seq uint32
}

func newTestClientCodec(rwc io.ReadWriteCloser, de *json.Decoder, en *json.Encoder) *testClientCodec {
	return &testClientCodec{
		rwc: rwc,
		de:  de,
		en:  en,
	}
}

func (p *testClientCodec) Close() error {
	return p.rwc.Close()
}

func (p *testClientCodec) GetSeq(req *gosr.Request) (seq interface{}) {
	seq = atomic.AddUint32(&p.seq, 1)
	return
}

func (p *testClientCodec) WriteRequest(req *gosr.Request, args interface{}) (err error) {
	var data testReq
	data.Seq = int(req.Seq.(uint32))
	data.Method = req.ServiceMethod.(string)
	data.Data = args
	err = p.en.Encode(data)
	return
}

func (p *testClientCodec) ReadResponseHeader(rsp *gosr.Response) (err error) {
	p.buf = p.buf[:0]
	var data testRsp
	data.Data = &p.buf
	err = p.de.Decode(&data)
	if err != nil {
		return
	}
	rsp.Seq = uint32(data.Seq)
	rsp.Context = data.Data
	return
}

// ReadResponseBody ...
func (p *testClientCodec) ReadResponseBody(rsp *gosr.Response, reply interface{}) (err error) {
	err = json.Unmarshal(*rsp.Context.(*json.RawMessage), reply)
	return
}

type testServerCodec struct {
	rwc io.ReadWriteCloser
	de  *json.Decoder
	en  *json.Encoder
	buf json.RawMessage
}

func newTestServerCodec(rwc io.ReadWriteCloser, de *json.Decoder, en *json.Encoder) *testServerCodec {
	return &testServerCodec{
		rwc: rwc,
		de:  de,
		en:  en,
	}
}

func (p *testServerCodec) ReadRequestHeader(req *gosr.Request) (err error) {
	p.buf = p.buf[:0]
	var data testReq
	data.Data = &p.buf
	err = p.de.Decode(&data)
	if err != nil {
		return
	}
	req.Seq = uint32(data.Seq)
	req.ServiceMethod = data.Method
	req.Context = data.Data
	return
}

func (p *testServerCodec) ReadRequestBody(req *gosr.Request, args interface{}) (err error) {
	err = json.Unmarshal(*req.Context.(*json.RawMessage), args)
	return
}

func (p *testServerCodec) WriteResponse(rsp *gosr.Response, reply interface{}) (err error) {
	var data testRsp
	data.Seq = int(rsp.Seq.(uint32))
	data.Data = reply
	err = p.en.Encode(data)
	return
}

func testServerClient(t *testing.T, client gosr.Client, count *int32) {
	atomic.StoreInt32(count, 0)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			arg := int32(1)
			res := int32(0)
			err := client.Call("incr", arg, &res)
			if err != nil {
				t.Error(err)
				return
			}
			if arg != res {
				t.Error(arg, res)
			}
		}(i)
	}
	wg.Add(1)
	arg := int32(1)
	res := int32(0)
	client.CallAsync("incr", arg, &res, func(*gosr.Call) {
		defer wg.Done()
		if arg != res {
			t.Fatal(arg, res)
		}
	})
	wg.Wait()
	e := client.Call("count", 0, &res)
	if e != nil {
		t.Fatal(e)
	}
	if res != 4 {
		t.Fatal(res, atomic.LoadInt32(count))
	}
}

func TestServerClient(t *testing.T) {
	c, s := gosr.NewTestConn()
	defer c.Close()
	client := gosr.NewClientWithCodec(newTestClientCodec(c, json.NewDecoder(c), json.NewEncoder(c)))
	handlers := gosr.NewHandlerManager()
	server := gosr.NewServerWithCodec(handlers, newTestServerCodec(s, json.NewDecoder(s), json.NewEncoder(s)), nil)
	count := int32(0)
	handlers.Register("incr", func(i int32, res *int32) error {
		atomic.AddInt32(&count, i)
		*res = i
		return nil
	})
	handlers.Register("count", func(int32, res *int32) error {
		*res = atomic.LoadInt32(&count)
		return nil
	})
	go server.Serve()
	testServerClient(t, client, &count)
}

type testDualCodec struct {
	*testClientCodec
	*testServerCodec
	buf json.RawMessage
}

func newTestDualCodec(rwc io.ReadWriteCloser) *testDualCodec {
	de := json.NewDecoder(rwc)
	en := json.NewEncoder(rwc)
	return &testDualCodec{
		testClientCodec: newTestClientCodec(rwc, de, en),
		testServerCodec: newTestServerCodec(rwc, de, en),
	}
}

// ReadHeader ...
func (p *testDualCodec) ReadHeader(head *gosr.DualHeader) (err error) {
	p.buf = p.buf[:0]
	var data testReq
	data.Data = &p.buf
	err = p.testServerCodec.de.Decode(&data)
	if err != nil {
		return
	}
	head.Seq = uint32(data.Seq)
	head.ServiceMethod = data.Method
	head.Context = data.Data
	if data.Method != "" {
		head.IsRequest = true
	}
	return
}

func TestDual(t *testing.T) {
	handlers := gosr.NewHandlerManager()
	handlers.Register("incr", func(i int32, res *int32, ctx interface{}) error {
		atomic.AddInt32(ctx.(*int32), i)
		*res = i
		return nil
	})
	handlers.Register("count", func(i int32, resp *gosr.ResponseWriter, ctx interface{}) error {
		defer resp.Free()
		resp.Reply(atomic.LoadInt32(ctx.(*int32)))
		return nil
	})
	c, s := gosr.NewTestConn()
	defer c.Close()
	defer s.Close()
	ccount := int32(0)
	scount := int32(0)
	client := gosr.NewDualWithCodec(newTestDualCodec(c), handlers, &ccount)
	server := gosr.NewDualWithCodec(newTestDualCodec(s), handlers, &scount)
	go server.Serve()
	go client.Serve()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		testServerClient(t, client.Client(), &ccount)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testServerClient(t, server.Client(), &scount)
	}()
	wg.Wait()
}
