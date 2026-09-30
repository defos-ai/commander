package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/operations"
	"commander/framework/id"
	"commander/providers/internal/jsonfile"
)

// OperationFsStore atomically publishes metadata, tasks, and questions through
// operations/<id>.json. Writers share a lock only with the same operation.
type OperationFsStore struct {
	root      string
	directory string
}

var _ operations.OperationStore = (*OperationFsStore)(nil)

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
	if err := jsonfile.PrepareDirectory(directory); err != nil {
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
	file, err := jsonfile.OpenRecords(ctx, path)
	if err != nil {
		return err
	}
	err = file.Update(ctx, func(records map[string]json.RawMessage) error {
		if len(records) != 0 {
			if _, err := decodeOperation(records, operation.ID); err != nil {
				return err
			}
			return operations.ErrOperationConflict
		}
		next, err := encodeOperation(snapshot)
		if err != nil {
			return err
		}
		maps.Copy(records, next)
		return nil
	})
	return operationWriteError(operation.ID, err)
}

func (s *OperationFsStore) Get(ctx context.Context, operationID string) (operations.Operation, error) {
	path, err := s.path(ctx, operationID)
	if err != nil {
		return operations.Operation{}, err
	}
	file, err := jsonfile.OpenRecords(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return operations.Operation{}, operations.ErrUnknownOperation
	}
	if err != nil {
		return operations.Operation{}, err
	}
	data, err := file.Read(ctx)
	if errors.Is(err, os.ErrNotExist) {
		return operations.Operation{}, operations.ErrUnknownOperation
	}
	if err != nil {
		return operations.Operation{}, err
	}
	return decodeOperation(data, operationID)
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

func (s *OperationFsStore) RefreshPreflight(ctx context.Context, operationID string, records []agents.Agent) (operations.Operation, error) {
	return s.updateChanged(ctx, operationID, func(operation *operations.Operation) (bool, error) {
		return operation.RefreshPreflight(records)
	})
}

func (s *OperationFsStore) Recover(ctx context.Context, operationID string, now time.Time) (operations.Operation, error) {
	return s.updateChanged(ctx, operationID, func(operation *operations.Operation) (bool, error) {
		return operation.Recover(now)
	})
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

func (s *OperationFsStore) AskQuestion(ctx context.Context, operationID string, question operations.Question, version uint64) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(operation *operations.Operation) error {
		return operation.AskQuestion(question, version)
	})
}

func (s *OperationFsStore) AnswerQuestion(ctx context.Context, operationID, questionID, answer string) (operations.Operation, error) {
	return s.Update(ctx, operationID, func(operation *operations.Operation) error {
		return operation.AnswerQuestion(questionID, answer)
	})
}

func (s *OperationFsStore) ExpireQuestions(ctx context.Context, operationID string, now time.Time) (operations.Operation, error) {
	if now.IsZero() {
		return operations.Operation{}, errors.New("question expiration requires a cutoff time")
	}
	return s.updateChanged(ctx, operationID, func(operation *operations.Operation) (bool, error) {
		return operation.ExpireQuestions(now), nil
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
	return s.Update(ctx, operationID, func(next *operations.Operation) error {
		_, err := mutate(next)
		return err
	})
}

// Update is the filesystem provider's mutation primitive. The callback must be
// safe to retry and must not perform I/O or other external side effects.
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
	file, err := jsonfile.OpenRecords(ctx, path)
	if err != nil {
		return operations.Operation{}, err
	}
	var result operations.Operation
	err = file.Update(ctx, func(records map[string]json.RawMessage) error {
		current, err := decodeOperation(records, operationID)
		if err != nil {
			return err
		}
		next := current.Clone()
		if err := mutate(&next); err != nil {
			return err
		}
		if next.ID != operationID {
			return errors.New("operation identity cannot change")
		}
		result = next.Clone()
		encoded, err := encodeOperation(result)
		if err != nil {
			return err
		}
		clear(records)
		maps.Copy(records, encoded)
		return nil
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

func operationWriteError(operationID string, err error) error {
	if errors.Is(err, jsonfile.ErrWriteUncertain) {
		return fmt.Errorf("write operation %s: %w", operationID, errors.Join(operations.ErrWriteUncertain, err))
	}
	return err
}
