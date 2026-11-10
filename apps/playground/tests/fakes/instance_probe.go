package fakes

import (
	"encoding/json"
	"net/http"
	"sync"
)

type InstanceProbeRequest struct {
	Method, Path  string
	Authorization bool
}

type InstanceProbe struct {
	mu                    sync.Mutex
	application, identity string
	redirect              string
	requests              []InstanceProbeRequest
}

func (p *InstanceProbe) Respond(application, identity, redirect string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.application, p.identity, p.redirect = application, identity, redirect
}

func (p *InstanceProbe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.requests = append(p.requests, InstanceProbeRequest{Method: r.Method, Path: r.URL.Path, Authorization: r.Header.Get("Authorization") != ""})
	application, identity, redirect := p.application, p.identity, p.redirect
	p.mu.Unlock()
	if redirect != "" {
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"application": application, "instance_id": identity})
}

func (p *InstanceProbe) Requests() []InstanceProbeRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]InstanceProbeRequest(nil), p.requests...)
}
