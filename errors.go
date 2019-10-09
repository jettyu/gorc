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
	// ErrorTimeOut ...
	ErrorTimeOut Error = fmt.Errorf("[gosr] timeout")
	// ErrorClosed ...
	ErrorClosed Error = fmt.Errorf("[gosr] closed")
)
