package gorc

import (
	"fmt"
)

// Error ...
type Error error

// Errof ...
func Errof(format string, args ...interface{}) Error {
	return fmt.Errorf(format, args...)
}

var (
	// ErrorTimeOut ...
	ErrorTimeOut Error = fmt.Errorf("[gorc] timeout")
	// ErrorClosed ...
	ErrorClosed Error = fmt.Errorf("[gorc] closed")
)
