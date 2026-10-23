// Package missions validates and administers mission definitions through the API.
package missions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	domainmissions "github.com/defos-ai/commander/framework/domain/missions"
)

const (
	MaxFileBytes       = 4 << 20
	MaxDefinitionBytes = 1 << 20
)

// Definition retains JSON Schema extensions and numeric literals as supplied.
type Definition struct {
	Name string
	JSON json.RawMessage
}

func (d Definition) MarshalJSON() ([]byte, error) { return d.JSON.MarshalJSON() }

// Parse accepts one definition, a nonempty array, or the API's items envelope.
// Every definition is validated before callers can begin an import.
func Parse(data []byte) ([]Definition, error) {
	if len(data) > MaxFileBytes {
		return nil, errors.New("mission file exceeds the 4 MiB limit")
	}
	raw, err := splitDefinitions(data)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, errors.New("mission file must contain at least one definition")
	}
	return loadDefinitions(raw, MaxDefinitionBytes)
}

func splitDefinitions(data []byte) ([]json.RawMessage, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || !json.Valid(data) {
		return nil, errors.New("mission file must contain valid JSON")
	}
	switch data[0] {
	case '[':
		var definitions []json.RawMessage
		if json.Unmarshal(data, &definitions) != nil {
			return nil, errors.New("mission file must contain an array of definitions")
		}
		return definitions, nil
	case '{':
		var envelope struct {
			Items json.RawMessage `json:"items"`
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.Items != nil {
			return readEnvelope(data)
		}
		return []json.RawMessage{data}, nil
	default:
		return nil, errors.New("mission file must contain a definition, an array or an items envelope")
	}
}

func readEnvelope(data []byte) ([]json.RawMessage, error) {
	var envelope struct {
		Items []json.RawMessage `json:"items"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || envelope.Items == nil {
		return nil, errors.New("mission list must contain an items array and no other fields")
	}
	return envelope.Items, nil
}

func loadDefinitions(raw []json.RawMessage, maximumDefinitionBytes int) ([]Definition, error) {
	definitions := make([]Definition, len(raw))
	names := make(map[string]bool, len(raw))
	for i, data := range raw {
		if len(bytes.TrimSpace(data)) > maximumDefinitionBytes {
			return nil, fmt.Errorf("mission %d: definition exceeds the %d MiB limit", i+1, maximumDefinitionBytes>>20)
		}
		definition, err := loadDefinition(data)
		if err != nil {
			return nil, fmt.Errorf("mission %d: %w", i+1, err)
		}
		if names[definition.Name] {
			return nil, fmt.Errorf("mission %d repeats an earlier mission name", i+1)
		}
		names[definition.Name] = true
		definitions[i] = definition
	}
	return definitions, nil
}

func loadDefinition(data []byte) (Definition, error) {
	data = bytes.TrimSpace(data)
	mission, err := domainmissions.LoadMission(data)
	if err != nil {
		return Definition{}, err
	}
	return Definition{Name: mission.Name, JSON: bytes.Clone(data)}, nil
}
