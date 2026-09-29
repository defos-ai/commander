package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"commander/framework/domain/operations"
	"commander/framework/id"
	"commander/providers/internal/jsonfile"
)

// OperationFsStore saves complete operation snapshots in root/operations.json.
// The collection lock covers reading, mutation, and atomic replacement.
type OperationFsStore struct {
	file *jsonfile.Collection
}

type storedOperation struct {
	operations.Operation
	Version uint64 `json:"version"`
}

var _ operations.OperationStore = (*OperationFsStore)(nil)

// NewOperationFsStore opens a JSON array collection, creating it if missing.
// An empty root selects state/database relative to the working directory.
func NewOperationFsStore(root string) (*OperationFsStore, error) {
	if root == "" {
		root = filepath.Join("state", "database")
	}
	file, err := jsonfile.New(filepath.Join(root, "operations.json"))
	if err != nil {
		return nil, err
	}
	if err := file.View(context.Background(), func(data []byte) error {
		_, err := decodeOperations(data)
		return err
	}); err != nil {
		return nil, err
	}
	return &OperationFsStore{file: file}, nil
}

func (s *OperationFsStore) Root() string { return filepath.Dir(s.file.Path()) }

func (s *OperationFsStore) Create(ctx context.Context, operation operations.Operation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !id.Valid(operation.ID) {
		return errors.New("new operation requires a UUID")
	}
	snapshot := operation.Clone()
	err := s.file.Update(ctx, func(data []byte) ([]byte, error) {
		records, err := decodeOperations(data)
		if err != nil {
			return nil, err
		}
		for _, current := range records {
			if current.ID == snapshot.ID {
				return nil, operations.ErrOperationConflict
			}
		}
		return encodeOperations(append(records, storedOperation{Operation: snapshot, Version: 1}))
	})
	return operationWriteError(operation.ID, err)
}

func (s *OperationFsStore) Get(ctx context.Context, operationID string) (operations.Operation, error) {
	if err := ctx.Err(); err != nil {
		return operations.Operation{}, err
	}
	if !id.Valid(operationID) {
		return operations.Operation{}, errors.New("operation ID must be a canonical UUID")
	}
	var result operations.Operation
	err := s.file.View(ctx, func(data []byte) error {
		records, err := decodeOperations(data)
		if err != nil {
			return err
		}
		for _, operation := range records {
			if operation.ID == operationID {
				result = operation.Clone()
				return nil
			}
		}
		return operations.ErrUnknownOperation
	})
	if err != nil {
		return operations.Operation{}, err
	}
	return result, nil
}

func (s *OperationFsStore) List(ctx context.Context) ([]operations.Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := []operations.Operation{}
	err := s.file.View(ctx, func(data []byte) error {
		records, err := decodeOperations(data)
		if err != nil {
			return err
		}
		for _, operation := range records {
			result = append(result, operation.Clone())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (s *OperationFsStore) Update(ctx context.Context, operationID string, mutate func(*operations.Operation) error) (operations.Operation, error) {
	if err := ctx.Err(); err != nil {
		return operations.Operation{}, err
	}
	if !id.Valid(operationID) {
		return operations.Operation{}, errors.New("operation ID must be a canonical UUID")
	}
	if mutate == nil {
		return operations.Operation{}, errors.New("operation mutation is required")
	}
	var result operations.Operation
	err := s.file.Update(ctx, func(data []byte) ([]byte, error) {
		records, err := decodeOperations(data)
		if err != nil {
			return nil, err
		}
		for index, current := range records {
			if current.ID != operationID {
				continue
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
			records[index] = storedOperation{Operation: result, Version: current.Version + 1}
			return encodeOperations(records)
		}
		return nil, operations.ErrUnknownOperation
	})
	if errors.Is(err, jsonfile.ErrWriteUncertain) {
		return result, operationWriteError(operationID, err)
	}
	if err != nil {
		return operations.Operation{}, err
	}
	return result, nil
}

func decodeOperations(data []byte) ([]storedOperation, error) {
	var records []storedOperation
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&records); err != nil {
		return nil, fmt.Errorf("decode operations: %w", err)
	}
	if records == nil {
		return nil, errors.New("operations must be a JSON array")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("operations must contain exactly one JSON array")
	}
	seen := make(map[string]bool, len(records))
	for _, operation := range records {
		if !id.Valid(operation.ID) || operation.Version == 0 {
			return nil, errors.New("stored operations require a UUID and a positive version")
		}
		if seen[operation.ID] {
			return nil, errors.New("operations contain a duplicate UUID")
		}
		seen[operation.ID] = true
	}
	return records, nil
}

func operationWriteError(operationID string, err error) error {
	if errors.Is(err, jsonfile.ErrWriteUncertain) {
		return fmt.Errorf("write operation %s: %w", operationID, errors.Join(operations.ErrWriteUncertain, err))
	}
	return err
}

func encodeOperations(records []storedOperation) ([]byte, error) {
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode operations: %w", err)
	}
	return append(data, '\n'), nil
}
