package sessions

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"

	model "github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/fs/internal/jsonfile"
)

// One collection atomically retains scope reservations and historical release receipts.
type SessionFsStore struct{ file *jsonfile.Collection }

var _ model.Store = (*SessionFsStore)(nil)

func NewSessionFsStore(root string) (*SessionFsStore, error) {
	if root == "" {
		root = filepath.Join("state", "database")
	}
	if err := jsonfile.PrepareDirectory(root); err != nil {
		return nil, err
	}
	f, err := jsonfile.New(filepath.Join(root, "runtime-sessions.json"))
	if err != nil {
		return nil, err
	}
	return &SessionFsStore{file: f}, nil
}
func decode(data []byte) ([]model.Scope, error) {
	var scopes []model.Scope
	if err := json.Unmarshal(data, &scopes); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, s := range scopes {
		if err := s.Validate(); err != nil {
			return nil, err
		}
		if seen[s.ID] {
			return nil, model.ErrConflict
		}
		seen[s.ID] = true
	}
	return scopes, nil
}
func (s *SessionFsStore) List(ctx context.Context) ([]model.Scope, error) {
	var result []model.Scope
	err := s.file.View(ctx, func(data []byte) error { var err error; result, err = decode(data); return err })
	return result, err
}

func (s *SessionFsStore) Find(ctx context.Context, query model.Query) ([]model.Record, error) {
	if err := query.Validate(); err != nil {
		return nil, err
	}
	result := []model.Record{}
	err := s.file.View(ctx, func(data []byte) error {
		scopes, err := decode(data)
		if err != nil {
			return err
		}
		for _, scope := range scopes {
			if err := ctx.Err(); err != nil {
				return err
			}
			if query.ScopeID != "" && scope.ID != query.ScopeID {
				continue
			}
			for _, record := range scope.Records {
				if query.Matches(record) {
					result = append(result, record.Clone())
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(result, func(a, b model.Record) int {
		if order := cmp.Compare(a.ScopeID, b.ScopeID); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return result, nil
}

func (s *SessionFsStore) Get(ctx context.Context, scopeID string) (model.Scope, error) {
	if !id.Valid(scopeID) {
		return model.Scope{}, model.ErrConflict
	}
	scopes, err := s.List(ctx)
	if err != nil {
		return model.Scope{}, err
	}
	for _, scope := range scopes {
		if scope.ID == scopeID {
			return scope, nil
		}
	}
	return model.Scope{ID: scopeID, Records: []model.Record{}}, nil
}
func (s *SessionFsStore) Apply(ctx context.Context, c model.Change) (model.Record, error) {
	var result model.Record
	err := s.file.Update(ctx, func(data []byte) ([]byte, error) {
		scopes, err := decode(data)
		if err != nil {
			return nil, err
		}
		index := -1
		for i := range scopes {
			if scopes[i].ID == c.ScopeID {
				index = i
				break
			}
		}
		if index < 0 {
			scopes = append(scopes, model.Scope{ID: c.ScopeID})
			index = len(scopes) - 1
		}
		if err := scopes[index].Apply(c); err != nil {
			return nil, err
		}
		result, err = scopes[index].Find(c.ID)
		if err != nil {
			return nil, err
		}
		return json.Marshal(scopes)
	})
	if errors.Is(err, jsonfile.ErrWriteUncertain) {
		err = errors.Join(model.ErrWriteUncertain, err)
	}
	return result, err
}
