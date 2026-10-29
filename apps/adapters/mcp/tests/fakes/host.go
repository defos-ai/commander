package fakes

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type BearerTransport struct {
	Token string
	Next  http.RoundTripper
}

func (transport BearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "bEaReR "+transport.Token)
	return transport.Next.RoundTrip(request)
}

type EventWaitStore struct {
	operations.OperationStore
	reads   atomic.Int32
	waiting chan struct{}
}

func NewEventWaitStore(store operations.OperationStore) *EventWaitStore {
	return &EventWaitStore{OperationStore: store, waiting: make(chan struct{})}
}

func (s *EventWaitStore) Waiting() <-chan struct{} { return s.waiting }

func (s *EventWaitStore) Get(ctx context.Context, operationID string) (operations.Operation, error) {
	operation, err := s.OperationStore.Get(ctx, operationID)
	if s.reads.Add(1) == 2 {
		close(s.waiting)
	}
	return operation, err
}

type FailedMissionCreate struct {
	missions.MissionStore
	err       error
	attempts  atomic.Int32
	remaining atomic.Int64
}

func NewFailedMissionCreate(err error) *FailedMissionCreate {
	return &FailedMissionCreate{err: err}
}

func (store *FailedMissionCreate) Calls() (int32, time.Duration) {
	return store.attempts.Load(), time.Duration(store.remaining.Load())
}

func (store *FailedMissionCreate) Create(ctx context.Context, _ missions.Mission) error {
	store.attempts.Add(1)
	deadline, _ := ctx.Deadline()
	store.remaining.Store(int64(time.Until(deadline)))
	return store.err
}

type FailedMissionUpdate struct {
	missions.MissionStore
	readErr, writeErr error
	reads, writes     atomic.Int32
	remaining         atomic.Int64
}

func NewFailedMissionUpdate(store missions.MissionStore, readErr, writeErr error) *FailedMissionUpdate {
	return &FailedMissionUpdate{MissionStore: store, readErr: readErr, writeErr: writeErr}
}

func (store *FailedMissionUpdate) Get(ctx context.Context, name string) (missions.Mission, error) {
	store.reads.Add(1)
	deadline, _ := ctx.Deadline()
	store.remaining.Store(int64(time.Until(deadline)))
	if store.readErr != nil {
		return missions.Mission{}, store.readErr
	}
	return store.MissionStore.Get(ctx, name)
}

func (store *FailedMissionUpdate) Upsert(ctx context.Context, _ missions.Mission) error {
	store.writes.Add(1)
	deadline, _ := ctx.Deadline()
	store.remaining.Store(int64(time.Until(deadline)))
	return store.writeErr
}

func (store *FailedMissionUpdate) Calls() (int32, int32, time.Duration) {
	return store.reads.Load(), store.writes.Load(), time.Duration(store.remaining.Load())
}
