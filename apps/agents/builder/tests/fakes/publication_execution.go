package fakes

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
)

// Each fake has one active action invocation. Tests join canceled runs before
// observing or reconfiguring their HTTP and checkpoint state.
type Checkpoints struct {
	record    executions.Execution
	afterSave func(string) error
}

func (p *Checkpoints) LoadCheckpoint(ctx context.Context, name string) (json.RawMessage, bool, error) {
	data, found := p.record.Checkpoints[name]
	return bytes.Clone(data), found, ctx.Err()
}

func (p *Checkpoints) SaveCheckpoint(ctx context.Context, name string, data json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := p.record.RecordCheckpoint(name, data)
	if err == nil && p.afterSave != nil {
		return p.afterSave(name)
	}
	return err
}

type PublicationSecrets string

func (s PublicationSecrets) Secret(name string) ([]byte, error) {
	if name != "GITHUB_TOKEN" {
		return nil, action.ErrSecretUnavailable
	}
	return []byte(s), nil
}

func NewCheckpoints(command messages.Message) *Checkpoints {
	command.Payload = bytes.Clone(command.Payload)
	return &Checkpoints{record: executions.Execution{Command: command, StartedAt: command.CreatedAt}}
}

func (p *Checkpoints) SetAfterSave(hook func(string) error) { p.afterSave = hook }

func (p *Checkpoints) Snapshot() map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(p.record.Checkpoints))
	for name, data := range p.record.Checkpoints {
		result[name] = bytes.Clone(data)
	}
	return result
}
