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

const secretsPath = "/api/v1/secrets"
const secretPath = secretsPath + "/{name}"

func Register(mux *http.ServeMux, service *secretservice.Service) {
	handlers := &secretHandler{service: service}
	for _, route := range []struct {
		pattern string
		handle  http.HandlerFunc
	}{
		{"GET " + secretsPath, handlers.list},
		{"POST " + secretsPath, handlers.create},
		{"GET " + secretPath, handlers.get},
		{"PUT " + secretPath, handlers.replace},
		{"DELETE " + secretPath, handlers.delete},
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

type secretHandler struct {
	service *secretservice.Service
}

func (h *secretHandler) list(w http.ResponseWriter, r *http.Request) {
	if _, ok := readSecretQuery(w, r); !ok {
		return
	}
	items, err := h.service.List(r.Context())
	if writeSecretError(w, err) {
		return
	}
	endpoints.WriteJSON(w, http.StatusOK, endpoints.ListResponse(mappings.ToSecretResponses(items, time.Now())))
}

func (h *secretHandler) create(w http.ResponseWriter, r *http.Request) {
	if _, ok := readSecretQuery(w, r); !ok {
		return
	}
	var request models.CreateSecretRequest
	if !endpoints.ReadJSON(w, r, &request, false) {
		return
	}
	key := secretmodel.Key{Name: request.Name}
	if key.Validate() != nil || request.Value == nil || (request.ExpiresAt != nil && request.ExpiresAt.IsZero()) {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	metadata, err := h.service.Create(r.Context(), key, []byte(*request.Value), secretExpiration(request.ExpiresAt))
	if writeSecretError(w, err) {
		return
	}
	w.Header().Set("Location", secretsPath+"/"+url.PathEscape(key.Name))
	endpoints.WriteJSON(w, http.StatusCreated, mappings.ToSecretResponse(metadata, time.Now()))
}

func (h *secretHandler) get(w http.ResponseWriter, r *http.Request) {
	if _, ok := readSecretQuery(w, r); !ok {
		return
	}
	key, ok := readSecretKey(w, r)
	if !ok {
		return
	}
	metadata, err := h.service.GetMetadata(r.Context(), key)
	if writeSecretError(w, err) {
		return
	}
	endpoints.WriteJSON(w, http.StatusOK, mappings.ToSecretResponse(metadata, time.Now()))
}

func (h *secretHandler) replace(w http.ResponseWriter, r *http.Request) {
	if _, ok := readSecretQuery(w, r); !ok {
		return
	}
	key, ok := readSecretKey(w, r)
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
	metadata, err := h.service.Replace(r.Context(), key, request.ID, request.Version, []byte(*request.Value), secretExpiration(request.ExpiresAt))
	if writeSecretError(w, err) {
		return
	}
	endpoints.WriteJSON(w, http.StatusOK, mappings.ToSecretResponse(metadata, time.Now()))
}

func (h *secretHandler) delete(w http.ResponseWriter, r *http.Request) {
	query, ok := readSecretQuery(w, r, "id", "version")
	if !ok {
		return
	}
	key, ok := readSecretKey(w, r)
	if !ok {
		return
	}
	version, err := strconv.ParseUint(query.Get("version"), 10, 64)
	if err != nil || version == 0 || !id.Valid(query.Get("id")) {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if writeSecretError(w, h.service.Delete(r.Context(), key, query.Get("id"), version)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readSecretQuery(w http.ResponseWriter, r *http.Request, allowed ...string) (url.Values, bool) {
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

func readSecretKey(w http.ResponseWriter, r *http.Request) (secretmodel.Key, bool) {
	key := secretmodel.Key{Name: r.PathValue("name")}
	if key.Validate() != nil {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
		return secretmodel.Key{}, false
	}
	return key, true
}

func secretExpiration(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func writeSecretError(w http.ResponseWriter, err error) bool {
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
