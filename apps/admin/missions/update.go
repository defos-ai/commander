package missions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/defos-ai/commander/apps/admin/client"
)

func Update(ctx context.Context, api *client.Client, definition Definition) (Definition, error) {
	validated, err := loadDefinitions([]json.RawMessage{definition.JSON}, MaxDefinitionBytes)
	if err != nil {
		return Definition{}, err
	}
	if definition.Name != validated[0].Name {
		return Definition{}, errors.New("mission name does not match its definition")
	}
	response, err := api.Do(ctx, http.MethodPut, collectionPath+"/"+url.PathEscape(definition.Name), validated[0].JSON, MaxFileBytes)
	if err != nil {
		return Definition{}, err
	}
	if response.StatusCode != http.StatusOK {
		return Definition{}, errors.Join(client.ErrWriteUncertain, client.ErrInvalidResponse)
	}
	saved, err := responseDefinition(response.Data, definition.Name)
	if err != nil {
		return Definition{}, errors.Join(client.ErrWriteUncertain, err)
	}
	return saved, nil
}
