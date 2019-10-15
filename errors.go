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
	// ErrClosed ...
	ErrClosed Error = fmt.Errorf("[gosr] closed")
)
