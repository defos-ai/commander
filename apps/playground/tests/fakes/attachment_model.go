package fakes

import (
	"io"
	"net/http"
	"sync"
)

const AttachmentReply = "Fixture local attachment reply."

type AttachmentModel struct {
	mu       sync.Mutex
	requests [][]byte
}

func (m *AttachmentModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read fixture request", http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	m.requests = append(m.requests, body)
	m.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Fixture local attachment reply.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
}

func (m *AttachmentModel) Requests() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	requests := make([][]byte, len(m.requests))
	for i, request := range m.requests {
		requests[i] = append([]byte(nil), request...)
	}
	return requests
}
