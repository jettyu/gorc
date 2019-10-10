package gosr

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
	// ErrTimeOut ...
	ErrTimeOut Error = fmt.Errorf("[gosr] timeout")
	// ErrClosed ...
	ErrClosed Error = fmt.Errorf("[gosr] closed")
)
