package gorc

import (
	"fmt"
)

type ChanRpcError error

func Errof(format string, args ...interface{}) ChanRpcError {
	return fmt.Errorf(format, args...)
}

var (
	ErrorTimeOut ChanRpcError = fmt.Errorf("[chanrpc] timeout")
)
