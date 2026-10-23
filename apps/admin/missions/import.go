package missions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/defos-ai/commander/apps/admin/client"
)

const (
	Created      = "created"
	Skipped      = "skipped"
	WouldCreate  = "would_create"
	Failed       = "failed"
	Uncertain    = "uncertain"
	NotAttempted = "not_attempted"
)

type ImportItem struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type ImportCounts struct {
	Created      int `json:"created"`
	Skipped      int `json:"skipped"`
	WouldCreate  int `json:"would_create"`
	Failed       int `json:"failed"`
	Uncertain    int `json:"uncertain"`
	NotAttempted int `json:"not_attempted"`
}

type ImportResult struct {
	Items  []ImportItem `json:"items"`
	Counts ImportCounts `json:"counts"`
}

// Import creates missing missions and preserves confirmed existing definitions.
// It stops at the first failure; retrying a file skips earlier completed writes.
// An uncertain write must be checked before submitting that definition again.
func Import(ctx context.Context, api *client.Client, definitions []Definition, dryRun bool) (ImportResult, error) {
	validated, err := validateImport(definitions)
	if err != nil {
		return ImportResult{}, err
	}
	result := ImportResult{Items: make([]ImportItem, len(validated))}
	for i, definition := range validated {
		result.Items[i] = ImportItem{Name: definition.Name, Status: NotAttempted}
	}
	for i, definition := range validated {
		status, err := importDefinition(ctx, api, definition, dryRun)
		result.Items[i].Status = status
		if err != nil {
			result.count()
			return result, fmt.Errorf("mission %d import stopped: %w", i+1, err)
		}
	}
	result.count()
	return result, nil
}

func validateImport(definitions []Definition) ([]Definition, error) {
	if len(definitions) == 0 {
		return nil, errors.New("import requires at least one mission")
	}
	raw := make([]json.RawMessage, len(definitions))
	total := 0
	for i, definition := range definitions {
		raw[i] = definition.JSON
		total += len(definition.JSON)
		if total > MaxFileBytes {
			return nil, errors.New("mission import exceeds the 4 MiB limit")
		}
	}
	validated, err := loadDefinitions(raw, MaxDefinitionBytes)
	if err != nil {
		return nil, err
	}
	for i, definition := range definitions {
		if definition.Name != validated[i].Name {
			return nil, fmt.Errorf("mission %d name does not match its definition", i+1)
		}
	}
	return validated, nil
}

func importDefinition(ctx context.Context, api *client.Client, definition Definition, dryRun bool) (string, error) {
	_, err := Export(ctx, api, definition.Name)
	if err == nil {
		return Skipped, nil
	}
	if !hasStatus(err, http.StatusNotFound, "mission_not_found") {
		return Failed, err
	}
	if dryRun {
		return WouldCreate, nil
	}
	data, err := api.Request(ctx, http.MethodPost, collectionPath, definition.JSON)
	if hasStatus(err, http.StatusConflict, "mission_exists") {
		return Skipped, nil
	}
	if err != nil {
		if errors.Is(err, client.ErrWriteUncertain) {
			return Uncertain, err
		}
		return Failed, err
	}
	if _, err := responseDefinition(data, definition.Name); err != nil {
		return Uncertain, errors.Join(client.ErrWriteUncertain, err)
	}
	return Created, nil
}

func (result *ImportResult) count() {
	for _, item := range result.Items {
		switch item.Status {
		case Created:
			result.Counts.Created++
		case Skipped:
			result.Counts.Skipped++
		case WouldCreate:
			result.Counts.WouldCreate++
		case Failed:
			result.Counts.Failed++
		case Uncertain:
			result.Counts.Uncertain++
		case NotAttempted:
			result.Counts.NotAttempted++
		}
	}
}
