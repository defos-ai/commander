// Package catalog shares typed JSON collection mechanics between controller filesystem catalogs.
package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"commander/providers/internal/jsonfile"
)

// Store shares collection mechanics while each domain keeps a typed contract.
type Store[T any] struct {
	file     *jsonfile.Collection
	name     func(T) string
	validate func(T) error
	unknown  error
}

func New[T any](root, filename string, name func(T) string, validate func(T) error, unknown error) (*Store[T], error) {
	if root == "" {
		root = filepath.Join("state", "database")
	}
	file, err := jsonfile.New(filepath.Join(root, filename))
	if err != nil {
		return nil, err
	}
	catalog := &Store[T]{file: file, name: name, validate: validate, unknown: unknown}
	if err := file.View(context.Background(), func(data []byte) error {
		_, err := catalog.decode(data)
		return err
	}); err != nil {
		return nil, err
	}
	return catalog, nil
}

func (s *Store[T]) decode(data []byte) ([]T, error) {
	var records []T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&records); err != nil {
		return nil, err
	}
	if records == nil {
		return nil, errors.New("catalog must be a JSON array")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("catalog must contain one JSON array")
	}
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		if err := s.validate(record); err != nil {
			return nil, err
		}
		key := s.name(record)
		if seen[key] {
			return nil, fmt.Errorf("duplicate catalog name %q", key)
		}
		seen[key] = true
	}
	return records, nil
}

func (s *Store[T]) Upsert(ctx context.Context, record T) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.validate(record); err != nil {
		return err
	}
	return s.file.Update(ctx, func(data []byte) ([]byte, error) {
		records, err := s.decode(data)
		if err != nil {
			return nil, err
		}
		found := false
		for i, current := range records {
			if s.name(current) == s.name(record) {
				records[i], found = record, true
				break
			}
		}
		if !found {
			records = append(records, record)
		}
		sort.Slice(records, func(i, j int) bool { return s.name(records[i]) < s.name(records[j]) })
		return json.MarshalIndent(records, "", "  ")
	})
}

func (s *Store[T]) Get(ctx context.Context, name string) (T, error) {
	var result T
	err := s.file.View(ctx, func(data []byte) error {
		records, err := s.decode(data)
		if err != nil {
			return err
		}
		for _, record := range records {
			if s.name(record) == name {
				result = record
				return nil
			}
		}
		return s.unknown
	})
	return result, err
}

func (s *Store[T]) List(ctx context.Context) ([]T, error) {
	var result []T
	err := s.file.View(ctx, func(data []byte) error {
		var err error
		result, err = s.decode(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return s.name(result[i]) < s.name(result[j]) })
	return result, nil
}
