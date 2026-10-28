package executions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/fs/internal/jsonfile"
)

type ExecutionFsStore struct {
	root string
}

var _ executions.ExecutionStore = (*ExecutionFsStore)(nil)

func NewExecutionFsStore(root string) (*ExecutionFsStore, error) {
	if root == "" {
		root = filepath.Join("state", "database")
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("execution database root must not be blank")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &ExecutionFsStore{root: absolute}, nil
}

func (s *ExecutionFsStore) Claim(ctx context.Context, command messages.Message) (executions.Execution, bool, error) {
	path, err := s.path(ctx, command.AgentID, command.ID)
	if err != nil {
		return executions.Execution{}, false, err
	}
	candidate := executions.Execution{Command: command, StartedAt: time.Now().UTC()}
	if err := candidate.Validate(); err != nil {
		return executions.Execution{}, false, err
	}
	encoded, err := json.Marshal([]executions.Execution{candidate})
	if err != nil {
		return executions.Execution{}, false, err
	}
	collection, err := jsonfile.NewContext(ctx, path)
	if err != nil {
		return executions.Execution{}, false, err
	}
	var record executions.Execution
	claimed := false
	err = collection.Update(ctx, func(data []byte) ([]byte, error) {
		current, err := decodeExecution(data, command.AgentID, command.ID)
		if errors.Is(err, executions.ErrUnknownExecution) {
			// Decode the encoded snapshot to detach caller-owned payload bytes.
			record, err = decodeExecution(encoded, command.AgentID, command.ID)
			claimed = err == nil
			return encoded, err
		}
		if err != nil {
			return nil, err
		}
		if !sameExecutionMessage(current.Command, command) {
			return nil, executions.ErrExecutionConflict
		}
		record = current
		return json.Marshal([]executions.Execution{record})
	})
	if err != nil {
		return executions.Execution{}, false, err
	}
	return record, claimed, nil
}

func (s *ExecutionFsStore) Complete(ctx context.Context, result messages.Message) error {
	path, err := s.path(ctx, result.AgentID, result.CorrelationID)
	if err != nil {
		return err
	}
	if err := result.Validate(); err != nil {
		return err
	}
	if result.Type != messages.ActionSucceeded && result.Type != messages.CommandFailed {
		return errors.New("execution result must indicate success or failure")
	}
	// Missing claims never create state, even when completion is attempted first.
	if _, err := s.Get(ctx, result.AgentID, result.CorrelationID); err != nil {
		return err
	}
	collection, err := jsonfile.NewContext(ctx, path)
	if err != nil {
		return err
	}
	return collection.Update(ctx, func(data []byte) ([]byte, error) {
		record, err := decodeExecution(data, result.AgentID, result.CorrelationID)
		if err != nil {
			return nil, err
		}
		if record.Result != nil {
			if !sameExecutionMessage(*record.Result, result) {
				return nil, executions.ErrExecutionConflict
			}
			return json.Marshal([]executions.Execution{record})
		}
		completed := time.Now().UTC()
		record.Result, record.CompletedAt = &result, &completed
		if err := record.Validate(); err != nil {
			return nil, err
		}
		return json.Marshal([]executions.Execution{record})
	})
}

func (s *ExecutionFsStore) SaveCheckpoint(ctx context.Context, agentID, commandID, name string, data json.RawMessage) error {
	path, err := s.path(ctx, agentID, commandID)
	if err != nil {
		return err
	}
	if err := executions.ValidateCheckpoint(name, data); err != nil {
		return err
	}
	// Like completion, a checkpoint cannot create an unclaimed execution.
	if _, err := s.Get(ctx, agentID, commandID); err != nil {
		return err
	}
	collection, err := jsonfile.NewContext(ctx, path)
	if err != nil {
		return err
	}
	return collection.Update(ctx, func(current []byte) ([]byte, error) {
		record, err := decodeExecution(current, agentID, commandID)
		if err != nil {
			return nil, err
		}
		if _, err := record.RecordCheckpoint(name, data); err != nil {
			return nil, err
		}
		// Exact retries still publish and sync to confirm an uncertain write.
		return json.Marshal([]executions.Execution{record})
	})
}

func (s *ExecutionFsStore) Get(ctx context.Context, agentID, commandID string) (executions.Execution, error) {
	path, err := s.path(ctx, agentID, commandID)
	if err != nil {
		return executions.Execution{}, err
	}
	data, err := jsonfile.Read(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		data, err = []byte("[]"), nil
	}
	if err != nil {
		return executions.Execution{}, err
	}
	return decodeExecution(data, agentID, commandID)
}

func (s *ExecutionFsStore) path(ctx context.Context, agentID, commandID string) (string, error) {
	if ctx == nil {
		return "", errors.New("execution database requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !id.ValidAgent(agentID) || !id.Valid(commandID) {
		return "", errors.New("execution requires a valid agent ID and a canonical UUID command ID")
	}
	return filepath.Join(s.root, "executions", agentID, commandID+".json"), nil
}

func sameExecutionMessage(a, b messages.Message) bool {
	if a.ID != b.ID || a.Type != b.Type || a.AgentID != b.AgentID || a.CorrelationID != b.CorrelationID || a.Work != b.Work {
		return false
	}
	var first, second any
	decode := func(payload json.RawMessage, target *any) error {
		decoder := json.NewDecoder(bytes.NewReader(payload))
		// Preserve integer precision when comparing input or result identifiers.
		decoder.UseNumber()
		return decoder.Decode(target)
	}
	return decode(a.Payload, &first) == nil && decode(b.Payload, &second) == nil && reflect.DeepEqual(first, second)
}

func decodeExecution(data []byte, agentID, commandID string) (executions.Execution, error) {
	var records []executions.Execution
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&records); err != nil {
		return executions.Execution{}, fmt.Errorf("decode execution: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return executions.Execution{}, errors.New("execution file must contain exactly one JSON array")
	}
	if len(records) == 0 {
		return executions.Execution{}, executions.ErrUnknownExecution
	}
	if len(records) != 1 {
		return executions.Execution{}, errors.New("execution file must contain exactly one record")
	}
	record := records[0]
	if err := record.Validate(); err != nil {
		return executions.Execution{}, err
	}
	if record.Command.AgentID != agentID || record.Command.ID != commandID {
		return executions.Execution{}, errors.New("execution file does not match its identity")
	}
	return record, nil
}
