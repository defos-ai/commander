package fakes

import (
	"errors"
	"io"
	"net/http"
)

type MetadataTransport func(*http.Request) (*http.Response, error)

func (f MetadataTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type MetadataReadError struct{}

func (MetadataReadError) Read([]byte) (int, error) {
	return 0, errors.New("private-marker body read failure")
}

func MetadataResponse(request *http.Request, status int, body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: body, Request: request, ContentLength: -1}
}

type CountedMetadataBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *CountedMetadataBody) Read(data []byte) (int, error) {
	n, err := b.Reader.Read(data)
	b.read += n
	return n, err
}

func (b *CountedMetadataBody) Close() error {
	b.closed = true
	return nil
}

func NewMetadataBody(reader io.Reader) *CountedMetadataBody {
	return &CountedMetadataBody{Reader: reader}
}
func (b *CountedMetadataBody) BytesRead() int { return b.read }
func (b *CountedMetadataBody) Closed() bool   { return b.closed }
