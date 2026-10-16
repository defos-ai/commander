package secrets

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretservice "github.com/defos-ai/commander/framework/secrets"
)

const rootPath = "/api/v1/secrets"
const detailPath = rootPath + "/{agent_type}/{profile}/{name}"

func Register(mux *http.ServeMux, service *secretservice.Service) {
	handlers := &handler{service: service}
	for _, route := range []struct {
		pattern string
		handle  http.HandlerFunc
	}{
		{"GET " + rootPath, handlers.list},
		{"POST " + rootPath, handlers.create},
		{"GET " + detailPath, handlers.get},
		{"PUT " + detailPath, handlers.replace},
		{"DELETE " + detailPath, handlers.delete},
	} {
		mux.HandleFunc(route.pattern, endpoints.RequestHandler(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if service == nil {
				endpoints.WriteError(w, http.StatusServiceUnavailable, "secrets_unavailable")
				return
			}
			route.handle(w, r)
		}))
	}
}

type handler struct {
	service *secretservice.Service
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	query, ok := readQuery(w, r, "agent_type", "profile")
	if !ok {
		return
	}
	scope, err := secretmodel.NewScope(query.Get("agent_type"), query.Get("profile"))
	if err != nil {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	items, err := h.service.List(r.Context(), scope)
	if writeError(w, err) {
		return
	}
	endpoints.WriteJSON(w, http.StatusOK, endpoints.ListResponse(mappings.ToSecretResponses(items, time.Now())))
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	if _, ok := readQuery(w, r); !ok {
		return
	}
	var request models.CreateSecretRequest
	if !endpoints.ReadJSON(w, r, &request, false) {
		return
	}
	scope, err := secretmodel.NewScope(request.AgentType, request.Profile)
	key := secretmodel.Key{Scope: scope, Name: request.Name}
	if err != nil || key.Validate() != nil || request.Value == nil || (request.ExpiresAt != nil && request.ExpiresAt.IsZero()) {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	metadata, err := h.service.Create(r.Context(), key, []byte(*request.Value), expiration(request.ExpiresAt))
	if writeError(w, err) {
		return
	}
	w.Header().Set("Location", rootPath+"/"+key.Scope.AgentType+"/"+key.Scope.Profile+"/"+key.Name)
	endpoints.WriteJSON(w, http.StatusCreated, mappings.ToSecretResponse(metadata, time.Now()))
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	if _, ok := readQuery(w, r); !ok {
		return
	}
	key, ok := readKey(w, r)
	if !ok {
		return
	}
	metadata, err := h.service.GetMetadata(r.Context(), key)
	if writeError(w, err) {
		return
	}
	endpoints.WriteJSON(w, http.StatusOK, mappings.ToSecretResponse(metadata, time.Now()))
}

func (h *handler) replace(w http.ResponseWriter, r *http.Request) {
	if _, ok := readQuery(w, r); !ok {
		return
	}
	key, ok := readKey(w, r)
	if !ok {
		return
	}
	var request models.ReplaceSecretRequest
	if !endpoints.ReadJSON(w, r, &request, false) {
		return
	}
	if !id.Valid(request.ID) || request.Version == 0 || request.Value == nil || (request.ExpiresAt != nil && request.ExpiresAt.IsZero()) {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	metadata, err := h.service.Replace(r.Context(), key, request.ID, request.Version, []byte(*request.Value), expiration(request.ExpiresAt))
	if writeError(w, err) {
		return
	}
	endpoints.WriteJSON(w, http.StatusOK, mappings.ToSecretResponse(metadata, time.Now()))
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	query, ok := readQuery(w, r, "id", "version")
	if !ok {
		return
	}
	key, ok := readKey(w, r)
	if !ok {
		return
	}
	version, err := strconv.ParseUint(query.Get("version"), 10, 64)
	if err != nil || version == 0 || !id.Valid(query.Get("id")) {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if writeError(w, h.service.Delete(r.Context(), key, query.Get("id"), version)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readQuery(w http.ResponseWriter, r *http.Request, allowed ...string) (url.Values, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
		return nil, false
	}
	for name, values := range query {
		if !slices.Contains(allowed, name) || len(values) != 1 {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return nil, false
		}
	}
	return query, true
}

func readKey(w http.ResponseWriter, r *http.Request) (secretmodel.Key, bool) {
	key := secretmodel.Key{
		Scope: secretmodel.Scope{AgentType: r.PathValue("agent_type"), Profile: r.PathValue("profile")},
		Name:  r.PathValue("name"),
	}
	if key.Validate() != nil {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
		return secretmodel.Key{}, false
	}
	return key, true
}

func expiration(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func writeError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, secretmodel.ErrWriteUncertain):
		status, code = http.StatusServiceUnavailable, "secret_write_uncertain"
	case errors.Is(err, domain.ErrInvalidInput):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, secretmodel.ErrUnknownSecret):
		status, code = http.StatusNotFound, "secret_not_found"
	case errors.Is(err, secretmodel.ErrSecretConflict):
		status, code = http.StatusConflict, "secret_conflict"
	case errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusGatewayTimeout, "request_timeout"
	case errors.Is(err, context.Canceled):
		status, code = http.StatusServiceUnavailable, "request_canceled"
	}
	endpoints.WriteError(w, status, code)
	return true
}
