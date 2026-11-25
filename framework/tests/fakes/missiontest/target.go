package missiontest

import (
	"net/http"
	"sync/atomic"
)

type Target struct {
	requests atomic.Int64
}

func (t *Target) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.requests.Add(1)
	_, _ = w.Write(evidence)
}

func (t *Target) Requests() int64 { return t.requests.Load() }
