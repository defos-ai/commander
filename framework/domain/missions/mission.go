package missions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/defos-ai/commander/framework/domain"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"

	"github.com/google/jsonschema-go/jsonschema"
)

// Mission describes ordered objectives independently of its operations.
// RequiredActions define preflight coverage. OptionalActions describe capabilities
// checked when selected for queued work; neither list is an execution allowlist.
// ActionDefinitions optionally describe desired contracts, without providing readiness.
// StartingActions guide initial task selection. Dispatch waits for readiness.
// InputSchema and OutputSchema are JSON Schema objects; {} allows any JSON value.
// References must resolve within each schema, without fetching external resources.
type Mission struct {
	Rules             RuleSet                  `json:"rules,omitempty"`
	Completion        *CompletionPolicy        `json:"completion,omitempty"`
	Name              string                   `json:"name"`
	Objectives        []string                 `json:"objectives"`
	RequiredActions   []string                 `json:"required_actions"`
	OptionalActions   []string                 `json:"optional_actions,omitempty"`
	ActionDefinitions []actionmodel.Definition `json:"action_definitions,omitempty"`
	StartingActions   []string                 `json:"starting_actions"`
	InputSchema       json.RawMessage          `json:"input_schema"`
	OutputSchema      json.RawMessage          `json:"output_schema"`
}

func LoadMission(data []byte) (Mission, error) {
	var definition Mission
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return definition, errors.New("mission definition must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil {
		return Mission{}, fmt.Errorf("decode mission definition: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Mission{}, errors.New("expected a single mission definition")
	}
	if err := definition.Validate(); err != nil {
		return Mission{}, err
	}
	return definition, nil
}

func (m Mission) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("mission name must not be empty")
	}
	if m.Name == "." || m.Name == ".." {
		return errors.New("mission name must not be a dot segment")
	}
	if len(m.Objectives) == 0 {
		return errors.New("mission objectives must contain at least one objective")
	}
	for _, objective := range m.Objectives {
		if strings.TrimSpace(objective) == "" {
			return errors.New("mission objectives must not contain blank entries")
		}
	}
	if m.Completion != nil {
		if err := m.Completion.Validate(); err != nil {
			return err
		}
	}
	for _, list := range []struct {
		name    string
		actions []string
	}{{"required_actions", m.RequiredActions}, {"optional_actions", m.OptionalActions}, {"starting_actions", m.StartingActions}} {
		if err := validateActionNames(list.name, list.actions); err != nil {
			return err
		}
	}
	for _, action := range m.OptionalActions {
		if slices.Contains(m.RequiredActions, action) {
			return fmt.Errorf("action %q must not be both required and optional", action)
		}
	}
	for _, action := range m.StartingActions {
		if !slices.Contains(m.RequiredActions, action) {
			return fmt.Errorf("starting action %q is not a required action", action)
		}
	}
	if err := m.validateActionDefinitions(); err != nil {
		return err
	}
	if err := m.Rules.Validate(); err != nil {
		return err
	}
	if _, err := resolveSchema(m.InputSchema); err != nil {
		return fmt.Errorf("input schema: %w", err)
	}
	if _, err := resolveSchema(m.OutputSchema); err != nil {
		return fmt.Errorf("output schema: %w", err)
	}
	return nil
}

// Definitions are planning/build contracts, never registered executors or proof
// of readiness. Require a reference so unused definitions do not drift in a mission.
func (m Mission) validateActionDefinitions() error {
	if len(m.ActionDefinitions) > 100 {
		return errors.New("action_definitions must contain at most 100 definitions")
	}
	names := make([]string, len(m.ActionDefinitions))
	for i, definition := range m.ActionDefinitions {
		names[i] = definition.Name
	}
	if err := validateActionNames("action_definitions", names); err != nil {
		return err
	}
	for i, definition := range m.ActionDefinitions {
		referenced := slices.Contains(m.RequiredActions, definition.Name) || slices.Contains(m.OptionalActions, definition.Name)
		if !referenced {
			return fmt.Errorf("action_definitions[%d] must reference a required or optional action", i)
		}
		if err := definition.Validate(); err != nil {
			return fmt.Errorf("action_definitions[%d]: %w", i, err)
		}
		if _, err := actionmodel.ResolveSchema(definition.InputSchema); err != nil {
			return fmt.Errorf("action_definitions[%d] input_schema: %w", i, err)
		}
		if _, err := actionmodel.ResolveSchema(definition.OutputSchema); err != nil {
			return fmt.Errorf("action_definitions[%d] output_schema: %w", i, err)
		}
	}
	return nil
}

func validateActionNames(field string, names []string) error {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name {
			return fmt.Errorf("%s must contain nonblank names without surrounding whitespace", field)
		}
		if seen[name] {
			return fmt.Errorf("%s contains duplicate action %q", field, name)
		}
		seen[name] = true
	}
	return nil
}

func (m Mission) Clone() Mission {
	if m.Completion != nil {
		completion := *m.Completion
		m.Completion = &completion
	}
	m.Objectives = slices.Clone(m.Objectives)
	m.ActionDefinitions = slices.Clone(m.ActionDefinitions)
	for i := range m.ActionDefinitions {
		m.ActionDefinitions[i] = m.ActionDefinitions[i].Clone()
	}
	m.Rules = m.Rules.Clone()
	if m.RequiredActions != nil {
		m.RequiredActions = append([]string{}, m.RequiredActions...)
	}
	if m.OptionalActions != nil {
		m.OptionalActions = append([]string{}, m.OptionalActions...)
	}
	if m.StartingActions != nil {
		m.StartingActions = append([]string{}, m.StartingActions...)
	}
	m.InputSchema = append(json.RawMessage(nil), m.InputSchema...)
	m.OutputSchema = append(json.RawMessage(nil), m.OutputSchema...)
	return m
}

func (m Mission) ValidateInput(input json.RawMessage) error {
	if err := validateValue(m.InputSchema, input); err != nil {
		return fmt.Errorf("invalid mission input: %w", err)
	}
	return nil
}

// ValidateOutput checks the input and report against their declared schemas.
// Evidence interpretation and whether a report answers the objective remain
// decisions for the runner and its decision maker.
func (m Mission) ValidateOutput(input, output json.RawMessage) error {
	if err := m.ValidateInput(input); err != nil {
		return err
	}
	resolved, err := resolveSchema(m.OutputSchema)
	if err != nil {
		return fmt.Errorf("output schema: %w", err)
	}
	if err := validateResolvedValue(resolved, output); err != nil {
		return fmt.Errorf("invalid mission output: %w: %w", domain.ErrInvalidOutput, err)
	}
	return nil
}

func resolveSchema(data json.RawMessage) (*jsonschema.Resolved, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("expected a JSON schema object")
	}
	var document map[string]any
	if err := json.Unmarshal(trimmed, &document); err != nil {
		return nil, err
	}
	if err := checkSchemaTypes(document); err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(trimmed, &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(nil)
}

func validateValue(schema, data json.RawMessage) error {
	resolved, err := resolveSchema(schema)
	if err != nil {
		return err
	}
	return validateResolvedValue(resolved, data)
}

func validateResolvedValue(resolved *jsonschema.Resolved, data json.RawMessage) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected a single JSON value")
	}
	return resolved.Validate(value)
}

// The schema library resolves references and patterns, but does not reject
// unknown type names when loading. Check schema nodes, never example values.
func checkSchemaTypes(schema map[string]any) error {
	if value, exists := schema["type"]; exists {
		values, multiple := value.([]any)
		if !multiple {
			values = []any{value}
		}
		if len(values) == 0 {
			return errors.New("schema type list must not be empty")
		}
		seen := map[string]bool{}
		for _, value := range values {
			name, ok := value.(string)
			if !ok || seen[name] {
				return errors.New("schema types must be unique names")
			}
			switch name {
			case "object", "array", "string", "number", "integer", "boolean", "null":
			default:
				return fmt.Errorf("invalid schema type %q", name)
			}
			seen[name] = true
		}
	}
	check := func(value any) error {
		if child, ok := value.(map[string]any); ok {
			return checkSchemaTypes(child)
		}
		return nil
	}
	for _, keyword := range []string{"$defs", "definitions", "properties", "patternProperties", "dependentSchemas"} {
		if children, ok := schema[keyword].(map[string]any); ok {
			for _, child := range children {
				if err := check(child); err != nil {
					return err
				}
			}
		}
	}
	for _, keyword := range []string{"items", "additionalItems", "additionalProperties", "contains", "propertyNames", "not", "if", "then", "else", "unevaluatedItems", "unevaluatedProperties", "allOf", "anyOf", "oneOf", "prefixItems"} {
		if children, ok := schema[keyword].([]any); ok {
			for _, child := range children {
				if err := check(child); err != nil {
					return err
				}
			}
		} else if err := check(schema[keyword]); err != nil {
			return err
		}
	}
	return nil
}
