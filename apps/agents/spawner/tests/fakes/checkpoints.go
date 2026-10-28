package fakes

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
)

// The runtime failure tests use the real immutable execution transitions while
// controlling acknowledgement failures. Public API/MCP tests cover bus delivery.
type Checkpoints struct {
	record     executions.Execution
	beforeSave func(context.Context, string, json.RawMessage) error
	afterSave  func(context.Context, string, json.RawMessage) error
}

func (s *Checkpoints) LoadCheckpoint(ctx context.Context, name string) (json.RawMessage, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	data, found := s.record.Checkpoints[name]
	return bytes.Clone(data), found, nil
}

func (s *Checkpoints) SaveCheckpoint(ctx context.Context, name string, data json.RawMessage) error {
	before := s.beforeSave
	if before != nil {
		if err := before(ctx, name, data); err != nil {
			return err
		}
	}
	_, err := s.record.RecordCheckpoint(name, data)
	after := s.afterSave
	if err != nil {
		return err
	}
	if after != nil {
		return after(ctx, name, data)
	}
	return ctx.Err()
}

func NewCheckpoints(command messages.Message) *Checkpoints {
	command.Payload = bytes.Clone(command.Payload)
	return &Checkpoints{record: executions.Execution{Command: command, StartedAt: command.CreatedAt}}
}
func (s *Checkpoints) SetBeforeSave(hook func(context.Context, string, json.RawMessage) error) {
	s.beforeSave = hook
}
func (s *Checkpoints) SetAfterSave(hook func(context.Context, string, json.RawMessage) error) {
	s.afterSave = hook
}
func (s *Checkpoints) Checkpoints() map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(s.record.Checkpoints))
	for name, data := range s.record.Checkpoints {
		result[name] = bytes.Clone(data)
	}
	return result
}
func (s *Checkpoints) ReplaceCheckpoint(name string, data json.RawMessage) {
	if s.record.Checkpoints == nil {
		s.record.Checkpoints = make(map[string]json.RawMessage)
	}
	s.record.Checkpoints[name] = bytes.Clone(data)
}
func (s *Checkpoints) ReplaceCheckpoints(data map[string]json.RawMessage) {
	s.record.Checkpoints = make(map[string]json.RawMessage, len(data))
	for name, value := range data {
		s.record.Checkpoints[name] = bytes.Clone(value)
	}
}
func (s *Checkpoints) KeepOnlyCheckpoints(names ...string) {
	for name := range s.record.Checkpoints {
		keep := false
		for _, retained := range names {
			if name == retained {
				keep = true
			}
		}
		if !keep {
			delete(s.record.Checkpoints, name)
		}
	}
}
