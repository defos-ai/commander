package fakes

import (
	"io"
	"net/http"
	"sync"
	"time"
)

type RateLimitReply struct {
	Status  int
	Headers http.Header
	Body    string
}

type RateLimitProvider struct {
	replies   []RateLimitReply
	mu        sync.Mutex
	times     []time.Time
	bodies    [][]byte
	responded chan struct{}
}

func NewRateLimitProvider(replies ...RateLimitReply) *RateLimitProvider {
	copy := append([]RateLimitReply(nil), replies...)
	for i := range copy {
		copy[i].Headers = copy[i].Headers.Clone()
	}
	return &RateLimitProvider{replies: copy, responded: make(chan struct{}, 16)}
}

func (p *RateLimitProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	p.mu.Lock()
	index := min(len(p.times), len(p.replies)-1)
	p.times = append(p.times, time.Now())
	p.bodies = append(p.bodies, body)
	reply := p.replies[index]
	p.mu.Unlock()
	for name, values := range reply.Headers {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(reply.Status)
	_, _ = io.WriteString(w, reply.Body)
	if flush, ok := w.(http.Flusher); ok {
		flush.Flush()
	}
	select {
	case p.responded <- struct{}{}:
	default:
	}
}

func (p *RateLimitProvider) Responded() <-chan struct{} { return p.responded }

func (p *RateLimitProvider) Snapshot() ([]time.Time, [][]byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	bodies := make([][]byte, len(p.bodies))
	for i, body := range p.bodies {
		bodies[i] = append([]byte(nil), body...)
	}
	return append([]time.Time(nil), p.times...), bodies
}
