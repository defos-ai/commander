package artifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"commander/framework/domain/artifacts"
	"commander/framework/id"
	"commander/providers/fs/internal/jsonfile"
)

type ArtifactFileFsStore struct{ root string }

var _ artifacts.ArtifactFileStore = (*ArtifactFileFsStore)(nil)

func NewArtifactFileFsStore(root string) (*ArtifactFileFsStore, error) {
	if root == "" {
		root = filepath.Join("state", "storage")
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("artifact storage root must not be blank")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("artifact storage root: %w", err)
	}
	return &ArtifactFileFsStore{root: absolute}, nil
}

func (s *ArtifactFileFsStore) Put(ctx context.Context, artifactID string, source artifacts.FileSource) (artifacts.ArtifactFile, error) {
	if err := checkContext(ctx); err != nil {
		return artifacts.ArtifactFile{}, err
	}
	if err := source.Validate(); err != nil {
		return artifacts.ArtifactFile{}, err
	}
	if source.MediaType == "" {
		source.MediaType = "application/octet-stream"
	}
	var reader io.Reader = bytes.NewReader(source.Data)
	if source.Path != "" {
		input, err := openRegular(source.Path)
		if err != nil {
			return artifacts.ArtifactFile{}, err
		}
		defer input.Close()
		reader = input
	}
	directory, err := s.directory(artifactID, true)
	if err != nil {
		return artifacts.ArtifactFile{}, err
	}
	if err := jsonfile.PrepareDirectory(directory); err != nil {
		return artifacts.ArtifactFile{}, err
	}
	output, err := os.CreateTemp(directory, ".pending-*")
	if err != nil {
		return artifacts.ArtifactFile{}, err
	}
	defer os.Remove(output.Name())
	defer output.Close()
	if err := output.Chmod(0600); err != nil {
		return artifacts.ArtifactFile{}, err
	}
	digest := sha256.New()
	size, err := io.CopyBuffer(io.MultiWriter(output, digest), contextReader{ctx, reader}, make([]byte, 64*1024))
	if err != nil {
		return artifacts.ArtifactFile{}, err
	}
	if err := output.Sync(); err != nil {
		return artifacts.ArtifactFile{}, err
	}
	if err := output.Close(); err != nil {
		return artifacts.ArtifactFile{}, err
	}
	file := artifacts.ArtifactFile{ID: id.ArtifactFileID(), Name: source.Name,
		MediaType: source.MediaType, Size: size, SHA256: hex.EncodeToString(digest.Sum(nil))}
	if err := ctx.Err(); err != nil {
		return artifacts.ArtifactFile{}, err
	}
	destination := filepath.Join(directory, file.ID)
	// Publishing a completed file must never replace another artifact's bytes.
	if err := os.Link(output.Name(), destination); err != nil {
		return artifacts.ArtifactFile{}, err
	}
	if err := syncDirectory(directory); err != nil {
		_ = os.Remove(destination)
		return artifacts.ArtifactFile{}, err
	}
	return file, nil
}

func (s *ArtifactFileFsStore) Get(ctx context.Context, artifactID string, file artifacts.ArtifactFile) ([]byte, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if err := file.Validate(); err != nil {
		return nil, err
	}
	directory, err := s.directory(artifactID, false)
	if err != nil {
		return nil, err
	}
	data, err := readFile(ctx, filepath.Join(directory, file.ID))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != file.Size || hex.EncodeToString(digest[:]) != file.SHA256 {
		return nil, errors.New("artifact file content failed its integrity check")
	}
	return data, nil
}

func (s *ArtifactFileFsStore) directory(artifactID string, create bool) (string, error) {
	if !id.Valid(artifactID) {
		return "", errors.New("artifact ID must be a canonical UUID")
	}
	directory := filepath.Join(s.root, "artifacts", artifactID)
	for _, path := range []string{s.root, filepath.Join(s.root, "artifacts"), directory} {
		if err := realDirectory(path, create); err != nil {
			return "", err
		}
	}
	return directory, nil
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("artifact storage requires a context")
	}
	return ctx.Err()
}

func realDirectory(path string, create bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("artifact directories must be real directories")
	}
	return nil
}

func openRegular(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("artifact files must be regular files, not symlinks or directories")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err == nil && (!opened.Mode().IsRegular() || !os.SameFile(info, opened)) {
		err = errors.New("artifact file changed while opening")
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func readFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(contextReader{ctx, file})
	if err == nil {
		err = ctx.Err()
	}
	return data, err
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
