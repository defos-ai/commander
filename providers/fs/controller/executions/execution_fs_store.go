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

	"commander/framework/artifact"
	"commander/framework/comms/messages"
	"commander/framework/domain"
	actionmodel "commander/framework/domain/actions"
	"commander/framework/domain/executions"
	"commander/framework/id"
	"commander/providers/fs/internal/jsonfile"
)

// ExecutionFsStore keeps one execution per locked JSON collection at
// <database>/executions/<agent>/<command>.json. Each file
// contains a single Record. Construction and reads do not create paths.
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

// FindArtifact scans accepted execution outcomes without creating paths. Database
// providers can index the same persisted references; FS keeps the execution record
// as the single source of truth for acceptance and artifact ownership.
func (s *ExecutionFsStore) FindArtifact(ctx context.Context, artifactID string) (artifact.Ref, error) {
	if ctx == nil {
		return artifact.Ref{}, errors.New("execution database requires a context")
	}
	if err := ctx.Err(); err != nil {
		return artifact.Ref{}, err
	}
	if !id.Valid(artifactID) {
		return artifact.Ref{}, errors.New("artifact ID must be a canonical UUID")
	}
	var found artifact.Ref
	root := filepath.Join(s.root, "executions")
	agents, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return artifact.Ref{}, artifact.ErrUnknownArtifact
	}
	if err != nil {
		return artifact.Ref{}, err
	}
	for _, agent := range agents {
		if err := ctx.Err(); err != nil {
			return artifact.Ref{}, err
		}
		if !agent.IsDir() || !id.ValidAgent(agent.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, agent.Name()))
		if err != nil {
			return artifact.Ref{}, err
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return artifact.Ref{}, err
			}
			commandID := strings.TrimSuffix(file.Name(), ".json")
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") || !id.Valid(commandID) {
				continue
			}
			record, err := s.Get(ctx, agent.Name(), commandID)
			if errors.Is(err, executions.ErrUnknownExecution) {
				// A new claim's collection may exist before its first atomic write.
				continue
			}
			if err != nil {
				return artifact.Ref{}, fmt.Errorf("read artifact execution: %w", err)
			}
			if record.Result == nil || record.Result.Type != messages.ActionSucceeded {
				continue
			}
			refs, err := executionArtifacts(record)
			if err != nil {
				return artifact.Ref{}, fmt.Errorf("read execution artifacts: %w", err)
			}
			for _, ref := range refs {
				if ref.ID != artifactID {
					continue
				}
				if found.ID != "" && found != ref {
					return artifact.Ref{}, errors.New("artifact has conflicting metadata across executions")
				}
				found = ref
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return artifact.Ref{}, err
	}
	if found.ID == "" {
		return artifact.Ref{}, artifact.ErrUnknownArtifact
	}
	return found, nil
}

func executionArtifacts(record executions.Execution) ([]artifact.Ref, error) {
	var call actionmodel.ActionCall
	if err := domain.Decode(record.Command.Payload, &call); err != nil {
		return nil, err
	}
	if call.Work != record.Command.Work {
		return nil, errors.New("claimed action request does not match its work context")
	}
	output, err := executions.DecodeActionResult(*record.Result, record.Command.AgentID, record.Command.ID, call)
	if err != nil {
		return nil, err
	}
	return executions.OutputArtifacts(*record.Result, output)
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
