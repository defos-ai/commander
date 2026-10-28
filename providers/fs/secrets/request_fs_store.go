package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/fs/internal/jsonfile"
)

type RequestFsStore struct{ file *jsonfile.Collection }

var _ secretmodel.RequestStore = (*RequestFsStore)(nil)

func NewRequestFsStore(root string) (*RequestFsStore, error) {
	if root == "" {
		root = filepath.Join("state", "database")
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("secret request database root must not be blank")
	}
	if err := jsonfile.PrepareDirectory(root); err != nil {
		return nil, err
	}
	file, err := jsonfile.New(filepath.Join(root, "secret-requests.json"))
	if err != nil {
		return nil, writeError(err)
	}
	store := &RequestFsStore{file: file}
	if err := store.view(context.Background(), func([]secretmodel.RequestRecord) error { return nil }); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *RequestFsStore) Create(ctx context.Context, request secretmodel.Request) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	record, err := secretmodel.NewRequestRecord(request)
	if err != nil {
		return err
	}
	return s.update(ctx, func(records []secretmodel.RequestRecord) ([]secretmodel.RequestRecord, error) {
		if index := findRequest(records, request.ID); index >= 0 {
			if !records[index].MatchesRequest(request) {
				return nil, secretmodel.ErrRequestConflict
			}
			return records, nil
		}
		return append(records, record), nil
	})
}

func (s *RequestFsStore) Get(ctx context.Context, requestID string) (secretmodel.RequestRecord, error) {
	if err := checkRequestContext(ctx, requestID); err != nil {
		return secretmodel.RequestRecord{}, err
	}
	var result secretmodel.RequestRecord
	err := s.view(ctx, func(records []secretmodel.RequestRecord) error {
		index := findRequest(records, requestID)
		if index < 0 {
			return secretmodel.ErrUnknownRequest
		}
		result = records[index].Clone()
		return nil
	})
	if err != nil {
		return secretmodel.RequestRecord{}, err
	}
	return result, nil
}

func (s *RequestFsStore) ListPending(ctx context.Context) ([]secretmodel.RequestRecord, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	result := []secretmodel.RequestRecord{}
	err := s.view(ctx, func(records []secretmodel.RequestRecord) error {
		for _, record := range records {
			if record.Request.Status == secretmodel.Pending {
				result = append(result, record.Clone())
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Request.ID < result[j].Request.ID })
	return result, nil
}

func (s *RequestFsStore) SaveDelivery(ctx context.Context, delivery secretmodel.Delivery) error {
	if err := checkRequestContext(ctx, delivery.RequestID); err != nil {
		return err
	}
	snapshot := delivery
	snapshot.Secret = delivery.Secret.Clone()
	return s.update(ctx, func(records []secretmodel.RequestRecord) ([]secretmodel.RequestRecord, error) {
		index := findRequest(records, snapshot.RequestID)
		if index < 0 {
			return nil, secretmodel.ErrUnknownRequest
		}
		for otherIndex, record := range records {
			if otherIndex != index && record.Delivery != nil && record.Delivery.ID == snapshot.ID {
				return nil, secretmodel.ErrRequestConflict
			}
		}
		updated, err := records[index].WithDelivery(snapshot)
		if err != nil {
			return nil, err
		}
		records[index] = updated
		return records, nil
	})
}

func (s *RequestFsStore) Acknowledge(ctx context.Context, installation secretmodel.Installation, now time.Time) error {
	return s.mutate(ctx, installation.RequestID, func(record secretmodel.RequestRecord) (secretmodel.RequestRecord, error) {
		return record.Acknowledge(installation, now)
	})
}

func (s *RequestFsStore) Cancel(ctx context.Context, requestID string, now time.Time) error {
	return s.mutate(ctx, requestID, func(record secretmodel.RequestRecord) (secretmodel.RequestRecord, error) {
		return record.Cancel(now)
	})
}

func (s *RequestFsStore) Expire(ctx context.Context, requestID string, now time.Time) error {
	return s.mutate(ctx, requestID, func(record secretmodel.RequestRecord) (secretmodel.RequestRecord, error) {
		return record.Expire(now)
	})
}

func (s *RequestFsStore) mutate(ctx context.Context, requestID string, fn func(secretmodel.RequestRecord) (secretmodel.RequestRecord, error)) error {
	if err := checkRequestContext(ctx, requestID); err != nil {
		return err
	}
	return s.update(ctx, func(records []secretmodel.RequestRecord) ([]secretmodel.RequestRecord, error) {
		index := findRequest(records, requestID)
		if index < 0 {
			return nil, secretmodel.ErrUnknownRequest
		}
		updated, err := fn(records[index])
		if err != nil {
			return nil, err
		}
		records[index] = updated
		return records, nil
	})
}

func (s *RequestFsStore) view(ctx context.Context, fn func([]secretmodel.RequestRecord) error) error {
	return s.file.View(ctx, func(data []byte) error {
		records, err := decodeRequests(data)
		if err != nil {
			return err
		}
		return fn(records)
	})
}

func (s *RequestFsStore) update(ctx context.Context, fn func([]secretmodel.RequestRecord) ([]secretmodel.RequestRecord, error)) error {
	err := s.file.Update(ctx, func(data []byte) ([]byte, error) {
		records, err := decodeRequests(data)
		if err != nil {
			return nil, err
		}
		records, err = fn(records)
		if err != nil {
			return nil, err
		}
		// Replays also publish and sync to confirm a prior uncertain write.
		return json.Marshal(records)
	})
	return writeError(err)
}

func checkRequestContext(ctx context.Context, requestID string) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if !id.Valid(requestID) {
		return errors.New("secret request requires a canonical UUID")
	}
	return nil
}
