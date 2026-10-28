package fakes

import (
	"io"
	"net/http"
)

type Transport func(*http.Request) (*http.Response, error)

func (f Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type ObservedReader struct {
	reader  io.Reader
	observe func()
}

func (r *ObservedReader) Read(data []byte) (int, error) {
	r.observe()
	if len(data) > 4 {
		data = data[:4]
	}
	return r.reader.Read(data)
}

type FailingReader struct{}

func (FailingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func NewObservedReader(reader io.Reader, observe func()) *ObservedReader {
	return &ObservedReader{reader: reader, observe: observe}
}
