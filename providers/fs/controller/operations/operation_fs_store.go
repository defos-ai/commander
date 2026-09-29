package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"commander/framework/domain/actions"
	"commander/framework/domain/operations"
	"commander/framework/id"
	"commander/providers/internal/jsonfile"
)

// OperationFsStore keeps each complete snapshot under operations/<id>.json.
// Updates share a persistent lock only with writers of the same operation.
type OperationFsStore struct {
	root      string
	directory string
}

type storedOperation struct {
	operations.Operation
	Version uint64 `json:"version"`
}

var _ operations.OperationStore = (*OperationFsStore)(nil)

var errUnchangedOperation = errors.New("operation unchanged")

func NewOperationFsStore(root string) (*OperationFsStore, error) {
	if root == "" {
		root = filepath.Join("state", "database")
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("operation database root must not be blank")
	}
	directory, err := filepath.Abs(filepath.Join(root, "operations"))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(filepath.Dir(directory))
	if err != nil {
		return nil, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	return &OperationFsStore{root: root, directory: directory}, nil
}

func (s *OperationFsStore) Root() string { return s.root }

func (s *OperationFsStore) Create(ctx context.Context, operation operations.Operation) error {
	path, err := s.path(ctx, operation.ID)
	if err != nil {
		return err
	}
	snapshot := operation.Clone()
	file, err := jsonfile.OpenContext(ctx, path)
	if err != nil {
		return err
	}
	err = file.Update(ctx, func(data []byte) ([]byte, error) {
		_, err := decodeOperation(data, operation.ID)
		if errors.Is(err, operations.ErrUnknownOperation) {
			return encodeOperation(storedOperation{Operation: snapshot, Version: 1})
		}
		if err != nil {
			return nil, err
		}
		return nil, operations.ErrOperationConflict
	})
	return operationWriteError(operation.ID, err)
}

func (s *OperationFsStore) Get(ctx context.Context, operationID string) (operations.Operation, error) {
	path, err := s.path(ctx, operationID)
	if err != nil {
		return operations.Operation{}, err
	}
	data, err := jsonfile.Read(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return operations.Operation{}, operations.ErrUnknownOperation
	}
	if err != nil {
		return operations.Operation{}, err
	}
	stored, err := decodeOperation(data, operationID)
	if err != nil {
		return operations.Operation{}, err
	}
	return stored.Operation.Clone(), nil
}

func (s *OperationFsStore) List(ctx context.Context) ([]operations.Operation, error) {
	if ctx == nil {
		return nil, errors.New("operation database requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := []operations.Operation{}
	entries, err := os.ReadDir(s.directory)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		operationID := strings.TrimSuffix(entry.Name(), ".json")
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || !id.Valid(operationID) {
			continue
		}
		operation, err := s.Get(ctx, operationID)
		if errors.Is(err, operations.ErrUnknownOperation) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read operation %s: %w", operationID, err)
		}
		result = append(result, operation)
	}
	return result, nil
}

func (s *OperationFsStore) GetTask(ctx context.Context, operationID, taskID string) (operations.Task, error) {
	if !id.Valid(taskID) {
		return operations.Task{}, errors.New("task ID must be a canonical UUID")
	}
	operation, err := s.Get(ctx, operationID)
	if err != nil {
		return operations.Task{}, err
	}
	for _, task := range operation.Tasks {
		if task.ID == taskID {
			return task, nil
		}
	}
	return operations.Task{}, operations.ErrUnknownTask
}

func (s *OperationFsStore) ListTasks(ctx context.Context, operationID, afterTaskID string, limit int) ([]operations.Task, error) {
	if limit <= 0 {
		return nil, errors.New("task limit must be positive")
	}
	if afterTaskID != "" && !id.Valid(afterTaskID) {
		return nil, errors.New("task cursor must be a canonical UUID")
	}
	operation, err := s.Get(ctx, operationID)
	if err != nil {
		return nil, err
	}
	start := 0
	if afterTaskID != "" {
		index := slices.IndexFunc(operation.Tasks, func(task operations.Task) bool { return task.ID == afterTaskID })
		if index < 0 {
			return nil, operations.ErrUnknownTask
		}
		start = index + 1
	}
	return operation.Tasks[start : start+min(limit, len(operation.Tasks)-start)], nil
}

func (s *OperationFsStore) ListEvents(ctx context.Context, operationID string, afterSequence uint64, limit int) ([]operations.Event, error) {
	if limit <= 0 {
		return nil, errors.New("event limit must be positive")
	}
	operation, err := s.Get(ctx, operationID)
	if err != nil {
		return nil, err
	}
	sort.Slice(operation.Events, func(i, j int) bool { return operation.Events[i].Sequence < operation.Events[j].Sequence })
	events := make([]operations.Event, 0, min(limit, len(operation.Events)))
	for _, event := range operation.Events {
		if event.Sequence > afterSequence {
			events = append(events, event)
			if len(events) == limit {
				break
			}
		}
	}
	return events, nil
}

func (s *OperationFsStore) QueueTask(ctx context.Context, operationID string, task operations.Task) (operations.Operation, error) {
	return s.updateChanged(ctx, operationID, func(next *operations.Operation) (bool, error) {
		return next.QueueTask(task)
	})
}

func (s *OperationFsStore) DispatchTask(ctx context.Context, operationID string, dispatch operations.TaskDispatch) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(next *operations.Operation) error {
		return next.DispatchTask(dispatch)
	})
}

func (s *OperationFsStore) FinishTask(ctx context.Context, operationID string, result operations.TaskResult) (operations.Operation, error) {
	return s.updateChanged(ctx, operationID, func(next *operations.Operation) (bool, error) {
		return next.FinishTask(result)
	})
}

func (s *OperationFsStore) ResolveContinuation(ctx context.Context, operationID, taskID string, version uint64, next []actions.NextAction) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(operation *operations.Operation) error {
		return operation.ResolveContinuation(taskID, version, next)
	})
}

func (s *OperationFsStore) Complete(ctx context.Context, operationID, reportID string, output json.RawMessage) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(operation *operations.Operation) error {
		return operation.Complete(reportID, output)
	})
}

func (s *OperationFsStore) Fail(ctx context.Context, operationID, reason string) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(operation *operations.Operation) error {
		return operation.Fail(reason)
	})
}

func (s *OperationFsStore) Cancel(ctx context.Context, operationID string) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(operation *operations.Operation) error {
		return operation.Cancel()
	})
}

func (s *OperationFsStore) updateChanged(ctx context.Context, operationID string, mutate func(*operations.Operation) (bool, error)) (operations.Operation, error) {
	var unchanged operations.Operation
	next, err := s.Update(ctx, operationID, func(next *operations.Operation) error {
		changed, err := mutate(next)
		if err != nil {
			return err
		}
		if !changed {
			unchanged = next.Clone()
			return errUnchangedOperation
		}
		return nil
	})
	if errors.Is(err, errUnchangedOperation) {
		return unchanged, nil
	}
	return next, err
}

func (s *OperationFsStore) Update(ctx context.Context, operationID string, mutate func(*operations.Operation) error) (operations.Operation, error) {
	path, err := s.path(ctx, operationID)
	if err != nil {
		return operations.Operation{}, err
	}
	if mutate == nil {
		return operations.Operation{}, errors.New("operation mutation is required")
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return operations.Operation{}, operations.ErrUnknownOperation
	} else if err != nil {
		return operations.Operation{}, err
	}
	file, err := jsonfile.OpenContext(ctx, path)
	if err != nil {
		return operations.Operation{}, err
	}
	var result operations.Operation
	err = file.Update(ctx, func(data []byte) ([]byte, error) {
		current, err := decodeOperation(data, operationID)
		if err != nil {
			return nil, err
		}
		next := current.Clone()
		if err := mutate(&next); err != nil {
			return nil, err
		}
		if next.ID != operationID {
			return nil, errors.New("operation identity cannot change")
		}
		if current.Version == ^uint64(0) {
			return nil, errors.New("operation storage version exhausted")
		}
		result = next.Clone()
		return encodeOperation(storedOperation{Operation: result, Version: current.Version + 1})
	})
	if errors.Is(err, jsonfile.ErrWriteUncertain) {
		return result, operationWriteError(operationID, err)
	}
	if err != nil {
		return operations.Operation{}, err
	}
	return result, nil
}

func (s *OperationFsStore) path(ctx context.Context, operationID string) (string, error) {
	if ctx == nil {
		return "", errors.New("operation database requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !id.Valid(operationID) {
		return "", errors.New("operation ID must be a canonical UUID")
	}
	return filepath.Join(s.directory, operationID+".json"), nil
}

func decodeOperation(data []byte, operationID string) (storedOperation, error) {
	var records []storedOperation
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&records); err != nil {
		return storedOperation{}, fmt.Errorf("decode operation: %w", err)
	}
	if records == nil {
		return storedOperation{}, errors.New("operation must be a JSON array")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return storedOperation{}, errors.New("operation must contain exactly one JSON array")
	}
	if len(records) == 0 {
		return storedOperation{}, operations.ErrUnknownOperation
	}
	if len(records) != 1 || records[0].ID != operationID || records[0].Version == 0 {
		return storedOperation{}, errors.New("operation file must contain one matching UUID and a positive version")
	}
	return records[0], nil
}

func operationWriteError(operationID string, err error) error {
	if errors.Is(err, jsonfile.ErrWriteUncertain) {
		return fmt.Errorf("write operation %s: %w", operationID, errors.Join(operations.ErrWriteUncertain, err))
	}
	return err
}

func encodeOperation(record storedOperation) ([]byte, error) {
	data, err := json.MarshalIndent([]storedOperation{record}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode operation: %w", err)
	}
	return append(data, '\n'), nil
}
