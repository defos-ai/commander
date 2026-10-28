package agents

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/agents"
)

const agentSourcesPath = "/api/v1/agent-sources"
const agentSourcePath = agentSourcesPath + "/{source}"

func registerSources(mux *http.ServeMux, store agents.AgentSourceStore) {
	handler := &sourceHandler{store: store}
	for _, route := range []struct {
		pattern string
		handle  http.HandlerFunc
	}{
		{"GET " + agentSourcesPath, handler.list},
		{"POST " + agentSourcesPath, handler.create},
		{"GET " + agentSourcePath, handler.get},
		{"PATCH " + agentSourcePath, handler.setTrust},
	} {
		mux.HandleFunc(route.pattern, endpoints.RequestHandler(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if r.URL.RawQuery != "" {
				endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
				return
			}
			route.handle(w, r)
		}))
	}
}

type sourceHandler struct {
	store agents.AgentSourceStore
}

func (h *sourceHandler) list(w http.ResponseWriter, r *http.Request) {
	entries, err := h.store.List(r.Context())
	if writeAgentSourceError(w, err) {
		return
	}
	for _, entry := range entries {
		if writeAgentSourceError(w, entry.Validate()) {
			return
		}
	}
	endpoints.WriteJSON(w, http.StatusOK, endpoints.ListResponse(mappings.ToAgentSourceResponses(entries)))
}

func (h *sourceHandler) create(w http.ResponseWriter, r *http.Request) {
	var request models.CreateAgentSourceRequest
	if !endpoints.ReadJSON(w, r, &request, false) {
		return
	}
	entry := mappings.ToAgentSource(request, time.Now().UTC())
	if entry.Validate() != nil {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if writeAgentSourceError(w, h.store.Create(r.Context(), entry)) {
		return
	}
	w.Header().Set("Location", agentSourcesPath+"/"+url.PathEscape(entry.Source))
	endpoints.WriteJSON(w, http.StatusCreated, mappings.ToAgentSourceResponse(entry))
}

func (h *sourceHandler) get(w http.ResponseWriter, r *http.Request) {
	value, ok := readSource(w, r)
	if !ok {
		return
	}
	entry, err := h.store.Get(r.Context(), value)
	if writeAgentSourceError(w, err) || !validSavedSource(w, entry, value) {
		return
	}
	endpoints.WriteJSON(w, http.StatusOK, mappings.ToAgentSourceResponse(entry))
}

func (h *sourceHandler) setTrust(w http.ResponseWriter, r *http.Request) {
	value, ok := readSource(w, r)
	if !ok {
		return
	}
	var request models.SetAgentSourceTrustRequest
	if !endpoints.ReadJSON(w, r, &request, false) {
		return
	}
	if request.Version == 0 || request.Trusted == nil {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if *request.Trusted {
		if _, err := packages.ParseReference(value); err != nil {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	actor, _, authenticated := r.BasicAuth()
	if !authenticated || actor == "" {
		endpoints.WriteError(w, http.StatusUnauthorized, "authentication_required")
		return
	}
	entry, err := h.store.Get(r.Context(), value)
	if writeAgentSourceError(w, err) || !validSavedSource(w, entry, value) {
		return
	}
	if entry.Version != request.Version || entry.Version == ^uint64(0) {
		writeAgentSourceError(w, agents.ErrAgentSourceConflict)
		return
	}
	now := time.Now().UTC()
	if now.Before(entry.UpdatedAt) {
		now = entry.UpdatedAt
	}
	entry.Trusted, entry.Version, entry.UpdatedAt = *request.Trusted, entry.Version+1, now
	entry.TrustUpdatedBy, entry.TrustUpdatedAt = actor, now
	if writeAgentSourceError(w, entry.Validate()) || writeAgentSourceError(w, h.store.Replace(r.Context(), entry, request.Version)) {
		return
	}
	endpoints.WriteJSON(w, http.StatusOK, mappings.ToAgentSourceResponse(entry))
}

func readSource(w http.ResponseWriter, r *http.Request) (string, bool) {
	value := r.PathValue("source")
	if agents.ValidateAgentSource(value) != nil {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
		return "", false
	}
	return value, true
}

func validSavedSource(w http.ResponseWriter, entry agents.AgentSource, value string) bool {
	if entry.Source != value || entry.Validate() != nil {
		endpoints.WriteError(w, http.StatusInternalServerError, "internal_error")
		return false
	}
	return true
}

func writeAgentSourceError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, agents.ErrAgentSourceWriteUncertain):
		status, code = http.StatusServiceUnavailable, "agent_source_write_uncertain"
	case errors.Is(err, agents.ErrAgentSourceExists):
		status, code = http.StatusConflict, "agent_source_exists"
	case errors.Is(err, agents.ErrAgentSourceConflict):
		status, code = http.StatusConflict, "agent_source_conflict"
	case errors.Is(err, agents.ErrUnknownAgentSource):
		status, code = http.StatusNotFound, "agent_source_not_found"
	case errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusGatewayTimeout, "request_timeout"
	case errors.Is(err, context.Canceled):
		status, code = http.StatusServiceUnavailable, "request_canceled"
	}
	endpoints.WriteError(w, status, code)
	return true
}
