package endpoints

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretservice "github.com/defos-ai/commander/framework/secrets"
	"github.com/google/jsonschema-go/jsonschema"
)

const secretRequestsPath = secretsPath + "/requests"

func RegisterSecretRequests(mux *http.ServeMux, service *secretservice.Service, requests secrets.RequestStore) {
	mux.HandleFunc("GET "+secretRequestsPath, RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if service == nil || requests == nil {
			WriteError(w, http.StatusServiceUnavailable, "secrets_unavailable")
			return
		}
		query, ok := readSecretQuery(w, r, "agent_type", "agent_id")
		if !ok {
			return
		}
		if (query.Has("agent_type") && !id.ValidType(query.Get("agent_type"))) ||
			(query.Has("agent_id") && !id.ValidAgent(query.Get("agent_id"))) {
			WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		items, err := listSecretRequests(r.Context(), service, requests, query.Get("agent_type"), query.Get("agent_id"), time.Now())
		if writeSecretError(w, err) {
			return
		}
		WriteJSON(w, http.StatusOK, ListResponse(items))
	}))
}

func listSecretRequests(ctx context.Context, service *secretservice.Service, requests secrets.RequestStore, agentType, agentID string, now time.Time) ([]models.SecretRequestResponse, error) {
	records, err := requests.ListPending(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]models.SecretRequestResponse, 0, len(records))
	metadata := make(map[secrets.Key]*secrets.Metadata)
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := record.Validate(); err != nil {
			return nil, err
		}
		request := record.Request
		if request.Status != secrets.Pending || !now.Before(request.ExpiresAt) ||
			(agentType != "" && request.Key.Scope.AgentType != agentType) ||
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

func requestedSecretMetadata(ctx context.Context, service *secretservice.Service, key secrets.Key) (*secrets.Metadata, error) {
	metadata, err := service.GetMetadata(ctx, key)
	if errors.Is(err, secrets.ErrUnknownSecret) {
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

func secretRequestStatus(record secrets.RequestRecord, current *secrets.Metadata, now time.Time) string {
	if secrets.AvailabilityAt(current, now) != secrets.Available || current.UpdatedAt.After(now) {
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

func RegisterSecretRequestsOpenAPI(paths, components map[string]any, options *jsonschema.ForOptions) error {
	typeOf := reflect.TypeFor[models.SecretRequestResponse]()
	schema, err := jsonschema.ForType(typeOf, options)
	if err != nil {
		return fmt.Errorf("describe API SecretRequest: %w", err)
	}
	schema.Description = "Pending request metadata only; never includes secret values, encrypted envelopes or encryption keys."
	schema.Properties["id"].Pattern, schema.Properties["id"].Format = secretIDPattern, "uuid"
	schema.Properties["agent_type"].Pattern = secretScopePattern
	maximum := 63
	schema.Properties["agent_type"].MaxLength = &maximum
	schema.Properties["agent_id"].Pattern = secretRequestAgentIDPattern
	schema.Properties["agent_id"].MaxLength = &maximum
	schema.Properties["name"].Pattern = secretNamePattern
	schema.Properties["status"].Enum = []any{"needs_value", "awaiting_delivery", "awaiting_installation"}
	schema.Properties["status"].Description = "needs_value: the current secret is absent or unavailable. awaiting_delivery: a current value exists without a matching valid reserved delivery. awaiting_installation: a matching delivery is reserved, but installation has not been acknowledged; publication is not guaranteed."
	components["SecretRequest"] = schema
	options.TypeSchemas[typeOf] = schema
	operation := OpenAPIQueryOperation("listSecretRequests", "List pending secret requests", "Secrets", map[string]any{
		"type": "object", "required": []string{"items"},
		"properties": map[string]any{"items": map[string]any{"type": "array", "items": OpenAPIRef("SecretRequest")}},
	})
	operation["description"] = "Returns pending, unexpired requests sorted by request ID, without pagination or changing saved state. Optional filters intersect. Unknown, repeated, empty or malformed query parameters are rejected. This is an eventually consistent snapshot across request and secret stores, not agent readiness: lease refreshes can overlap for one agent and key. Responses use Cache-Control: no-store."
	operation["parameters"] = []any{
		secretParameter("agent_type", "query", false, map[string]any{"type": "string", "pattern": secretScopePattern, "maxLength": 63}),
		secretParameter("agent_id", "query", false, map[string]any{"type": "string", "pattern": secretRequestAgentIDPattern, "maxLength": 63}),
	}
	responses := operation["responses"].(map[string]any)
	responses["400"] = OpenAPIResponse("invalid_request or invalid_request_origin", OpenAPIRef("Error"))
	responses["503"] = OpenAPIResponse("secrets_unavailable or request_canceled. Secret management requires configured encryption and a request store.", OpenAPIRef("Error"))
	responses["504"] = OpenAPIResponse("request_timeout after at most 10 seconds. Reads do not change saved state.", OpenAPIRef("Error"))
	paths[secretRequestsPath] = map[string]any{"get": operation}
	return nil
}

const secretRequestAgentIDPattern = `^[a-z0-9]+(-[a-z0-9]+)*-agent-[0-9a-f]{8}$`
