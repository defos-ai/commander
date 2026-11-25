package missiontest

import (
	"net/http"
	"sync/atomic"
	"time"
)

type DeadlineTransport struct {
	base         http.RoundTripper
	maxRemaining atomic.Int64
}

func NewDeadlineTransport(base http.RoundTripper) *DeadlineTransport {
	return &DeadlineTransport{base: base}
}

func (t *DeadlineTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if deadline, ok := request.Context().Deadline(); ok {
		remaining := int64(time.Until(deadline))
		for previous := t.maxRemaining.Load(); remaining > previous; previous = t.maxRemaining.Load() {
			if t.maxRemaining.CompareAndSwap(previous, remaining) {
				break
			}
		}
	}
	return t.base.RoundTrip(request)
}

func (t *DeadlineTransport) MaxRemaining() time.Duration {
	return time.Duration(t.maxRemaining.Load())
}

func (t *DeadlineTransport) CloseIdleConnections() {
	if client, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		client.CloseIdleConnections()
	}
}
