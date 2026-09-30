package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"commander/framework/artifact"
	artifactstore "commander/framework/artifact/store"
	"commander/framework/id"
	"commander/providers/s3/store"
)

type ArtifactFileS3Store struct{ store *store.S3Store }

var _ artifactstore.ArtifactFileStore = (*ArtifactFileS3Store)(nil)

func NewArtifactFileS3Store(storage *store.S3Store) (*ArtifactFileS3Store, error) {
	if storage == nil {
		return nil, errors.New("artifact file store requires S3 storage")
	}
	return &ArtifactFileS3Store{store: storage}, nil
}

func (s *ArtifactFileS3Store) Put(ctx context.Context, artifactID string, source artifact.FileSource) (artifact.ArtifactFile, error) {
	if err := validateContextAndID(ctx, artifactID); err != nil {
		return artifact.ArtifactFile{}, err
	}
	if err := source.Validate(); err != nil {
		return artifact.ArtifactFile{}, err
	}
	if source.MediaType == "" {
		source.MediaType = "application/octet-stream"
	}
	var reader io.ReadSeeker = bytes.NewReader(source.Data)
	if source.Path != "" {
		file, err := openRegular(source.Path)
		if err != nil {
			return artifact.ArtifactFile{}, err
		}
		defer file.Close()
		reader = file
	}
	digest := sha256.New()
	size, err := io.Copy(digest, contextReader{ctx: ctx, reader: reader})
	if err != nil {
		return artifact.ArtifactFile{}, err
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return artifact.ArtifactFile{}, errors.New("could not rewind artifact source")
	}
	file := artifact.ArtifactFile{
		ID: id.ArtifactFileID(), Name: source.Name, MediaType: source.MediaType,
		Size: size, SHA256: hex.EncodeToString(digest.Sum(nil)),
	}
	if err := s.store.Put(ctx, fileKey(artifactID, file.ID), reader, file.Size, file.MediaType, file.SHA256); err != nil {
		return artifact.ArtifactFile{}, err
	}
	return file, nil
}

func (s *ArtifactFileS3Store) Get(ctx context.Context, artifactID string, file artifact.ArtifactFile) ([]byte, error) {
	if err := validateContextAndID(ctx, artifactID); err != nil {
		return nil, err
	}
	if err := file.Validate(); err != nil {
		return nil, err
	}
	data, err := s.store.GetExact(ctx, fileKey(artifactID, file.ID), file.Size)
	if errors.Is(err, store.ErrObjectNotFound) {
		return nil, artifact.ErrUnknownArtifact
	}
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != file.SHA256 {
		return nil, errors.New("artifact file content failed its integrity check")
	}
	return data, nil
}

func fileKey(artifactID, fileID string) string {
	return "artifacts/" + artifactID + "/" + fileID
}

func validateContextAndID(ctx context.Context, artifactID string) error {
	if ctx == nil {
		return errors.New("artifact storage requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !id.Valid(artifactID) {
		return errors.New("artifact ID must be a canonical UUID")
	}
	return nil
}

func openRegular(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, errors.New("could not inspect artifact source file")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("artifact files must be regular files, not symlinks or directories")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("could not open artifact source file")
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		file.Close()
		return nil, errors.New("artifact file changed while opening")
	}
	return file, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
