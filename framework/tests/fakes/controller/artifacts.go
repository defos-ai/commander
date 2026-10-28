package fakes

import (
	"bytes"
	"context"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
)

type ArtifactCatalog struct {
	records map[string]artifacts.Artifact
	failID  string
	failure error
	writes  int
}

func (s *ArtifactCatalog) Create(_ context.Context, record artifacts.Artifact) error {
	s.writes++
	if record.ID == s.failID && s.failure != nil {
		return s.failure
	}
	if previous, found := s.records[record.ID]; found && !previous.Equal(record) {
		return artifacts.ErrArtifactConflict
	}
	s.records[record.ID] = record.Clone()
	return nil
}

func (s *ArtifactCatalog) Get(_ context.Context, artifactID string) (artifacts.Artifact, error) {
	record, found := s.records[artifactID]
	if !found {
		return artifacts.Artifact{}, artifacts.ErrUnknownArtifact
	}
	return record.Clone(), nil
}

type ArtifactExecutions struct {
	executions.ExecutionStore
	record      executions.Execution
	completions int
}

func (s *ArtifactExecutions) Get(_ context.Context, owner, commandID string) (executions.Execution, error) {
	if owner != s.record.Command.AgentID || commandID != s.record.Command.ID {
		return executions.Execution{}, executions.ErrUnknownExecution
	}
	cloned := s.record
	cloned.Command.Payload = bytes.Clone(cloned.Command.Payload)
	if cloned.Result != nil {
		result := *cloned.Result
		result.Payload = bytes.Clone(result.Payload)
		cloned.Result = &result
		completed := *cloned.CompletedAt
		cloned.CompletedAt = &completed
	}
	return cloned, nil
}

func (s *ArtifactExecutions) Complete(_ context.Context, result messages.Message) error {
	s.completions++
	if s.record.Result != nil {
		if s.record.Result.ID != result.ID || !bytes.Equal(s.record.Result.Payload, result.Payload) {
			return executions.ErrExecutionConflict
		}
		return nil
	}
	result.Payload = bytes.Clone(result.Payload)
	completed := time.Now().UTC()
	candidate := s.record
	candidate.Result, candidate.CompletedAt = &result, &completed
	if err := candidate.Validate(); err != nil {
		return err
	}
	s.record = candidate
	return nil
}

func NewArtifactCatalog() *ArtifactCatalog {
	return &ArtifactCatalog{records: make(map[string]artifacts.Artifact)}
}
func (s *ArtifactCatalog) FailOn(id string, err error) { s.failID, s.failure = id, err }
func (s *ArtifactCatalog) Fail(err error)              { s.failure = err }
func (s *ArtifactCatalog) Snapshot() map[string]artifacts.Artifact {
	copy := make(map[string]artifacts.Artifact, len(s.records))
	for id, record := range s.records {
		copy[id] = record.Clone()
	}
	return copy
}
func (s *ArtifactCatalog) Writes() int { return s.writes }
func NewArtifactExecutions(record executions.Execution) *ArtifactExecutions {
	return &ArtifactExecutions{record: record}
}
func (s *ArtifactExecutions) Snapshot() executions.Execution {
	copy, _ := s.Get(context.Background(), s.record.Command.AgentID, s.record.Command.ID)
	return copy
}
func (s *ArtifactExecutions) Completions() int { return s.completions }
