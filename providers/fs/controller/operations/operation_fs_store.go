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
// Each mutation holds the collection's process-shared lock through the version
// comparison and atomic replacement. Existing invalid data is never overwritten.
type OperationFsStore struct {
	file *jsonfile.Collection
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

// Root returns the absolute directory containing the operation collection.
func (s *OperationFsStore) Root() string { return filepath.Dir(s.file.Path()) }

func (s *OperationFsStore) Create(ctx context.Context, operation operations.Operation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !id.Valid(operation.ID) || operation.Version != 1 {
		return errors.New("new operation requires a UUID and version 1")
	}
	snapshot := operation.Clone()
	return s.file.Update(ctx, func(data []byte) ([]byte, error) {
		records, err := decodeOperations(data)
		if err != nil {
			return nil, err
		}
		for _, current := range records {
			if current.ID == snapshot.ID {
				return nil, operations.ErrOperationConflict
			}
		}
		return encodeOperations(append(records, snapshot))
	})
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

func (s *OperationFsStore) Update(ctx context.Context, operation operations.Operation, expectedVersion uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !id.Valid(operation.ID) {
		return errors.New("operation ID must be a canonical UUID")
	}
	snapshot := operation.Clone()
	return s.file.Update(ctx, func(data []byte) ([]byte, error) {
		records, err := decodeOperations(data)
		if err != nil {
			return nil, err
		}
		for index, current := range records {
			if current.ID != snapshot.ID {
				continue
			}
			// Guard addition before comparing the supplied successor so uint64
			// wraparound can never make version zero a successful update.
			if expectedVersion == 0 || expectedVersion == ^uint64(0) ||
				current.Version != expectedVersion || snapshot.Version != expectedVersion+1 {
				return nil, operations.ErrOperationConflict
			}
			records[index] = snapshot
			return encodeOperations(records)
		}
		return nil, operations.ErrUnknownOperation
	})
}

func decodeOperations(data []byte) ([]operations.Operation, error) {
	var records []operations.Operation
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

func encodeOperations(records []operations.Operation) ([]byte, error) {
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode operations: %w", err)
	}
	return append(data, '\n'), nil
}
