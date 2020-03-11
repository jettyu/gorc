/*
	Package rpc provides access to the exported methods of an object across a
	network or other I/O connection.  A server registers an object, making it visible
	as a service with the name of the type of the object.  After registration, exported
	methods of the object will be accessible remotely.  A server may register multiple
	objects (services) of different types but it is an error to register multiple
	objects of the same type.

		- the method has two or three arguments, both exported (or builtin) types.
		- the method's second argument is a pointer.
		- the method has return type error.
		- if the function has three arguments, the thirst argument is interface{}

	In effect, the method must look schematically like

		func (t *T) MethodName(argType T1, replyType *T2) error
	or
		func (t *T) MethodName(argType T1, replyType *T2, ctx interface{}) error
	or
		func (t *T) MethodName(argType T1, ResponseWriter *T2) error
	or
		func (t *T) MethodName(argType T1, ResponseWriter *T2, ctx interface{}) error

	The method's first argument represents the arguments provided by the caller; the
	second argument represents the result parameters to be returned to the caller.
	The method's return value, if non-nil, is passed back as a string that the client
	sees as if created by errors.New.  If an error is returned, the reply parameter
	will not be sent back to the client.
*/

package gosr

import (
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"sync"
	"unicode"
	"unicode/utf8"
)

// HandlerManager ...
type HandlerManager struct {
	handlers map[interface{}]*service
}

// NewHandlerManager ...
func NewHandlerManager() *HandlerManager {
	return &HandlerManager{
		handlers: make(map[interface{}]*service),
	}
}

// Register ...
func (p *HandlerManager) Register(serviceMethod interface{}, rcvr interface{}) error {
	s, e := newService(rcvr)
	if e != nil {
		return e
	}
	p.handlers[serviceMethod] = s
	return nil
}

func (p *HandlerManager) Has(serviceMethod interface{}) bool {
	_, ok := p.handlers[serviceMethod]
	return ok
}

// ServerCodec ...
type ServerCodec interface {
	ReadRequestHeader(req *Request) error
	ReadRequestBody(req *Request, args interface{}) error
	WriteResponse(resp *Response, reply interface{}) error
}

// Server ...
type Server interface {
	Serve()
	ServeRequest() error
	ReadFunction(*ServerFunction) error
	SetContext(interface{})
}

// ServerFunction ...
type ServerFunction struct {
	server   *server
	funcType *funcType
	argv     reflect.Value
	replyv   reflect.Value
	rsp      *Response
}

// ResponseWriter ...
type ResponseWriter struct {
	*Response
	server *server
}

// Free ...
func (p *ResponseWriter) Free() {
	p.server.freeResponse(p.Response)
}

// Reply ...
func (p *ResponseWriter) Reply(reply interface{}) error {
	return p.server.sendResponse(p.Response, reply, false)
}

var responseWriterType = reflect.TypeOf(&ResponseWriter{})

// Call ...
func (p *ServerFunction) Call() {
	p.server.call(p.funcType, p.rsp, p.argv, p.replyv)
}

// NewServerWithCodec ...
func NewServerWithCodec(handlerManager *HandlerManager, codec ServerCodec, ctx interface{}) Server {
	return newServerWithCodec(handlerManager, codec, ctx)
}

// Precompute the reflect type for error. Can't use error directly
// because Typeof takes an empty interface value. This is annoying.
var typeOfError = reflect.TypeOf((*error)(nil)).Elem()

type funcType struct {
	funcValue reflect.Value
	ArgType   reflect.Type
	ReplyType reflect.Type
	numIn     int
	noReply   bool
}

type service struct {
	rcvr  reflect.Value // receiver of methods for the service
	typ   reflect.Type  // type of the receiver
	fType *funcType     // registered methods
}

func newService(rcvr interface{}) (s *service, err error) {
	s = new(service)
	s.typ = reflect.TypeOf(rcvr)
	s.rcvr = reflect.ValueOf(rcvr)
	// Install the methods
	s.fType, err = suitableFuncValue(s.rcvr, false)

	sname := reflect.Indirect(s.rcvr).Type().Name()
	if err != nil {
		str := "rpc.Register: type " + sname + " not suitable type"
		log.Print(str)
		err = errors.New(str)
		return
	}
	return
}

type server struct {
	*HandlerManager
	codec        ServerCodec
	ctx          reflect.Value
	responsePool *sync.Pool
	request      *Request
}

func newServerWithCodec(handlerManager *HandlerManager, codec ServerCodec, ctx interface{}) *server {
	s := &server{
		HandlerManager: handlerManager,
		codec:          codec,
		request:        new(Request),
		responsePool: &sync.Pool{
			New: func() interface{} {
				return &Response{}
			},
		},
	}
	if ctx != nil {
		s.ctx = reflect.ValueOf(ctx)
	}
	return s
}

func (p *server) SetContext(v interface{}) {
	p.ctx = reflect.ValueOf(v)
}

func (p *server) Serve() {
	var (
		err error
	)
	request := p.getRequest()
	for err == nil {
		*request = Request{}
		err = p.codec.ReadRequestHeader(request)
		if err != nil {
			break
		}
		err = p.dealRequestBody(request, false)
	}
}

func (p *server) ServeRequest() (err error) {
	request := p.getRequest()
	err = p.codec.ReadRequestHeader(request)
	if err != nil {
		return
	}
	err = p.dealRequestBody(request, true)
	return
}

func (p *server) ReadFunction(sf *ServerFunction) (err error) {
	request := p.getRequest()
	err = p.codec.ReadRequestHeader(request)
	if err != nil {
		return
	}
	err = p.dealFunction(request, sf)
	return
}

func (p *server) getResponse() *Response {
	rsp := p.responsePool.Get().(*Response)
	if rsp.Seq == nil {
		return rsp
	}
	rsp.Seq = nil
	rsp.ServiceMethod = nil
	rsp.Error = nil
	rsp.Context = nil
	return rsp
}

func (p *server) freeResponse(rsp *Response) {
	p.responsePool.Put(rsp)
}

func (p *server) sendResponse(rsp *Response, reply interface{}, withFree bool) error {
	e := p.codec.WriteResponse(rsp, reply)
	if withFree {
		p.freeResponse(rsp)
	}
	return e
}

func (p *server) getRequest() *Request {
	return p.request
}

func (p *server) getFunction() *ServerFunction {
	return &ServerFunction{}
}

func (p *server) dealFunction(req *Request, sf *ServerFunction) (err error) {
	rsp := p.getResponse()
	rsp.Seq = req.Seq
	rsp.ServiceMethod = req.ServiceMethod
	rsp.Context = req.Context
	s, ok := p.handlers[req.ServiceMethod]
	if !ok {
		rsp.Error = os.ErrInvalid
		err = p.codec.ReadRequestBody(req, invalidRequest)
		if err != nil {
			p.freeResponse(rsp)
			return
		}
		p.sendResponse(rsp, nil, true)
		return
	}
	sf.funcType = s.fType
	mtype := s.fType
	sf.argv, err = p.getRequestBody(req, mtype)
	if err != nil {
		p.freeResponse(rsp)
		return
	}
	sf.replyv = p.getReplyv(rsp, mtype)
	sf.rsp = rsp
	return
}

func (p *server) dealRequestBody(req *Request, block bool) (err error) {
	rsp := p.getResponse()
	rsp.Seq = req.Seq
	rsp.ServiceMethod = req.ServiceMethod
	rsp.Context = req.Context
	s, ok := p.handlers[req.ServiceMethod]
	if !ok {
		rsp.Error = os.ErrInvalid
		err = p.codec.ReadRequestBody(req, invalidRequest)
		if err != nil {
			p.freeResponse(rsp)
			return
		}
		p.sendResponse(rsp, nil, true)
		return
	}
	mtype := s.fType
	argv, e := p.getRequestBody(req, mtype)
	if e != nil {
		p.freeResponse(rsp)
		err = e
		return
	}
	replyv := p.getReplyv(rsp, mtype)
	if block {
		p.call(mtype, rsp, argv, replyv)
		return
	}
	go p.call(mtype, rsp, argv, replyv)
	return
}

func (p *server) getRequestBody(req *Request, mtype *funcType) (argv reflect.Value, err error) {
	// Decode the argument value.
	argIsValue := false // if true, need to indirect before calling.
	if mtype.ArgType.Kind() == reflect.Ptr {
		argv = reflect.New(mtype.ArgType.Elem())
	} else {
		argv = reflect.New(mtype.ArgType)
		argIsValue = true
	}
	// argv guaranteed to be a pointer now.
	if err = p.codec.ReadRequestBody(req, argv.Interface()); err != nil {
		return
	}
	if argIsValue {
		argv = argv.Elem()
	}

	return
}

func (p *server) getReplyv(rsp *Response, mtype *funcType) (replyv reflect.Value) {
	if mtype.noReply {
		replyv = reflect.ValueOf(&ResponseWriter{
			Response: rsp,
			server:   p,
		})
		return
	}
	replyv = reflect.New(mtype.ReplyType.Elem())
	switch mtype.ReplyType.Elem().Kind() {
	case reflect.Map:
		replyv.Elem().Set(reflect.MakeMap(mtype.ReplyType.Elem()))
	case reflect.Slice:
		replyv.Elem().Set(reflect.MakeSlice(mtype.ReplyType.Elem(), 0, 0))
	}
	return
}

func (p *server) call(mtype *funcType, rsp *Response, argv, replyv reflect.Value) {
	function := mtype.funcValue
	// Invoke the method, providing a new value for the reply.
	values := []reflect.Value{argv, replyv, p.ctx}
	returnValues := function.Call(values[:mtype.numIn])
	if mtype.noReply {
		return
	}
	reply := replyv.Interface()
	// The return value for the method is an error.
	errInter := returnValues[0].Interface()
	if errInter != nil {
		rsp.Error = errInter.(error)
		reply = invalidRequest
	}
	p.sendResponse(rsp, reply, true)
}

func suitableFuncValue(funcValue reflect.Value, reportErr bool) (ft *funcType, err error) {
	mtype := funcValue.Type()
	mname := funcValue.Type().Name()
	if mtype.NumIn() != 2 && mtype.NumIn() != 3 {
		err = fmt.Errorf("rpc.Register: method %q has %d input parameters; needs exactly two or three", mname, mtype.NumIn())
		if reportErr {
			log.Println(err)
		}
		return
	}
	if mtype.NumIn() == 3 {
		if mtype.In(2).Kind() != reflect.Interface {
			err = fmt.Errorf("rpc.Register: method %q's thirst input parameter must be interface{} ", mname)
			if reportErr {
				log.Println(err)
			}
			return
		}
	}
	// First arg need not be a pointer.
	argType := mtype.In(0)
	if !isExportedOrBuiltinType(argType) {
		err = fmt.Errorf("rpc.Register: argument type of method %q is not exported: %q", mname, argType)
		if reportErr {
			log.Println(err)
		}
		return
	}
	// Second arg must be a pointer.
	replyType := mtype.In(1)
	if replyType.Kind() != reflect.Ptr {
		err = fmt.Errorf("rpc.Register: reply type of method %q is not a pointer: %q", mname, replyType)
		if reportErr {
			log.Println(err)
		}
		return
	}
	// Reply type must be exported.
	if !isExportedOrBuiltinType(replyType) {
		err = fmt.Errorf("rpc.Register: reply type of method %q is not exported: %q", mname, replyType)
		if reportErr {
			log.Println(err)
		}
		return
	}
	// Method needs one out.
	if mtype.NumOut() != 1 {
		err = fmt.Errorf("rpc.Register: method %q has %d output parameters; needs exactly one", mname, mtype.NumOut())
		if reportErr {
			log.Println(err)
		}
		return
	}
	// The return type of the method must be error.
	if returnType := mtype.Out(0); returnType != typeOfError {
		err = fmt.Errorf("rpc.Register: return type of method %q is %q, must be error", mname, returnType)
		if reportErr {
			log.Println(err)
		}
		return
	}
	noReply := false
	if replyType == responseWriterType {
		noReply = true
	}
	ft = &funcType{funcValue: funcValue, ArgType: argType, ReplyType: replyType, numIn: mtype.NumIn(), noReply: noReply}
	return
}

// A value sent as a placeholder for the server's response value when the server
// receives an invalid request. It is never decoded by the client since the Response
// contains an error when it is used.
var invalidRequest = struct{}{}

// Is this type exported or a builtin?
func isExportedOrBuiltinType(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	// PkgPath will be non-empty even for an exported type,
	// so we need to check the type name as well.
	return isExported(t.Name()) || t.PkgPath() == ""
}

// Is this an exported - upper case - name?
func isExported(name string) bool {
	rune, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(rune)
}
