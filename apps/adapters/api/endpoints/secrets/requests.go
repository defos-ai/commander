package secrets

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/comms"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretservice "github.com/defos-ai/commander/framework/secrets"
)

const secretRequestsPath = "/api/v1/secret-requests"
const secretRequestPath = secretRequestsPath + "/{request_id}"
const secretRequestValuesPath = secretRequestPath + "/values"

func RegisterRequests(mux *http.ServeMux, service *secretservice.Service, requests secretmodel.RequestStore, commands controllercommands.SecretRequests) {
	mux.HandleFunc("GET "+secretRequestsPath, endpoints.RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if service == nil || requests == nil {
			endpoints.WriteError(w, http.StatusServiceUnavailable, "secrets_unavailable")
			return
		}
		query, ok := readSecretQuery(w, r, "type", "agent_type", "agent_id")
		if !ok {
			return
		}
		kind := secretmodel.SecretType(query.Get("type"))
		if (query.Has("type") && kind != secretmodel.SecretReusable && kind != secretmodel.SecretTransient) ||
			(query.Has("agent_type") && !id.ValidType(query.Get("agent_type"))) ||
			(query.Has("agent_id") && !id.ValidAgent(query.Get("agent_id"))) {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		items, err := listSecretRequests(r.Context(), service, requests, kind, query.Get("agent_type"), query.Get("agent_id"), time.Now())
		if writeSecretError(w, err) {
			return
		}
		endpoints.WriteJSON(w, http.StatusOK, endpoints.ListResponse(items))
	}))
	mux.HandleFunc("GET "+secretRequestPath, endpoints.RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if service == nil || requests == nil {
			endpoints.WriteError(w, http.StatusServiceUnavailable, "secrets_unavailable")
			return
		}
		if _, ok := readSecretQuery(w, r); !ok {
			return
		}
		requestID := r.PathValue("request_id")
		if !id.Valid(requestID) {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		record, err := requests.Get(r.Context(), requestID)
		if errors.Is(err, secretmodel.ErrUnknownRequest) {
			endpoints.WriteError(w, http.StatusNotFound, "secret_request_not_found")
			return
		}
		if writeSecretError(w, err) {
			return
		}
		response, err := secretRequestResponse(r.Context(), service, record, make(map[secretmodel.Key]*secretmodel.Metadata), time.Now())
		if writeSecretError(w, err) {
			return
		}
		endpoints.WriteJSON(w, http.StatusOK, response)
	}))
	mux.HandleFunc("POST "+secretRequestValuesPath, endpoints.RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if service == nil || requests == nil || commands == nil {
			endpoints.WriteError(w, http.StatusServiceUnavailable, "secrets_unavailable")
			return
		}
		if _, ok := readSecretQuery(w, r); !ok {
			return
		}
		requestID := r.PathValue("request_id")
		if !id.Valid(requestID) {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var request models.ProvideSecretValueRequest
		if !endpoints.ReadJSON(w, r, &request, false) {
			return
		}
		if request.Value == nil || !utf8.ValidString(*request.Value) || len(*request.Value) > secretmodel.MaxTransientSecretBytes {
			request.Value = nil
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		value := []byte(*request.Value)
		request.Value = nil
		defer clear(value)
		if writeSecretRequestError(w, commands.ProvideTransient(r.Context(), requestID, value)) {
			return
		}
		endpoints.WriteJSON(w, http.StatusAccepted, models.SecretValueSubmissionResponse{Status: "awaiting_receipt"})
	}))
}

func listSecretRequests(ctx context.Context, service *secretservice.Service, requests secretmodel.RequestStore, kind secretmodel.SecretType, agentType, agentID string, now time.Time) ([]models.SecretRequestResponse, error) {
	records, err := requests.ListPending(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]models.SecretRequestResponse, 0, len(records))
	metadata := make(map[secretmodel.Key]*secretmodel.Metadata)
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := record.Validate(); err != nil {
			return nil, err
		}
		request := record.Request
		if request.Status != secretmodel.Pending || !now.Before(request.ExpiresAt) ||
			(kind != "" && request.EffectiveType() != kind) ||
			(agentType != "" && request.AgentType != agentType) ||
			(agentID != "" && request.AgentID != agentID) {
			continue
		}
		response, err := secretRequestResponse(ctx, service, record, metadata, now)
		if err != nil {
			return nil, err
		}
		items = append(items, response)
	}
	slices.SortFunc(items, func(a, b models.SecretRequestResponse) int { return cmp.Compare(a.ID, b.ID) })
	return items, ctx.Err()
}

func secretRequestResponse(ctx context.Context, service *secretservice.Service, record secretmodel.RequestRecord, metadata map[secretmodel.Key]*secretmodel.Metadata, now time.Time) (models.SecretRequestResponse, error) {
	if err := record.Validate(); err != nil {
		return models.SecretRequestResponse{}, err
	}
	request := record.Request
	status := string(request.Status)
	if request.Status != secretmodel.Pending {
		return mappings.ToSecretRequestResponse(request, status), nil
	}
	if !now.Before(request.ExpiresAt) {
		return mappings.ToSecretRequestResponse(request, string(secretmodel.RequestExpired)), nil
	}
	if request.EffectiveType() == secretmodel.SecretTransient {
		return mappings.ToSecretRequestResponse(request, status), nil
	}
	current, found := metadata[request.Key]
	if !found {
		var err error
		current, err = requestedSecretMetadata(ctx, service, request.Key)
		if err != nil {
			return models.SecretRequestResponse{}, err
		}
		metadata[request.Key] = current
	}
	return mappings.ToSecretRequestResponse(request, secretRequestStatus(record, current, now)), nil
}

func writeSecretRequestError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	status, code := http.StatusServiceUnavailable, "secret_delivery_uncertain"
	switch {
	case errors.Is(err, secretmodel.ErrWriteUncertain):
		code = "secret_write_uncertain"
	case errors.Is(err, secretmodel.ErrDeliveryUncertain):
		code = "secret_delivery_uncertain"
	case errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusGatewayTimeout, "request_timeout"
	case errors.Is(err, context.Canceled):
		code = "request_canceled"
	case errors.Is(err, secretmodel.ErrUnavailable):
		code = "secrets_unavailable"
	case errors.Is(err, domain.ErrControllerClosed), errors.Is(err, comms.ErrClosed):
		code = "controller_unavailable"
	case errors.Is(err, secretmodel.ErrUnknownRequest):
		status, code = http.StatusNotFound, "secret_request_not_found"
	case errors.Is(err, secretmodel.ErrRequestConflict):
		status, code = http.StatusConflict, "secret_request_conflict"
	case errors.Is(err, secretmodel.ErrRequestClosed):
		status, code = http.StatusConflict, "secret_request_closed"
	case errors.Is(err, secretmodel.ErrRequestExpired):
		status, code = http.StatusConflict, "secret_request_expired"
	case errors.Is(err, domain.ErrInvalidInput):
		status, code = http.StatusBadRequest, "invalid_request"
	}
	endpoints.WriteError(w, status, code)
	return true
}

func requestedSecretMetadata(ctx context.Context, service *secretservice.Service, key secretmodel.Key) (*secretmodel.Metadata, error) {
	metadata, err := service.GetMetadata(ctx, key)
	if errors.Is(err, secretmodel.ErrUnknownSecret) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if metadata.Validate() != nil || metadata.Key != key {
		return nil, errors.New("invalid requested secret metadata")
	}
	return &metadata, nil
}

func secretRequestStatus(record secretmodel.RequestRecord, current *secretmodel.Metadata, now time.Time) string {
	if secretmodel.AvailabilityAt(current, now) != secretmodel.Available || current.UpdatedAt.After(now) {
		return "needs_value"
	}
	delivery := record.Delivery
	if delivery == nil || delivery.Secret.Metadata.ID != current.ID || delivery.Secret.Metadata.Version != current.Version ||
		delivery.CreatedAt.After(now) || !now.Before(delivery.ExpiresAt) {
		return "awaiting_delivery"
	}
	// Reservation is durable evidence, not proof of publication or agent readiness.
	return "awaiting_installation"
}
