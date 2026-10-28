package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strings"

	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/providers/fs/internal/jsonfile"
)

type CatalogFsStore struct{ file *jsonfile.Collection }

var _ agentmodel.CatalogStore = (*CatalogFsStore)(nil)

func NewCatalogFsStore(root string) (*CatalogFsStore, error) {
	if root == "" {
		root = filepath.Join("state", "database")
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("agent catalog database root must not be blank")
	}
	file, err := jsonfile.New(filepath.Join(root, "agent-catalog.json"))
	if err != nil {
		return nil, catalogWriteError(err)
	}
	store := &CatalogFsStore{file: file}
	if _, err := store.List(context.Background()); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *CatalogFsStore) Create(ctx context.Context, entry agentmodel.CatalogEntry) error {
	if err := catalogContext(ctx); err != nil {
		return err
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	if entry.Version != 1 || !entry.CreatedAt.Equal(entry.UpdatedAt) {
		return errors.New("new catalog entries require version 1 and equal creation and update times")
	}
	return s.update(ctx, func(entries []agentmodel.CatalogEntry) ([]agentmodel.CatalogEntry, error) {
		for _, current := range entries {
			if current.Package == entry.Package {
				return nil, agentmodel.ErrCatalogEntryExists
			}
		}
		return append(entries, entry), nil
	})
}

func (s *CatalogFsStore) Get(ctx context.Context, reference string) (agentmodel.CatalogEntry, error) {
	if err := catalogContext(ctx); err != nil {
		return agentmodel.CatalogEntry{}, err
	}
	if err := agentmodel.ValidateCatalogPackage(reference); err != nil {
		return agentmodel.CatalogEntry{}, err
	}
	var result agentmodel.CatalogEntry
	err := s.file.View(ctx, func(data []byte) error {
		entries, err := decodeCatalogEntries(data)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Package == reference {
				result = entry
				return nil
			}
		}
		return agentmodel.ErrUnknownCatalogEntry
	})
	if err != nil {
		return agentmodel.CatalogEntry{}, err
	}
	return result, nil
}

func (s *CatalogFsStore) List(ctx context.Context) ([]agentmodel.CatalogEntry, error) {
	if err := catalogContext(ctx); err != nil {
		return nil, err
	}
	var entries []agentmodel.CatalogEntry
	err := s.file.View(ctx, func(data []byte) error {
		var err error
		entries, err = decodeCatalogEntries(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	sortCatalogEntries(entries)
	return entries, nil
}

func (s *CatalogFsStore) Replace(ctx context.Context, entry agentmodel.CatalogEntry, expectedVersion uint64) error {
	if err := catalogContext(ctx); err != nil {
		return err
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	if expectedVersion == 0 || expectedVersion == math.MaxUint64 || entry.Version != expectedVersion+1 {
		return agentmodel.ErrCatalogConflict
	}
	return s.update(ctx, func(entries []agentmodel.CatalogEntry) ([]agentmodel.CatalogEntry, error) {
		for i, current := range entries {
			if current.Package != entry.Package {
				continue
			}
			if current.Version != expectedVersion || !entry.CreatedAt.Equal(current.CreatedAt) || entry.UpdatedAt.Before(current.UpdatedAt) {
				return nil, agentmodel.ErrCatalogConflict
			}
			entries[i] = entry
			return entries, nil
		}
		return nil, agentmodel.ErrUnknownCatalogEntry
	})
}

func (s *CatalogFsStore) update(ctx context.Context, mutate func([]agentmodel.CatalogEntry) ([]agentmodel.CatalogEntry, error)) error {
	err := s.file.Update(ctx, func(data []byte) ([]byte, error) {
		entries, err := decodeCatalogEntries(data)
		if err != nil {
			return nil, err
		}
		entries, err = mutate(entries)
		if err != nil {
			return nil, err
		}
		sortCatalogEntries(entries)
		return json.MarshalIndent(entries, "", "  ")
	})
	return catalogWriteError(err)
}

func decodeCatalogEntries(data []byte) ([]agentmodel.CatalogEntry, error) {
	var entries []agentmodel.CatalogEntry
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return nil, errors.New("invalid stored agent catalog encoding")
	}
	if entries == nil {
		return nil, errors.New("agent catalog must be a JSON array")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("agent catalog must contain one JSON array")
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if err := entry.Validate(); err != nil {
			return nil, errors.New("invalid stored agent catalog entry")
		}
		if seen[entry.Package] {
			return nil, errors.New("stored agent catalog contains duplicate packages")
		}
		seen[entry.Package] = true
	}
	return entries, nil
}

func sortCatalogEntries(entries []agentmodel.CatalogEntry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Package < entries[j].Package })
}

func catalogContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("agent catalog requires a context")
	}
	return ctx.Err()
}

func catalogWriteError(err error) error {
	if errors.Is(err, jsonfile.ErrWriteUncertain) {
		return errors.Join(agentmodel.ErrCatalogWriteUncertain, err)
	}
	return err
}
