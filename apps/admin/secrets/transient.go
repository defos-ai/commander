package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/defos-ai/commander/apps/admin/client"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

type TransientRequest struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Name        string    `json:"name"`
	AgentType   string    `json:"agent_type"`
	AgentID     string    `json:"agent_id"`
	OperationID string    `json:"operation_id"`
	TaskID      string    `json:"task_id"`
	CommandID   string    `json:"command_id"`
	RequestedAt time.Time `json:"requested_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Status      string    `json:"status"`
}

func TransientRequests(ctx context.Context, api *client.Client, agentID string) ([]TransientRequest, error) {
	path := "/api/v1/secret-requests?type=transient"
	if agentID != "" {
		if !id.ValidAgent(agentID) {
			return nil, errors.New("invalid agent ID")
		}
		path += "&agent_id=" + url.QueryEscape(agentID)
	}
	response, err := api.Do(ctx, http.MethodGet, path, nil, maxListBytes)
	if err != nil {
		return nil, transientError(err, false)
	}
	defer clear(response.Data)
	var envelope struct {
		Items []TransientRequest `json:"items"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(response.Data, &envelope) != nil || envelope.Items == nil {
		return nil, client.ErrInvalidResponse
	}
	seen := make(map[string]bool)
	for _, request := range envelope.Items {
		if !validTransientRequest(request) || request.Status != "pending" ||
			agentID != "" && request.AgentID != agentID || seen[request.ID] {
			return nil, client.ErrInvalidResponse
		}
		seen[request.ID] = true
	}
	return envelope.Items, nil
}

func GetTransientRequest(ctx context.Context, api *client.Client, requestID string) (*TransientRequest, error) {
	if !id.Valid(requestID) {
		return nil, errors.New("invalid secret request ID")
	}
	response, err := api.Do(ctx, http.MethodGet, transientRequestPath(requestID), nil, maxMetadataBytes)
	if err != nil {
		return nil, transientError(err, false)
	}
	defer clear(response.Data)
	var request TransientRequest
	if response.StatusCode != http.StatusOK || json.Unmarshal(response.Data, &request) != nil ||
		!validTransientRequest(request) || request.ID != requestID {
		return nil, client.ErrInvalidResponse
	}
	return &request, nil
}

// Success confirms submission only. Receipt requires a fresh request metadata read.
func ProvideTransient(ctx context.Context, api *client.Client, requestID string, value string) error {
	if !id.Valid(requestID) {
		return errors.New("invalid secret request ID")
	}
	if !utf8.ValidString(value) || len(value) > secretmodel.MaxTransientSecretBytes {
		return errors.New("one-time value must be valid UTF-8 and at most 64 KiB")
	}
	body, err := json.Marshal(struct {
		Value string `json:"value"`
	}{value})
	value = ""
	defer clear(body)
	if err != nil {
		return errors.New("could not encode one-time value")
	}
	response, err := api.Do(ctx, http.MethodPost, transientRequestPath(requestID)+"/values", body, maxMetadataBytes)
	if err != nil {
		return transientError(err, true)
	}
	defer clear(response.Data)
	var acknowledgment map[string]json.RawMessage
	var status string
	if response.StatusCode != http.StatusAccepted || json.Unmarshal(response.Data, &acknowledgment) != nil ||
		len(acknowledgment) != 1 || json.Unmarshal(acknowledgment["status"], &status) != nil || status != "awaiting_receipt" {
		return client.ErrWriteUncertain
	}
	return nil
}

func validTransientRequest(request TransientRequest) bool {
	if request.Type != string(secretmodel.SecretTransient) || !id.Valid(request.ID) ||
		(secretmodel.Key{Name: request.Name}).Validate() != nil || !id.ValidType(request.AgentType) || !id.ValidAgent(request.AgentID) ||
		!id.Valid(request.OperationID) || !id.Valid(request.TaskID) || !id.Valid(request.CommandID) ||
		!validTime(request.RequestedAt) || !validTime(request.ExpiresAt) || !request.ExpiresAt.After(request.RequestedAt) {
		return false
	}
	switch request.Status {
	case "pending", "fulfilled", "cancelled", "expired":
		return true
	default:
		return false
	}
}

func transientRequestPath(requestID string) string {
	return "/api/v1/secret-requests/" + requestID
}

func transientError(err error, mutation bool) error {
	var status *client.StatusError
	if errors.As(err, &status) && status.StatusCode == http.StatusConflict {
		return errors.Join(ErrConflict, err)
	}
	return secretError(err, mutation)
}
