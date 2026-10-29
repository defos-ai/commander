package fakes

import (
	"context"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

type Calls struct {
	Attempts  int
	Remaining time.Duration
}

func (c *Calls) record(ctx context.Context) {
	c.Attempts++
	deadline, _ := ctx.Deadline()
	c.Remaining = time.Until(deadline)
}

type FailedStart struct {
	commands.Operations
	err   error
	calls Calls
}

func NewFailedStart(err error) *FailedStart { return &FailedStart{err: err} }

func (s *FailedStart) Start(ctx context.Context, _ commands.StartOperationCommand) (operations.Operation, error) {
	s.calls.record(ctx)
	return operations.Operation{}, s.err
}

func (s *FailedStart) Snapshot() Calls { return s.calls }

type FailedMissionCreate struct {
	missions.MissionStore
	err   error
	calls Calls
}

func NewFailedMissionCreate(err error) *FailedMissionCreate { return &FailedMissionCreate{err: err} }

func (s *FailedMissionCreate) Create(ctx context.Context, _ missions.Mission) error {
	s.calls.record(ctx)
	return s.err
}

func (s *FailedMissionCreate) Snapshot() Calls { return s.calls }

type FailedMissionUpdate struct {
	missions.MissionStore
	err   error
	calls Calls
}

func NewFailedMissionUpdate(store missions.MissionStore, err error) *FailedMissionUpdate {
	return &FailedMissionUpdate{MissionStore: store, err: err}
}

func (s *FailedMissionUpdate) Upsert(ctx context.Context, _ missions.Mission) error {
	s.calls.record(ctx)
	return s.err
}

func (s *FailedMissionUpdate) Snapshot() Calls { return s.calls }

type FailedSecretCreate struct {
	secrets.SecretStore
	err   error
	calls Calls
}

func NewFailedSecretCreate(err error) *FailedSecretCreate { return &FailedSecretCreate{err: err} }

func (s *FailedSecretCreate) Create(ctx context.Context, _ secrets.Secret) error {
	s.calls.record(ctx)
	return s.err
}

func (s *FailedSecretCreate) Snapshot() Calls { return s.calls }

type ArtifactMetadata struct {
	artifacts.ArtifactStore
	value artifacts.Artifact
}

func NewArtifactMetadata(value artifacts.Artifact) ArtifactMetadata {
	return ArtifactMetadata{value: value.Clone()}
}

func (s ArtifactMetadata) Get(context.Context, string) (artifacts.Artifact, error) {
	return s.value.Clone(), nil
}

type ArtifactBytes struct {
	artifacts.ArtifactFileStore
	err error
}

func NewArtifactBytes(err error) *ArtifactBytes { return &ArtifactBytes{err: err} }

func (s *ArtifactBytes) Get(context.Context, string, artifacts.ArtifactFile) ([]byte, error) {
	return []byte{}, s.err
}

type PendingRequests struct {
	secrets.RequestStore
	List func(context.Context) ([]secrets.RequestRecord, error)
}

func (s PendingRequests) ListPending(ctx context.Context) ([]secrets.RequestRecord, error) {
	return s.List(ctx)
}

type RequestedMetadata struct {
	secrets.SecretStore
	Lookup func(context.Context, secrets.Key) (secrets.Metadata, error)
}

func (s RequestedMetadata) GetMetadata(ctx context.Context, key secrets.Key) (secrets.Metadata, error) {
	return s.Lookup(ctx, key)
}
