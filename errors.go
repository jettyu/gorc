package gorc

import (
	"fmt"
)

type GoRcError error

func Errof(format string, args ...interface{}) GoRcError {
	return fmt.Errorf(format, args...)
}

var (
	ErrorTimeOut GoRcError = fmt.Errorf("[gorc] timeout")
	ErrorClosed  GoRcError = fmt.Errorf("[gorc] closed")
)
