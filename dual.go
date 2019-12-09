package gosr

// DualHeader ...
type DualHeader struct {
	Seq           interface{}
	ServiceMethod interface{}
	Error         error
	Context       interface{}
	IsRequest     bool
}

// ToResponse ...
func (p *DualHeader) ToResponse(response *Response) {
	response.Seq = p.Seq
	response.ServiceMethod = p.ServiceMethod
	response.Error = p.Error
	response.Context = p.Context
}

// ToRequest ...
func (p *DualHeader) ToRequest(request *Request) {
	request.Seq = p.Seq
	request.ServiceMethod = p.ServiceMethod
	request.Context = p.Context
}

// DualCodec ...
type DualCodec interface {
	GetSeq(*Request) (seq interface{})
	WriteRequest(*Request, interface{}) error
	WriteResponse(*Response, interface{}) error
	ReadHeader(*DualHeader) error
	ReadRequestBody(req *Request, args interface{}) error
	ReadResponseBody(resp *Response, reply interface{}) error
	Close() error
}

// Dual ...
type Dual interface {
	Server
	Client() Client
}

// NewDualWithCodec ...
func NewDualWithCodec(codec DualCodec, handlers *HandlerManager, ctx interface{}) Dual {
	return newDualWithCodec(codec, handlers, ctx)
}

// CodecAdapter ...
type CodecAdapter struct {
	DualCodec
}

// ReadResponseHeader ...
func (p CodecAdapter) ReadResponseHeader(rsp *Response) error {
	var header DualHeader
	err := p.ReadHeader(&header)
	if err != nil {
		return err
	}
	header.ToResponse(rsp)
	return nil
}

// ReadRequestHeader ...
func (p CodecAdapter) ReadRequestHeader(req *Request) error {
	var header DualHeader
	err := p.ReadHeader(&header)
	if err != nil {
		return err
	}
	header.ToRequest(req)
	return nil
}

type dual struct {
	codec        DualCodec
	codecAdapter *CodecAdapter
	client       *client
	server       *server
	head         *DualHeader
	request      *Request
	response     *Response
}

func (p *dual) getHeader() *DualHeader {
	return p.head
}

func (p *dual) getRequest() *Request {
	return p.request
}

func (p *dual) getResponse() *Response {
	return p.response
}

func newDualWithCodec(codec DualCodec, handlers *HandlerManager, ctx interface{}) *dual {
	s := &dual{
		codec:        codec,
		codecAdapter: &CodecAdapter{codec},
		head:         new(DualHeader),
		request:      new(Request),
		response:     new(Response),
	}

	s.client = newClientWithCodec(s.codecAdapter)
	s.server = newServerWithCodec(handlers, s.codecAdapter, ctx)

	return s
}

func (p *dual) SetContext(ctx interface{}) {
	p.server.SetContext(ctx)
}

func (p *dual) Client() Client {
	return p.client
}

func (p *dual) ServeRequest() (err error) {
	head := p.getHeader()
	request := p.getRequest()
	response := p.getResponse()
	for err == nil {
		*head = DualHeader{}
		err = p.codec.ReadHeader(head)
		if err != nil {
			break
		}
		if !head.IsRequest {
			head.ToResponse(response)
			err = p.client.dealResp(response)
			continue
		}
		head.ToRequest(request)
		err = p.server.dealRequestBody(request, true)
		return
	}
	p.client.dealClose(err)
	return
}

func (p *dual) ReadFunction(sf *ServerFunction) (err error) {
	head := p.getHeader()
	request := p.getRequest()
	response := p.getResponse()
	for err == nil {
		*head = DualHeader{}
		err = p.codec.ReadHeader(head)
		if err != nil {
			break
		}
		if !head.IsRequest {
			head.ToResponse(response)
			err = p.client.dealResp(response)
			continue
		}
		head.ToRequest(request)
		err = p.server.dealFunction(request, sf)
		return
	}
	p.client.dealClose(err)
	return
}

func (p *dual) Serve() {
	var (
		err error
	)
	head := p.getHeader()
	request := p.getRequest()
	response := p.getResponse()
	for err == nil {
		*head = DualHeader{}
		err = p.codec.ReadHeader(head)
		if err != nil {
			break
		}
		if !head.IsRequest {
			head.ToResponse(response)
			err = p.client.dealResp(response)
			continue
		}
		head.ToRequest(request)
		// *response = Response{}
		err = p.server.dealRequestBody(request, false)
	}
	p.client.dealClose(err)
}
