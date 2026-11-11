package fakes

import (
	"net/http"
	"sync"
)

type SetupMCPProbe struct {
	mu                        sync.Mutex
	localToken, explicitToken string
	requests                  int
	sawLocal, sawExplicit     bool
}

func NewSetupMCPProbe(localToken, explicitToken string) *SetupMCPProbe {
	return &SetupMCPProbe{localToken: localToken, explicitToken: explicitToken}
}

func (p *SetupMCPProbe) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	p.mu.Lock()
	p.requests++
	p.sawLocal = p.sawLocal || request.Header.Get("Authorization") == "Bearer "+p.localToken
	p.sawExplicit = p.sawExplicit || request.Header.Get("Authorization") == "Bearer "+p.explicitToken
	p.mu.Unlock()
	w.WriteHeader(http.StatusUnauthorized)
}

func (p *SetupMCPProbe) RequestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests
}

func (p *SetupMCPProbe) SawLocalToken() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sawLocal
}

func (p *SetupMCPProbe) SawExplicitToken() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sawExplicit
}
