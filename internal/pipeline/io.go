package pipeline

import "io"

// ioReadAll wraps io.ReadAll for the fetch package (avoids importing io directly
// in fetch.go which already has many imports).
func ioReadAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}