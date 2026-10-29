package fakes

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

type MissionAPIConfig struct {
	Records             map[string]json.RawMessage
	DropAcknowledgement string
	UpdateStatus        int
	UpdateResponse      json.RawMessage
}

type MissionAPISnapshot struct {
	Records  map[string]json.RawMessage
	Requests []string
}

type MissionAPI struct {
	t                   testing.TB
	mu                  sync.Mutex
	records             map[string]json.RawMessage
	requests            []string
	dropAcknowledgement string
	updateStatus        int
	updateResponse      json.RawMessage
}

func NewMissionAPI(t testing.TB, config MissionAPIConfig) *MissionAPI {
	t.Helper()
	api := &MissionAPI{t: t, records: make(map[string]json.RawMessage), dropAcknowledgement: config.DropAcknowledgement,
		updateStatus: config.UpdateStatus, updateResponse: bytes.Clone(config.UpdateResponse)}
	for name, definition := range config.Records {
		api.records[name] = bytes.Clone(definition)
	}
	return api
}

func (a *MissionAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/missions" {
		a.requests = append(a.requests, "GET list")
		names := make([]string, 0, len(a.records))
		for name := range a.records {
			names = append(names, name)
		}
		sort.Strings(names)
		items := make([]json.RawMessage, len(names))
		for i, name := range names {
			items[i] = a.records[name]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/missions/") {
		name := strings.TrimPrefix(r.URL.Path, "/api/v1/missions/")
		a.requests = append(a.requests, "GET "+name)
		if data, exists := a.records[name]; exists {
			_, _ = w.Write(data)
		} else {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"mission_not_found"}`)
		}
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/v1/missions" {
		data, err := io.ReadAll(r.Body)
		var definition struct{ Name string }
		if err != nil || json.Unmarshal(data, &definition) != nil {
			a.t.Error("mission command sent invalid JSON")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		a.requests = append(a.requests, "POST "+definition.Name)
		if _, exists := a.records[definition.Name]; exists {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"error":"mission_exists"}`)
			return
		}
		a.records[definition.Name] = data
		if definition.Name == a.dropAcknowledgement {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				a.t.Error("could not simulate a lost write acknowledgement")
				return
			}
			_ = connection.Close()
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(data)
		return
	}
	if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/missions/") {
		name := strings.TrimPrefix(r.URL.Path, "/api/v1/missions/")
		a.requests = append(a.requests, "PUT "+name)
		if _, exists := a.records[name]; !exists {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"mission_not_found"}`)
			return
		}
		data, err := io.ReadAll(r.Body)
		var definition struct{ Name string }
		if err != nil || json.Unmarshal(data, &definition) != nil || definition.Name != name {
			a.t.Error("mission update changed the target name or sent invalid JSON")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		a.records[name] = data
		if name == a.dropAcknowledgement {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				a.t.Error("could not simulate a lost update acknowledgement")
				return
			}
			_ = connection.Close()
			return
		}
		status := a.updateStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		if a.updateResponse != nil {
			data = a.updateResponse
		}
		_, _ = w.Write(data)
		return
	}
	a.t.Errorf("unexpected mission API request: %s %s", r.Method, r.URL.Path)
	w.WriteHeader(http.StatusNotFound)
}

func (a *MissionAPI) Snapshot() MissionAPISnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := MissionAPISnapshot{Records: make(map[string]json.RawMessage, len(a.records)), Requests: slices.Clone(a.requests)}
	for name, definition := range a.records {
		state.Records[name] = bytes.Clone(definition)
	}
	return state
}

func (a *MissionAPI) ReplaceMissionAndResetRequests(name string, definition json.RawMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records[name] = bytes.Clone(definition)
	a.requests = nil
}
