package packages_test

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
)

// The runtime failure tests use the real immutable execution transitions while
// controlling acknowledgement failures. Public API/MCP tests cover bus delivery.
type spawnCheckpoints struct {
	mu         sync.Mutex
	record     executions.Execution
	beforeSave func(context.Context, string, json.RawMessage) error
	afterSave  func(context.Context, string, json.RawMessage) error
}

func newSpawnCheckpoints(t *testing.T) (context.Context, string, *spawnCheckpoints) {
	t.Helper()
	command, err := messages.NewMessage(messages.RunAction, "spawner-agent-12345678",
		actions.ActionCall{ActionName: "spawn-agent", Input: json.RawMessage(`{"package":"github.com/defos-ai/fixture-agent@stable"}`)})
	if err != nil {
		t.Fatal(err)
	}
	journal := &spawnCheckpoints{record: executions.Execution{Command: command, StartedAt: command.CreatedAt}}
	ctx := action.WithExecution(t.Context(), action.Execution{CommandID: command.ID})
	return action.WithCheckpoints(ctx, journal), "command:" + command.ID, journal
}

func (s *spawnCheckpoints) LoadCheckpoint(ctx context.Context, name string) (json.RawMessage, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, found := s.record.Checkpoints[name]
	return bytes.Clone(data), found, nil
}

func (s *spawnCheckpoints) SaveCheckpoint(ctx context.Context, name string, data json.RawMessage) error {
	if s.beforeSave != nil {
		if err := s.beforeSave(ctx, name, data); err != nil {
			return err
		}
	}
	s.mu.Lock()
	_, err := s.record.RecordCheckpoint(name, data)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if s.afterSave != nil {
		return s.afterSave(ctx, name, data)
	}
	return ctx.Err()
}
