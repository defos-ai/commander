package missions

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/defos-ai/commander/apps/admin/client"
)

const collectionPath = "/api/v1/missions"

func List(ctx context.Context, api *client.Client) ([]Definition, error) {
	data, err := api.Request(ctx, http.MethodGet, collectionPath, nil)
	if err != nil {
		return nil, err
	}
	raw, err := readEnvelope(data)
	if err != nil {
		return nil, client.ErrInvalidResponse
	}
	definitions, err := loadDefinitions(raw, MaxFileBytes)
	if err != nil {
		return nil, client.ErrInvalidResponse
	}
	return definitions, nil
}

func Export(ctx context.Context, api *client.Client, name string) (Definition, error) {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." {
		return Definition{}, errors.New("mission name must be nonblank and not a dot segment")
	}
	data, err := api.Request(ctx, http.MethodGet, collectionPath+"/"+url.PathEscape(name), nil)
	if err != nil {
		return Definition{}, err
	}
	return responseDefinition(data, name)
}

func responseDefinition(data []byte, name string) (Definition, error) {
	definition, err := loadDefinition(data)
	if err != nil || definition.Name != name {
		return Definition{}, client.ErrInvalidResponse
	}
	return definition, nil
}

func hasStatus(err error, status int, code string) bool {
	var response *client.StatusError
	return errors.As(err, &response) && response.StatusCode == status && response.Code == code
}
