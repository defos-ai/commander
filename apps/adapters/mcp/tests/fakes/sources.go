package fakes

import "net/http"

type SourceTransport func(*http.Request) (*http.Response, error)

func (transport SourceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}
