package secrets

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretservice "github.com/defos-ai/commander/framework/secrets"
)

const secretRequestsPath = "/api/v1/secret-requests"

func RegisterRequests(mux *http.ServeMux, service *secretservice.Service, requests secretmodel.RequestStore) {
	mux.HandleFunc("GET "+secretRequestsPath, endpoints.RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if service == nil || requests == nil {
			endpoints.WriteError(w, http.StatusServiceUnavailable, "secrets_unavailable")
			return
		}
		query, ok := readSecretQuery(w, r, "agent_type", "agent_id")
		if !ok {
			return
		}
		if (query.Has("agent_type") && !id.ValidType(query.Get("agent_type"))) ||
			(query.Has("agent_id") && !id.ValidAgent(query.Get("agent_id"))) {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		items, err := listSecretRequests(r.Context(), service, requests, query.Get("agent_type"), query.Get("agent_id"), time.Now())
		if writeSecretError(w, err) {
			return
		}
		endpoints.WriteJSON(w, http.StatusOK, endpoints.ListResponse(items))
	}))
}

func listSecretRequests(ctx context.Context, service *secretservice.Service, requests secretmodel.RequestStore, agentType, agentID string, now time.Time) ([]models.SecretRequestResponse, error) {
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
			(agentType != "" && request.AgentType != agentType) ||
			(agentID != "" && request.AgentID != agentID) {
			continue
		}
		current, found := metadata[request.Key]
		if !found {
			current, err = requestedSecretMetadata(ctx, service, request.Key)
			if err != nil {
				return nil, err
			}
			metadata[request.Key] = current
		}
		items = append(items, mappings.ToSecretRequestResponse(request, secretRequestStatus(record, current, now)))
	}
	slices.SortFunc(items, func(a, b models.SecretRequestResponse) int { return cmp.Compare(a.ID, b.ID) })
	return items, ctx.Err()
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
