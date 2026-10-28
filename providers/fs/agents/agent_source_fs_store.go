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

type AgentSourceFsStore struct{ file *jsonfile.Collection }

var _ agentmodel.AgentSourceStore = (*AgentSourceFsStore)(nil)

func NewAgentSourceFsStore(root string) (*AgentSourceFsStore, error) {
	if root == "" {
		root = filepath.Join("state", "database")
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("agent source database root must not be blank")
	}
	file, err := jsonfile.New(filepath.Join(root, "agent-sources.json"))
	if err != nil {
		return nil, agentSourceWriteError(err)
	}
	store := &AgentSourceFsStore{file: file}
	if _, err := store.List(context.Background()); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *AgentSourceFsStore) Create(ctx context.Context, entry agentmodel.AgentSource) error {
	if err := agentSourceContext(ctx); err != nil {
		return err
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	if entry.Version != 1 || !entry.CreatedAt.Equal(entry.UpdatedAt) {
		return errors.New("new agent sources require version 1 and equal creation and update times")
	}
	return s.update(ctx, func(entries []agentmodel.AgentSource) ([]agentmodel.AgentSource, error) {
		for _, current := range entries {
			if current.Source == entry.Source {
				return nil, agentmodel.ErrAgentSourceExists
			}
		}
		return append(entries, entry), nil
	})
}

func (s *AgentSourceFsStore) Get(ctx context.Context, reference string) (agentmodel.AgentSource, error) {
	if err := agentSourceContext(ctx); err != nil {
		return agentmodel.AgentSource{}, err
	}
	if err := agentmodel.ValidateAgentSource(reference); err != nil {
		return agentmodel.AgentSource{}, err
	}
	var result agentmodel.AgentSource
	err := s.file.View(ctx, func(data []byte) error {
		entries, err := decodeAgentSources(data)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Source == reference {
				result = entry
				return nil
			}
		}
		return agentmodel.ErrUnknownAgentSource
	})
	if err != nil {
		return agentmodel.AgentSource{}, err
	}
	return result, nil
}

func (s *AgentSourceFsStore) List(ctx context.Context) ([]agentmodel.AgentSource, error) {
	if err := agentSourceContext(ctx); err != nil {
		return nil, err
	}
	var entries []agentmodel.AgentSource
	err := s.file.View(ctx, func(data []byte) error {
		var err error
		entries, err = decodeAgentSources(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	sortAgentSources(entries)
	return entries, nil
}

func (s *AgentSourceFsStore) Replace(ctx context.Context, entry agentmodel.AgentSource, expectedVersion uint64) error {
	if err := agentSourceContext(ctx); err != nil {
		return err
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	if expectedVersion == 0 || expectedVersion == math.MaxUint64 || entry.Version != expectedVersion+1 {
		return agentmodel.ErrAgentSourceConflict
	}
	return s.update(ctx, func(entries []agentmodel.AgentSource) ([]agentmodel.AgentSource, error) {
		for i, current := range entries {
			if current.Source != entry.Source {
				continue
			}
			if current.Version != expectedVersion || !entry.CreatedAt.Equal(current.CreatedAt) || entry.UpdatedAt.Before(current.UpdatedAt) {
				return nil, agentmodel.ErrAgentSourceConflict
			}
			entries[i] = entry
			return entries, nil
		}
		return nil, agentmodel.ErrUnknownAgentSource
	})
}

func (s *AgentSourceFsStore) update(ctx context.Context, mutate func([]agentmodel.AgentSource) ([]agentmodel.AgentSource, error)) error {
	err := s.file.Update(ctx, func(data []byte) ([]byte, error) {
		entries, err := decodeAgentSources(data)
		if err != nil {
			return nil, err
		}
		entries, err = mutate(entries)
		if err != nil {
			return nil, err
		}
		sortAgentSources(entries)
		return json.MarshalIndent(entries, "", "  ")
	})
	return agentSourceWriteError(err)
}

func decodeAgentSources(data []byte) ([]agentmodel.AgentSource, error) {
	var entries []agentmodel.AgentSource
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return nil, errors.New("invalid stored agent source encoding")
	}
	if entries == nil {
		return nil, errors.New("agent sources must be a JSON array")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("agent sources must contain one JSON array")
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if err := entry.Validate(); err != nil {
			return nil, errors.New("invalid stored agent source")
		}
		if seen[entry.Source] {
			return nil, errors.New("stored agent sources contain duplicate sources")
		}
		seen[entry.Source] = true
	}
	return entries, nil
}

func sortAgentSources(entries []agentmodel.AgentSource) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Source < entries[j].Source })
}

func agentSourceContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("agent source store requires a context")
	}
	return ctx.Err()
}

func agentSourceWriteError(err error) error {
	if errors.Is(err, jsonfile.ErrWriteUncertain) {
		return errors.Join(agentmodel.ErrAgentSourceWriteUncertain, err)
	}
	return err
}
