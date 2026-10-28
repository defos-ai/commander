package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/defos-ai/commander/apps/admin/client"
	model "github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

const (
	collectionPath  = "/api/v1/sessions"
	maxSessionBytes = 64 << 10
	maxListBytes    = 4 << 20
)

var ErrNotFound = errors.New("session not found")

type ControlResult struct {
	Session   Session `json:"session"`
	RequestID string  `json:"request_id"`
}

// ControlError retains the submitted identity without implying rejection or closure.
type ControlError struct {
	RequestID string
	cause     error
}

func (e *ControlError) Error() string {
	return fmt.Sprintf("session control failed (request ID %s); inspect the session before retrying: %s", e.RequestID, e.cause.Error())
}

func (e *ControlError) Unwrap() error { return e.cause }

func List(ctx context.Context, api *client.Client, filter Filter) ([]Session, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	path := collectionPath
	query := url.Values{}
	if filter.ScopeID != "" {
		query.Set("scope_id", filter.ScopeID)
	}
	if filter.OwnerAgentID != "" {
		query.Set("owner_agent_id", filter.OwnerAgentID)
	}
	if len(query) != 0 {
		path += "?" + query.Encode()
	}
	response, err := api.Do(ctx, http.MethodGet, path, nil, maxListBytes)
	if err != nil {
		return nil, err
	}
	defer clear(response.Data)
	var envelope struct {
		Items []Session `json:"items"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(response.Data, &envelope) != nil || envelope.Items == nil {
		return nil, client.ErrInvalidResponse
	}
	seen := make(map[string]bool)
	for _, item := range envelope.Items {
		if !item.valid() || seen[item.ID] || filter.ScopeID != "" && item.ScopeID != filter.ScopeID ||
			filter.OwnerAgentID != "" && item.OwnerAgentID != filter.OwnerAgentID {
			return nil, client.ErrInvalidResponse
		}
		seen[item.ID] = true
	}
	return envelope.Items, nil
}

func Get(ctx context.Context, api *client.Client, sessionID string) (Session, error) {
	if !id.Valid(sessionID) {
		return Session{}, errors.New("session ID must be a canonical UUID")
	}
	response, err := api.Do(ctx, http.MethodGet, collectionPath+"/"+sessionID, nil, maxSessionBytes)
	if err != nil {
		var status *client.StatusError
		if errors.As(err, &status) && status.StatusCode == http.StatusNotFound && status.Code == "session_not_found" {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	defer clear(response.Data)
	item, err := readSession(response.Data)
	if response.StatusCode != http.StatusOK || err != nil || item.ID != sessionID {
		return Session{}, client.ErrInvalidResponse
	}
	return item, nil
}

// The request ID is a submitted correlation identity; a pending close may already
// have its own durable identity on the controller. No mutation is retried here.
func Control(ctx context.Context, api *client.Client, sessionID, method, requestID string) (ControlResult, error) {
	switch method {
	case "verify", "close", "abandon":
	default:
		return ControlResult{}, errors.New("session control must be verify, close or abandon")
	}
	if requestID != "" && !id.Valid(requestID) {
		return ControlResult{}, errors.New("request ID must be a canonical UUID")
	}
	item, err := Get(ctx, api, sessionID)
	if err != nil {
		return ControlResult{}, err
	}
	if err := item.reference().Validate(); err != nil {
		return ControlResult{}, errors.New("session has no assigned owner; inspect it before sending a control")
	}
	if requestID == "" {
		requestID = id.New()
	}
	result := ControlResult{RequestID: requestID}
	body, err := json.Marshal(model.Control{Reference: item.reference(), Method: method, RequestID: requestID})
	if err != nil {
		return result, err
	}
	response, err := api.Do(ctx, http.MethodPost, collectionPath+"/control", body, maxSessionBytes)
	if err != nil {
		return result, &ControlError{RequestID: requestID, cause: err}
	}
	defer clear(response.Data)
	observed, err := readSession(response.Data)
	if response.StatusCode != http.StatusOK || err != nil || observed.reference() != item.reference() {
		return result, &ControlError{RequestID: requestID, cause: errors.Join(client.ErrWriteUncertain, client.ErrInvalidResponse)}
	}
	result.Session = observed
	return result, nil
}

func readSession(data []byte) (Session, error) {
	var item Session
	if json.Unmarshal(data, &item) != nil || !item.valid() {
		return Session{}, client.ErrInvalidResponse
	}
	return item, nil
}
