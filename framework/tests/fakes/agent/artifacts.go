package fakes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/id"
)

type ArtifactAction struct {
	*action.Base
	result action.Result
}

func (a *ArtifactAction) Run(context.Context, json.RawMessage) (action.Result, error) {
	return a.result, nil
}

type OutputFiles struct {
	artifactID string
	file       artifacts.ArtifactFile
	data       []byte
	failure    error
}

func (s *OutputFiles) Put(_ context.Context, artifactID string, source artifacts.FileSource) (artifacts.ArtifactFile, error) {
	if s.failure != nil {
		return artifacts.ArtifactFile{}, s.failure
	}
	digest := sha256.Sum256(source.Data)
	s.artifactID, s.data = artifactID, bytes.Clone(source.Data)
	s.file = artifacts.ArtifactFile{ID: id.ArtifactFileID(), Name: source.Name, MediaType: source.MediaType, Size: int64(len(source.Data)), SHA256: hex.EncodeToString(digest[:])}
	return s.file, nil
}
func (s *OutputFiles) Get(_ context.Context, artifactID string, file artifacts.ArtifactFile) ([]byte, error) {
	if artifactID != s.artifactID || file != s.file {
		return nil, artifacts.ErrUnknownArtifact
	}
	return bytes.Clone(s.data), nil
}

func NewArtifactAction(result action.Result) *ArtifactAction {
	return &ArtifactAction{Base: action.NewBase(actions.Action{Name: "inspect"}), result: result}
}
func (s *OutputFiles) ArtifactID() string { return s.artifactID }
func (s *OutputFiles) Fail(err error)     { s.failure = err }
