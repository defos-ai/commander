package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/defos-ai/commander/apps/admin/client"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

const (
	collectionPath   = "/api/v1/secrets"
	maxMetadataBytes = 64 << 10
	maxListBytes     = 4 << 20
	maxRequestBytes  = 1 << 20
)

var (
	ErrConflict = errors.New("secret changed; load its current metadata before submitting again")
	ErrDisabled = errors.New("secret management is unavailable; configure API secret encryption")
)

// Get returns nil only when the API confirms that the exact key is absent.
func Get(ctx context.Context, api *client.Client, key secretmodel.Key) (*Metadata, error) {
	if key.Validate() != nil {
		return nil, errors.New("invalid secret name")
	}
	response, err := api.Do(ctx, http.MethodGet, secretPath(key), nil, maxMetadataBytes)
	if hasStatus(err, http.StatusNotFound, "secret_not_found") {
		return nil, nil
	}
	if err != nil {
		return nil, secretError(err, false)
	}
	defer clear(response.Data)
	if response.StatusCode != http.StatusOK {
		return nil, client.ErrInvalidResponse
	}
	return readMetadata(response.Data, key)
}

func List(ctx context.Context, api *client.Client) ([]Metadata, error) {
	response, err := api.Do(ctx, http.MethodGet, collectionPath, nil, maxListBytes)
	if err != nil {
		return nil, secretError(err, false)
	}
	defer clear(response.Data)
	var envelope struct {
		Items []Metadata `json:"items"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(response.Data, &envelope) != nil || envelope.Items == nil {
		return nil, client.ErrInvalidResponse
	}
	seenIDs, seenKeys := make(map[string]bool), make(map[secretmodel.Key]bool)
	for _, metadata := range envelope.Items {
		key := metadata.key()
		if !validMetadata(metadata, key) || seenIDs[metadata.ID] || seenKeys[key] {
			return nil, client.ErrInvalidResponse
		}
		seenIDs[metadata.ID], seenKeys[key] = true, true
	}
	return envelope.Items, nil
}

func Requests(ctx context.Context, api *client.Client, agentType string) ([]Request, error) {
	path := "/api/v1/secret-requests"
	if agentType != "" {
		if !id.ValidType(agentType) {
			return nil, errors.New("invalid agent type")
		}
		path += "?agent_type=" + url.QueryEscape(agentType)
	}
	response, err := api.Do(ctx, http.MethodGet, path, nil, maxListBytes)
	if err != nil {
		return nil, secretError(err, false)
	}
	defer clear(response.Data)
	var envelope struct {
		Items []Request `json:"items"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(response.Data, &envelope) != nil || envelope.Items == nil {
		return nil, client.ErrInvalidResponse
	}
	seen := make(map[string]bool)
	for _, request := range envelope.Items {
		if !validRequest(request) || agentType != "" && request.AgentType != agentType || seen[request.ID] {
			return nil, client.ErrInvalidResponse
		}
		seen[request.ID] = true
	}
	return envelope.Items, nil
}

// A completed but unconfirmed write must be inspected, never retried automatically.
func Save(ctx context.Context, api *client.Client, key secretmodel.Key, value string, expected *Metadata) (*Metadata, error) {
	if key.Validate() != nil {
		return nil, errors.New("invalid secret name")
	}
	method, path := http.MethodPost, collectionPath
	var body []byte
	var err error
	if expected == nil {
		body, err = json.Marshal(struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{key.Name, value})
	} else {
		if !validMetadata(*expected, key) || expected.Version == ^uint64(0) {
			return nil, errors.New("invalid saved secret metadata; load its current version")
		}
		method, path = http.MethodPut, secretPath(key)
		// Rotation preserves future expiry. Replacing an expired value renews it
		// without expiry, matching the existing terminal and browser forms.
		var expiresAt *time.Time
		if expected.ExpiresAt != nil && expected.ExpiresAt.After(time.Now()) {
			expiresAt = expected.ExpiresAt
		}
		body, err = json.Marshal(struct {
			ID        string     `json:"id"`
			Version   uint64     `json:"version"`
			Value     string     `json:"value"`
			ExpiresAt *time.Time `json:"expires_at,omitempty"`
		}{expected.ID, expected.Version, value, expiresAt})
	}
	defer clear(body)
	if err != nil || len(body) > maxRequestBytes {
		return nil, errors.New("secret value is too large")
	}
	response, err := api.Do(ctx, method, path, body, maxMetadataBytes)
	if err != nil {
		return nil, secretError(err, true)
	}
	defer clear(response.Data)
	expectedStatus := http.StatusCreated
	if expected != nil {
		expectedStatus = http.StatusOK
	}
	metadata, err := readMetadata(response.Data, key)
	if response.StatusCode != expectedStatus || err != nil ||
		(expected == nil && metadata.Version != 1) ||
		(expected != nil && (metadata.ID != expected.ID || metadata.Version != expected.Version+1)) {
		return nil, client.ErrWriteUncertain
	}
	return metadata, nil
}

func Delete(ctx context.Context, api *client.Client, key secretmodel.Key, expected Metadata) error {
	if !validMetadata(expected, key) {
		return errors.New("invalid saved secret metadata; load its current version")
	}
	query := url.Values{"id": {expected.ID}, "version": {strconv.FormatUint(expected.Version, 10)}}
	response, err := api.Do(ctx, http.MethodDelete, secretPath(key)+"?"+query.Encode(), nil, maxMetadataBytes)
	if err != nil {
		return secretError(err, true)
	}
	defer clear(response.Data)
	if response.StatusCode != http.StatusNoContent || len(response.Data) != 0 {
		return client.ErrWriteUncertain
	}
	return nil
}

func secretPath(key secretmodel.Key) string {
	return collectionPath + "/" + url.PathEscape(key.Name)
}

func readMetadata(data []byte, key secretmodel.Key) (*Metadata, error) {
	var metadata Metadata
	if json.Unmarshal(data, &metadata) != nil || !validMetadata(metadata, key) {
		return nil, client.ErrInvalidResponse
	}
	return &metadata, nil
}

func secretError(err error, mutation bool) error {
	var response *client.StatusError
	if !errors.As(err, &response) {
		return err
	}
	if response.StatusCode == http.StatusServiceUnavailable && response.Code == "secrets_unavailable" {
		return ErrDisabled
	}
	if errors.Is(err, client.ErrWriteUncertain) || errors.Is(err, client.ErrAuthentication) {
		return err
	}
	if response.StatusCode == http.StatusConflict || mutation && response.StatusCode == http.StatusNotFound {
		return ErrConflict
	}
	if mutation && response.StatusCode != http.StatusBadRequest && response.StatusCode != http.StatusForbidden &&
		response.StatusCode != http.StatusRequestEntityTooLarge && response.StatusCode != http.StatusUnsupportedMediaType &&
		(response.StatusCode < 300 || response.StatusCode >= 400) {
		return errors.Join(client.ErrWriteUncertain, err)
	}
	return err
}

func hasStatus(err error, status int, code string) bool {
	var response *client.StatusError
	return errors.As(err, &response) && response.StatusCode == status && response.Code == code
}
