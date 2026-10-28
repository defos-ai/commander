package fakes

import (
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"

	adminsessions "github.com/defos-ai/commander/apps/admin/sessions"
)

type SessionAPIConfig struct {
	Session   adminsessions.Session
	RequestID string
}

type SessionAPISnapshot struct {
	Session adminsessions.Session
	Methods []string
}

type SessionAPI struct {
	t         testing.TB
	mu        sync.Mutex
	session   adminsessions.Session
	requestID string
	methods   []string
}

func NewSessionAPI(t testing.TB, config SessionAPIConfig) *SessionAPI {
	t.Helper()
	return &SessionAPI{t: t, session: cloneSession(config.Session), requestID: config.RequestID}
}

func (a *SessionAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions":
		if r.URL.Query().Get("scope_id") != a.session.ScopeID || r.URL.Query().Get("owner_agent_id") != a.session.OwnerAgentID {
			a.t.Error("session list dropped intersecting filters")
		}
		json.NewEncoder(w).Encode(map[string]any{"items": []adminsessions.Session{a.session}})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/"+a.session.ID:
		json.NewEncoder(w).Encode(a.session)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/control":
		var input struct {
			ID           string `json:"id"`
			ScopeID      string `json:"scope_id"`
			OwnerAgentID string `json:"owner_agent_id"`
			Method       string `json:"method"`
			RequestID    string `json:"request_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			a.t.Error(err)
		}
		if input.ID != a.session.ID || input.ScopeID != a.session.ScopeID || input.OwnerAgentID != a.session.OwnerAgentID || input.RequestID != a.requestID {
			a.t.Error("control did not preserve saved identity and supplied request ID")
		}
		a.methods = append(a.methods, input.Method)
		switch input.Method {
		case "verify":
			a.session.State = "connected"
		case "close":
			a.session.Disposition = "closing"
		case "abandon":
			a.session.State, a.session.Disposition, a.session.ReleaseReason = "lost", "released", "abandoned"
		}
		json.NewEncoder(w).Encode(a.session)
	default:
		a.t.Errorf("unexpected session request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (a *SessionAPI) SetState(state string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.session.State = state
}

func (a *SessionAPI) Snapshot() SessionAPISnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return SessionAPISnapshot{Session: cloneSession(a.session), Methods: slices.Clone(a.methods)}
}

func cloneSession(session adminsessions.Session) adminsessions.Session {
	if session.ConnectedAt != nil {
		value := *session.ConnectedAt
		session.ConnectedAt = &value
	}
	if session.VerifiedAt != nil {
		value := *session.VerifiedAt
		session.VerifiedAt = &value
	}
	return session
}
