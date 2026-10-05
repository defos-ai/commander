// Package missions loads reusable mission definitions. Operations own execution state.
package missions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"commander/framework/domain"
	agentmodel "commander/framework/domain/agents"

	"github.com/google/jsonschema-go/jsonschema"
)

// Step is ordered planning guidance. Required actions also participate in preflight.
type Step struct {
	ID              string                 `json:"id"`
	Objective       string                 `json:"objective"`
	AgentTypes      []agentmodel.AgentType `json:"agent_types"`
	RequiredActions []string               `json:"required_actions"`
}

// Mission describes an objective independently of its operations.
// RequiredActions define preflight coverage; StartingActions guide initial task
// selection. An operation can be created with gaps, but dispatch waits for readiness.
// InputSchema and OutputSchema are JSON Schema objects; {} allows any JSON value.
// References must resolve within each schema, without fetching external resources.
type Mission struct {
	Automations     map[string]Automation `json:"automations,omitempty"`
	Name            string                `json:"name"`
	Objective       string                `json:"objective"`
	RequiredActions []string              `json:"required_actions"`
	StartingActions []string              `json:"starting_actions"`
	Steps           []Step                `json:"steps,omitempty"`
	InputSchema     json.RawMessage       `json:"input_schema"`
	OutputSchema    json.RawMessage       `json:"output_schema"`
}

// LoadMission strictly decodes and validates one JSON definition. Unknown
// definition and step fields are rejected; JSON Schema extension keywords are
// permitted according to the schema specification.
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
	if strings.TrimSpace(m.Objective) == "" {
		return errors.New("mission objective must not be empty")
	}
	required := make(map[string]bool, len(m.RequiredActions))
	for _, action := range m.RequiredActions {
		if strings.TrimSpace(action) == "" {
			return errors.New("mission actions must not be blank")
		}
		required[action] = true
	}
	for _, action := range m.StartingActions {
		if !required[action] {
			return fmt.Errorf("starting action %q is not a required action", action)
		}
	}
	seenSteps := make(map[string]bool, len(m.Steps))
	for _, step := range m.Steps {
		if strings.TrimSpace(step.ID) == "" || seenSteps[step.ID] || strings.TrimSpace(step.Objective) == "" {
			return errors.New("mission steps require unique nonblank IDs and objectives")
		}
		seenSteps[step.ID] = true
		for _, kind := range step.AgentTypes {
			if strings.TrimSpace(string(kind)) == "" {
				return errors.New("step agent types must not be blank")
			}
		}
		for _, action := range step.RequiredActions {
			if strings.TrimSpace(action) == "" {
				return errors.New("step actions must not be blank")
			}
		}
	}
	for name, rule := range m.Automations {
		if strings.TrimSpace(name) == "" {
			return errors.New("automation requires a source action")
		}
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("automation %q: %w", name, err)
		}
	}
	if _, err := resolveSchema(m.InputSchema); err != nil {
		return fmt.Errorf("input schema: %w", err)
	}
	if _, err := resolveSchema(m.OutputSchema); err != nil {
		return fmt.Errorf("output schema: %w", err)
	}
	return nil
}

func (m Mission) Clone() Mission {
	m.Automations = maps.Clone(m.Automations)
	for name, rule := range m.Automations {
		rule.Run = slices.Clone(rule.Run)
		m.Automations[name] = rule
	}
	if m.RequiredActions != nil {
		m.RequiredActions = append([]string{}, m.RequiredActions...)
	}
	if m.StartingActions != nil {
		m.StartingActions = append([]string{}, m.StartingActions...)
	}
	if m.Steps != nil {
		m.Steps = append([]Step{}, m.Steps...)
		for i := range m.Steps {
			m.Steps[i].AgentTypes = append([]agentmodel.AgentType(nil), m.Steps[i].AgentTypes...)
			m.Steps[i].RequiredActions = append([]string(nil), m.Steps[i].RequiredActions...)
		}
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
