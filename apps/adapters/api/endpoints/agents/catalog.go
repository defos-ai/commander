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

const catalogAgentsPath = "/api/v1/catalog-agents"
const catalogAgentPath = catalogAgentsPath + "/{package}"

func registerCatalog(mux *http.ServeMux, store agents.CatalogStore) {
	handler := &catalogHandler{store: store}
	for _, route := range []struct {
		pattern string
		handle  http.HandlerFunc
	}{
		{"GET " + catalogAgentsPath, handler.list},
		{"POST " + catalogAgentsPath, handler.create},
		{"GET " + catalogAgentPath, handler.get},
		{"PATCH " + catalogAgentPath, handler.setTrust},
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

type catalogHandler struct {
	store agents.CatalogStore
}

func (h *catalogHandler) list(w http.ResponseWriter, r *http.Request) {
	entries, err := h.store.List(r.Context())
	if writeCatalogError(w, err) {
		return
	}
	for _, entry := range entries {
		if writeCatalogError(w, entry.Validate()) {
			return
		}
	}
	endpoints.WriteJSON(w, http.StatusOK, endpoints.ListResponse(mappings.ToCatalogAgentResponses(entries)))
}

func (h *catalogHandler) create(w http.ResponseWriter, r *http.Request) {
	var request models.CreateCatalogAgentRequest
	if !endpoints.ReadJSON(w, r, &request, false) {
		return
	}
	entry := mappings.ToCatalogEntry(request, time.Now().UTC())
	if entry.Validate() != nil {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if writeCatalogError(w, h.store.Create(r.Context(), entry)) {
		return
	}
	w.Header().Set("Location", catalogAgentsPath+"/"+url.PathEscape(entry.Package))
	endpoints.WriteJSON(w, http.StatusCreated, mappings.ToCatalogAgentResponse(entry))
}

func (h *catalogHandler) get(w http.ResponseWriter, r *http.Request) {
	value, ok := readPackage(w, r)
	if !ok {
		return
	}
	entry, err := h.store.Get(r.Context(), value)
	if writeCatalogError(w, err) || !validSavedEntry(w, entry, value) {
		return
	}
	endpoints.WriteJSON(w, http.StatusOK, mappings.ToCatalogAgentResponse(entry))
}

func (h *catalogHandler) setTrust(w http.ResponseWriter, r *http.Request) {
	value, ok := readPackage(w, r)
	if !ok {
		return
	}
	var request models.SetCatalogAgentTrustRequest
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
	if writeCatalogError(w, err) || !validSavedEntry(w, entry, value) {
		return
	}
	if entry.Version != request.Version || entry.Version == ^uint64(0) {
		writeCatalogError(w, agents.ErrCatalogConflict)
		return
	}
	now := time.Now().UTC()
	if now.Before(entry.UpdatedAt) {
		now = entry.UpdatedAt
	}
	entry.Trusted, entry.Version, entry.UpdatedAt = *request.Trusted, entry.Version+1, now
	entry.TrustUpdatedBy, entry.TrustUpdatedAt = actor, now
	if writeCatalogError(w, entry.Validate()) || writeCatalogError(w, h.store.Replace(r.Context(), entry, request.Version)) {
		return
	}
	endpoints.WriteJSON(w, http.StatusOK, mappings.ToCatalogAgentResponse(entry))
}

func readPackage(w http.ResponseWriter, r *http.Request) (string, bool) {
	value := r.PathValue("package")
	if agents.ValidateCatalogPackage(value) != nil {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
		return "", false
	}
	return value, true
}

func validSavedEntry(w http.ResponseWriter, entry agents.CatalogEntry, value string) bool {
	if entry.Package != value || entry.Validate() != nil {
		endpoints.WriteError(w, http.StatusInternalServerError, "internal_error")
		return false
	}
	return true
}

func writeCatalogError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, agents.ErrCatalogWriteUncertain):
		status, code = http.StatusServiceUnavailable, "catalog_write_uncertain"
	case errors.Is(err, agents.ErrCatalogEntryExists):
		status, code = http.StatusConflict, "catalog_entry_exists"
	case errors.Is(err, agents.ErrCatalogConflict):
		status, code = http.StatusConflict, "catalog_conflict"
	case errors.Is(err, agents.ErrUnknownCatalogEntry):
		status, code = http.StatusNotFound, "catalog_entry_not_found"
	case errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusGatewayTimeout, "request_timeout"
	case errors.Is(err, context.Canceled):
		status, code = http.StatusServiceUnavailable, "request_canceled"
	}
	endpoints.WriteError(w, status, code)
	return true
}
