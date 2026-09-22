package action

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"commander/framework/agent/environment"
	actionmodel "commander/framework/domain/actions"

	"github.com/google/jsonschema-go/jsonschema"
)

// FuncConfig describes an action implemented by a typed Go function. I and O must
// be structs or string-keyed maps. Omit schemas to infer them from their types.
// Actions that return attachments can implement Executor directly using Base.
type FuncConfig[I, O any] struct {
	Action actionmodel.Action
	// NewInput supplies fresh defaults for each call. Fields with defaults should
	// have omitempty or omitzero JSON tags so the inferred schema makes them optional.
	// JSON Schema's default keyword is descriptive and is not applied automatically.
	NewInput func() I
	// Discover is optional; compiled-in implementations exist by default.
	// A successful callback must return Ready or Unavailable.
	Discover    func(context.Context, environment.Environment, *environment.DiscoveryScope) (Status, error)
	Run         func(context.Context, I) (O, error)
	NextActions func(context.Context, I, O) ([]NextAction, error)
}

// Func adapts a typed function to Executor. It checks discovery, permissions, schemas
// and, when implemented by I or *I, Validate() error before invoking the handler.
// Each agent owns its Func instances; callbacks must honor context cancellation.
type Func[I, O any] struct {
	*Base
	newInput     func() I
	discover     func(context.Context, environment.Environment, *environment.DiscoveryScope) (Status, error)
	run          func(context.Context, I) (O, error)
	nextActions  func(context.Context, I, O) ([]NextAction, error)
	inputSchema  *jsonschema.Resolved
	outputSchema *jsonschema.Resolved
}

// NewFunc validates metadata and prepares schemas without running callbacks,
// discovering dependencies or granting permissions.
func NewFunc[I, O any](config FuncConfig[I, O]) (*Func[I, O], error) {
	if strings.TrimSpace(config.Action.Name) == "" {
		return nil, errors.New("action name must not be blank")
	}
	if config.Run == nil {
		return nil, errors.New("action run handler is required")
	}
	definition := config.Action
	input, rawInput, err := funcSchema[I](definition.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("action input schema: %w", err)
	}
	output, rawOutput, err := funcSchema[O](definition.OutputSchema)
	if err != nil {
		return nil, fmt.Errorf("action output schema: %w", err)
	}
	definition.InputSchema, definition.OutputSchema = rawInput, rawOutput
	return &Func[I, O]{
		Base: NewBase(definition), newInput: config.NewInput,
		discover: config.Discover, run: config.Run, nextActions: config.NextActions,
		inputSchema: input, outputSchema: output,
	}, nil
}

func (f *Func[I, O]) Discover(ctx context.Context, env environment.Environment, scope *environment.DiscoveryScope) (Status, error) {
	if err := ctx.Err(); err != nil {
		f.SetStatus(Unknown)
		return Unknown, err
	}
	if f.discover == nil {
		return f.Base.Discover(ctx, env, scope)
	}
	status, err := f.discover(ctx, env, scope)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && status != Ready && status != Unavailable {
		err = fmt.Errorf("invalid action discovery status %q: expected ready or unavailable", status)
	}
	if err != nil {
		status = Unknown
	}
	f.SetStatus(status)
	return status, err
}

func (f *Func[I, O]) Run(ctx context.Context, raw json.RawMessage, grants []string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := f.Check(grants); err != nil {
		return Result{}, err
	}
	if err := validateObject(f.inputSchema, raw); err != nil {
		return Result{}, fmt.Errorf("invalid action input: %w", err)
	}
	var input I
	if f.newInput != nil {
		input = f.newInput()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return Result{}, fmt.Errorf("decode action input: %w", err)
	}
	if validator, ok := any(&input).(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			return Result{}, fmt.Errorf("invalid action input: %w", err)
		}
	} else if validator, ok := any(input).(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			return Result{}, fmt.Errorf("invalid action input: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	output, err := f.run(ctx, input)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	result, err := JSONResult(output)
	if err != nil {
		return Result{}, fmt.Errorf("encode action output: %w", err)
	}
	if err := validateObject(f.outputSchema, result.Output); err != nil {
		return Result{}, fmt.Errorf("invalid action output: %w", err)
	}
	if f.nextActions != nil {
		actions, err := f.nextActions(ctx, input, output)
		if err != nil {
			return Result{}, fmt.Errorf("propose next actions: %w", err)
		}
		if err := actionmodel.ValidateNextActions(actions); err != nil {
			return Result{}, err
		}
		result.NextActions = actionmodel.CloneNextActions(actions)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return result, nil
}

func funcSchema[T any](raw json.RawMessage) (*jsonschema.Resolved, json.RawMessage, error) {
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct && (t.Kind() != reflect.Map || t.Key().Kind() != reflect.String) {
		return nil, nil, fmt.Errorf("type %v must be a struct or string-keyed map", t)
	}
	if len(raw) == 0 {
		var err error
		raw, err = SchemaFor[T]()
		if err != nil {
			return nil, nil, err
		}
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, nil, err
	}
	if document["type"] != "object" {
		return nil, nil, errors.New("schema must declare type object")
	}
	if err := checkSchemaTypes(document); err != nil {
		return nil, nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, nil, err
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	return resolved, raw, err
}

func validateObject(schema *jsonschema.Resolved, raw json.RawMessage) error {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	if object == nil {
		return errors.New("expected a JSON object")
	}
	return schema.Validate(object)
}
