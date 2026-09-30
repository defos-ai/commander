package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"commander/framework/artifact"
	artifactstore "commander/framework/artifact/store"
	"commander/framework/id"
	"commander/providers/fs/internal/jsonfile"
)

type ArtifactFsStore struct{ root string }

var _ artifactstore.ArtifactStore = (*ArtifactFsStore)(nil)
var errAlreadyCreated = errors.New("artifact is already stored")

func NewArtifactFsStore(root string) (*ArtifactFsStore, error) {
	if root == "" {
		root = filepath.Join("state", "database")
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("artifact database root must not be blank")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &ArtifactFsStore{root: absolute}, nil
}

func (s *ArtifactFsStore) Create(ctx context.Context, value artifact.Artifact) error {
	path, err := s.path(ctx, value.ID)
	if err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal([]artifact.Artifact{value})
	if err != nil {
		return err
	}
	if err := jsonfile.PrepareDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	record, err := jsonfile.OpenContext(ctx, path)
	if err != nil {
		return err
	}
	err = record.Update(ctx, func(data []byte) ([]byte, error) {
		stored, err := decode(data, value.ID)
		if errors.Is(err, artifact.ErrUnknownArtifact) {
			return encoded, nil
		}
		if err != nil {
			return nil, err
		}
		if !stored.Equal(value) {
			return nil, artifact.ErrArtifactConflict
		}
		return nil, errAlreadyCreated
	})
	if errors.Is(err, errAlreadyCreated) {
		return nil
	}
	return err
}

func (s *ArtifactFsStore) Get(ctx context.Context, artifactID string) (artifact.Artifact, error) {
	path, err := s.path(ctx, artifactID)
	if err != nil {
		return artifact.Artifact{}, err
	}
	data, err := jsonfile.Read(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return artifact.Artifact{}, artifact.ErrUnknownArtifact
	}
	if err != nil {
		return artifact.Artifact{}, err
	}
	return decode(data, artifactID)
}

func (s *ArtifactFsStore) path(ctx context.Context, artifactID string) (string, error) {
	if ctx == nil {
		return "", errors.New("artifact database requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !id.Valid(artifactID) {
		return "", errors.New("artifact ID must be a canonical UUID")
	}
	return filepath.Join(s.root, "artifacts", artifactID+".json"), nil
}

func decode(data []byte, artifactID string) (artifact.Artifact, error) {
	var records []artifact.Artifact
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&records); err != nil {
		return artifact.Artifact{}, fmt.Errorf("artifact record: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return artifact.Artifact{}, errors.New("artifact record must contain one JSON array")
	}
	if records == nil || len(records) > 1 {
		return artifact.Artifact{}, errors.New("artifact record must contain at most one artifact")
	}
	if len(records) == 0 {
		return artifact.Artifact{}, artifact.ErrUnknownArtifact
	}
	value := records[0]
	if value.ID != artifactID {
		return artifact.Artifact{}, errors.New("stored artifact ID does not match its record")
	}
	if err := value.Validate(); err != nil {
		return artifact.Artifact{}, fmt.Errorf("invalid stored artifact: %w", err)
	}
	return value, nil
}
