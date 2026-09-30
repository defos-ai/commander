package action

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync/atomic"

	"commander/framework/agent/environment"
	actionmodel "commander/framework/domain/actions"
)

type Status = actionmodel.Status
type NextAction = actionmodel.NextAction

const (
	Unknown     = actionmodel.Unknown
	Ready       = actionmodel.Ready
	Unavailable = actionmodel.Unavailable
)

type Result struct {
	Output      json.RawMessage `json:"output"`
	Artifacts   []Artifact      `json:"-"`
	NextActions []NextAction    `json:"next_actions,omitempty"`
}

// Each instance belongs to one agent. Run never chooses another action or contacts a controller.
type Executor interface {
	Action() actionmodel.Action
	Discover(context.Context, environment.Environment, *environment.DiscoveryScope) (Status, error)
	Run(context.Context, json.RawMessage, []string) (Result, error)
}

// Base publishes immutable readiness snapshots and checks permissions for extensions.
// Embed *Base, implement Run, and override Discover for external dependencies.
type Base struct {
	definition actionmodel.Action
	readiness  atomic.Pointer[readiness]
}

type readiness struct {
	status       Status
	reason       string
	requirements []actionmodel.Requirement
}

func NewBase(definition actionmodel.Action) *Base {
	definition.Status = Unknown
	definition.StatusReason = ""
	definition.MissingRequirements = nil
	definition.RequiredPermissions = slices.Clone(definition.RequiredPermissions)
	definition.InputSchema = bytes.Clone(definition.InputSchema)
	definition.OutputSchema = bytes.Clone(definition.OutputSchema)
	return &Base{definition: definition}
}

// Action returns an independent snapshot of the action's metadata and readiness.
func (b *Base) Action() actionmodel.Action {
	definition := b.definition
	if current := b.readiness.Load(); current != nil {
		definition.Status = current.status
		definition.StatusReason = current.reason
		definition.MissingRequirements = current.requirements
	}
	definition.RequiredPermissions = slices.Clone(definition.RequiredPermissions)
	definition.MissingRequirements = slices.Clone(definition.MissingRequirements)
	definition.InputSchema = bytes.Clone(definition.InputSchema)
	definition.OutputSchema = bytes.Clone(definition.OutputSchema)
	return definition
}

func (b *Base) SetStatus(status Status) {
	b.readiness.Store(&readiness{status: status})
}

// SetUnavailable records a public reason. It must never contain credentials.
func (b *Base) SetUnavailable(reason string, requirements ...actionmodel.Requirement) {
	b.readiness.Store(&readiness{status: Unavailable, reason: reason, requirements: slices.Clone(requirements)})
}

// Discover marks compiled-in implementations available. Scope paths constrain only
// external executables; they do not hide networking code compiled into the agent.
func (b *Base) Discover(ctx context.Context, _ environment.Environment, _ *environment.DiscoveryScope) (Status, error) {
	if err := ctx.Err(); err != nil {
		b.SetStatus(Unknown)
		return Unknown, err
	}
	b.SetStatus(Ready)
	return Ready, nil
}

func (b *Base) Check(grantedPermissions []string) error {
	definition := b.Action()
	for _, permission := range definition.RequiredPermissions {
		if !slices.Contains(grantedPermissions, permission) {
			return fmt.Errorf("missing permission: %s", permission)
		}
	}
	if definition.Status != Ready {
		return fmt.Errorf("action %q is not ready; run discovery first", definition.Name)
	}
	return nil
}

// DecodeInput rejects unknown fields, null required values, and non-object input.
// Initialize the typed destination with defaults before calling this function.
func DecodeInput(input json.RawMessage, destination any, nullableFields ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return fmt.Errorf("invalid input: %w", err)
	}
	if fields == nil {
		return errors.New("input must be a JSON object")
	}
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) && !slices.Contains(nullableFields, name) {
			return fmt.Errorf("input field %q must not be null", name)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid input: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("input must contain exactly one JSON object")
	}
	return nil
}

func JSONResult(output any) (Result, error) {
	encoded, err := json.Marshal(output)
	if err != nil {
		return Result{}, err
	}
	if len(encoded) == 0 || encoded[0] != '{' {
		return Result{}, errors.New("action output must be a JSON object")
	}
	return Result{Output: encoded}, nil
}
