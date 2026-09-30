package operations

import (
	"context"
	"errors"
	"os"
	"slices"
	"sort"

	"commander/framework/domain/operations"
	"commander/framework/id"
	"commander/providers/fs/internal/jsonfile"
)

func (s *OperationFsStore) GetTask(ctx context.Context, operationID, taskID string) (operations.Task, error) {
	if !id.Valid(taskID) {
		return operations.Task{}, errors.New("task ID must be a canonical UUID")
	}
	var task operations.Task
	err := s.viewOperation(ctx, operationID, func(metadata operationMetadata, view *jsonfile.RecordView) error {
		if !slices.Contains(metadata.TaskIDs, taskID) {
			return operations.ErrUnknownTask
		}
		var err error
		task, err = readTask(view, taskID)
		return err
	})
	if err != nil {
		return operations.Task{}, err
	}
	return (operations.Operation{Tasks: []operations.Task{task}}).Clone().Tasks[0], nil
}

func (s *OperationFsStore) ListTasks(ctx context.Context, operationID, afterTaskID string, limit int) ([]operations.Task, error) {
	if limit <= 0 {
		return nil, errors.New("task limit must be positive")
	}
	if afterTaskID != "" && !id.Valid(afterTaskID) {
		return nil, errors.New("task cursor must be a canonical UUID")
	}
	var tasks []operations.Task
	err := s.viewOperation(ctx, operationID, func(metadata operationMetadata, view *jsonfile.RecordView) error {
		start := 0
		if afterTaskID != "" {
			index := slices.Index(metadata.TaskIDs, afterTaskID)
			if index < 0 {
				return operations.ErrUnknownTask
			}
			start = index + 1
		}
		ids := metadata.TaskIDs[start : start+min(limit, len(metadata.TaskIDs)-start)]
		next := make([]operations.Task, 0, len(ids))
		for _, taskID := range ids {
			task, err := readTask(view, taskID)
			if err != nil {
				return err
			}
			next = append(next, task)
		}
		tasks = next
		return nil
	})
	if err != nil {
		return nil, err
	}
	return (operations.Operation{Tasks: tasks}).Clone().Tasks, nil
}

func (s *OperationFsStore) ListEvents(ctx context.Context, operationID string, afterSequence uint64, limit int) ([]operations.Event, error) {
	if limit <= 0 {
		return nil, errors.New("event limit must be positive")
	}
	var events []operations.Event
	err := s.viewOperation(ctx, operationID, func(metadata operationMetadata, view *jsonfile.RecordView) error {
		sequences := metadata.EventSequences
		slices.Sort(sequences)
		start := sort.Search(len(sequences), func(i int) bool { return sequences[i] > afterSequence })
		sequences = sequences[start : start+min(limit, len(sequences)-start)]
		next := make([]operations.Event, 0, len(sequences))
		for _, sequence := range sequences {
			event, err := readEvent(view, operationID, sequence)
			if err != nil {
				return err
			}
			next = append(next, event)
		}
		events = next
		return nil
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

func (s *OperationFsStore) viewOperation(ctx context.Context, operationID string, read func(operationMetadata, *jsonfile.RecordView) error) error {
	path, err := s.path(ctx, operationID)
	if err != nil {
		return err
	}
	file, err := jsonfile.OpenRecords(ctx, path)
	if err == nil {
		err = file.View(ctx, func(view *jsonfile.RecordView) error {
			metadata, err := readOperationMetadata(view, operationID)
			if err != nil {
				return err
			}
			return read(metadata, view)
		})
	}
	if errors.Is(err, os.ErrNotExist) {
		return operations.ErrUnknownOperation
	}
	return err
}
