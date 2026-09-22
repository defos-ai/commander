package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"commander/framework/artifact"
	artifactstore "commander/framework/artifact/store"
	"commander/framework/id"
	"commander/providers/internal/jsonfile"
)

// ArtifactFsStore stores state/agent/<agent ID>/artifact.json manifests and the
// corresponding artifact/<artifact ID> content files. Display names never form
// filesystem paths. Construction and reads do not create files or directories.
type ArtifactFsStore struct {
	root    string
	agentID string
}

var _ artifactstore.ArtifactStore = (*ArtifactFsStore)(nil)

func NewArtifactFsStore(stateDir string) (*ArtifactFsStore, error) {
	if stateDir == "" {
		stateDir = "state"
	}
	absolute, err := filepath.Abs(stateDir)
	if err != nil {
		return nil, fmt.Errorf("artifact state root: %w", err)
	}
	return &ArtifactFsStore{root: absolute}, nil
}

func (s *ArtifactFsStore) Root() string { return s.root }

// ForAgent restricts reads and writes to agentID without creating storage.
// An already bound store cannot be rebound to another agent.
func (s *ArtifactFsStore) ForAgent(agentID string) (*ArtifactFsStore, error) {
	if !id.ValidAgent(agentID) {
		return nil, errors.New("invalid artifact agent ID")
	}
	if s.agentID != "" && s.agentID != agentID {
		return nil, errors.New("artifact store is bound to another agent")
	}
	return &ArtifactFsStore{root: s.root, agentID: agentID}, nil
}

func (s *ArtifactFsStore) Put(ctx context.Context, a artifact.Artifact, owner artifact.Owner) (artifact.Ref, error) {
	if err := ctx.Err(); err != nil {
		return artifact.Ref{}, err
	}
	if err := a.Validate(); err != nil {
		return artifact.Ref{}, err
	}
	if err := owner.Validate(); err != nil {
		return artifact.Ref{}, err
	}
	if s.agentID != "" && owner.AgentID != s.agentID {
		return artifact.Ref{}, errors.New("artifact owner does not match the store's agent")
	}
	if a.MediaType == "" {
		a.MediaType = "application/octet-stream"
	} else if strings.TrimSpace(a.MediaType) == "" {
		return artifact.Ref{}, errors.New("artifact media type must not be blank")
	}
	var source io.Reader = bytes.NewReader(a.Data)
	if a.Path != "" {
		file, err := openRegular(a.Path)
		if err != nil {
			return artifact.Ref{}, err
		}
		defer file.Close()
		source = file
	}
	directory, err := agentDirectory(s.root, owner.AgentID, true)
	if err != nil {
		return artifact.Ref{}, err
	}
	contentDirectory := filepath.Join(directory, "artifact")
	if err := realDirectory(contentDirectory, true); err != nil {
		return artifact.Ref{}, err
	}
	manifestPath := filepath.Join(directory, "artifact.json")
	manifest, err := jsonfile.NewContext(ctx, manifestPath)
	if err != nil {
		return artifact.Ref{}, err
	}
	var reference artifact.Ref
	var contentPath string
	err = manifest.Update(ctx, func(data []byte) ([]byte, error) {
		references, err := decodeManifest(data, owner.AgentID)
		if err != nil {
			return nil, err
		}
		reference, err = publishContent(ctx, contentDirectory, a, owner, source)
		if err != nil {
			return nil, err
		}
		contentPath = filepath.Join(contentDirectory, reference.ID)
		encoded, err := json.MarshalIndent(append(references, reference), "", "  ")
		return append(encoded, '\n'), err
	})
	if err != nil {
		if contentPath != "" {
			// A directory-sync error can occur after the manifest rename committed.
			// Never remove content still referenced by that committed snapshot.
			data, readErr := jsonfile.Read(context.Background(), manifestPath)
			if readErr == nil {
				references, decodeErr := decodeManifest(data, owner.AgentID)
				indexed := false
				for _, current := range references {
					indexed = indexed || current.ID == reference.ID
				}
				if decodeErr == nil && !indexed {
					_ = os.Remove(contentPath)
				}
			} else if errors.Is(readErr, os.ErrNotExist) {
				_ = os.Remove(contentPath)
			}
		}
		return artifact.Ref{}, err
	}
	return reference, nil
}

func publishContent(ctx context.Context, directory string, a artifact.Artifact, owner artifact.Owner, source io.Reader) (artifact.Ref, error) {
	output, err := os.CreateTemp(directory, ".pending-*")
	if err != nil {
		return artifact.Ref{}, err
	}
	defer os.Remove(output.Name())
	defer output.Close()
	if err := output.Chmod(0600); err != nil {
		return artifact.Ref{}, err
	}
	digest := sha256.New()
	size, err := io.CopyBuffer(io.MultiWriter(output, digest), contextReader{ctx, source}, make([]byte, 64*1024))
	if err != nil {
		return artifact.Ref{}, err
	}
	if err := output.Sync(); err != nil {
		return artifact.Ref{}, err
	}
	if err := output.Close(); err != nil {
		return artifact.Ref{}, err
	}
	reference := artifact.Ref{ID: id.ArtifactID(), Name: a.Name, MediaType: a.MediaType,
		Size: size, SHA256: hex.EncodeToString(digest.Sum(nil)),
		AgentID: owner.AgentID, ActionName: owner.ActionName, CommandID: owner.CommandID, CreatedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return artifact.Ref{}, err
	}
	path := filepath.Join(directory, reference.ID)
	if err := os.Rename(output.Name(), path); err != nil {
		return artifact.Ref{}, err
	}
	if err := syncDirectory(directory); err != nil {
		_ = os.Remove(path)
		return artifact.Ref{}, err
	}
	return reference, nil
}

func agentDirectory(root, agentID string, create bool) (string, error) {
	if !id.ValidAgent(agentID) {
		return "", errors.New("invalid artifact agent ID")
	}
	paths := []string{root, filepath.Join(root, "agent"), filepath.Join(root, "agent", agentID)}
	for _, path := range paths {
		if err := realDirectory(path, create); err != nil {
			return "", err
		}
	}
	return paths[len(paths)-1], nil
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

func decodeManifest(data []byte, agentID string) ([]artifact.Ref, error) {
	var references []artifact.Ref
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&references); err != nil {
		return nil, fmt.Errorf("artifact manifest: %w", err)
	}
	if references == nil {
		return nil, errors.New("artifact manifest must be a JSON array")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("artifact manifest must contain one JSON array")
	}
	seen := make(map[string]bool, len(references))
	for _, reference := range references {
		if err := reference.Validate(); err != nil {
			return nil, fmt.Errorf("artifact manifest: %w", err)
		}
		if reference.AgentID != agentID || seen[reference.ID] {
			return nil, errors.New("artifact manifest has mismatched ownership or duplicate IDs")
		}
		seen[reference.ID] = true
	}
	return references, nil
}

// Stat checks that the entire supplied reference matches its agent's manifest.
func (s *ArtifactFsStore) Stat(ctx context.Context, reference artifact.Ref) (artifact.Ref, error) {
	if err := ctx.Err(); err != nil {
		return artifact.Ref{}, err
	}
	if err := reference.Validate(); err != nil {
		return artifact.Ref{}, err
	}
	if s.agentID != "" && reference.AgentID != s.agentID {
		return artifact.Ref{}, errors.New("artifact reference does not match the store's agent")
	}
	directory, err := agentDirectory(s.root, reference.AgentID, false)
	if err != nil {
		return artifact.Ref{}, err
	}
	data, err := jsonfile.Read(ctx, filepath.Join(directory, "artifact.json"))
	if err != nil {
		return artifact.Ref{}, err
	}
	references, err := decodeManifest(data, reference.AgentID)
	if err != nil {
		return artifact.Ref{}, err
	}
	for _, stored := range references {
		if stored.ID == reference.ID {
			if !stored.CreatedAt.Equal(reference.CreatedAt) {
				return artifact.Ref{}, errors.New("artifact reference does not match its manifest")
			}
			reference.CreatedAt = stored.CreatedAt
			if stored != reference {
				return artifact.Ref{}, errors.New("artifact reference does not match its manifest")
			}
			return stored, nil
		}
	}
	return artifact.Ref{}, fmt.Errorf("artifact is absent from its manifest: %w", os.ErrNotExist)
}

func (s *ArtifactFsStore) Get(ctx context.Context, reference artifact.Ref) ([]byte, error) {
	reference, err := s.Stat(ctx, reference)
	if err != nil {
		return nil, err
	}
	directory, err := agentDirectory(s.root, reference.AgentID, false)
	if err != nil {
		return nil, err
	}
	contentDirectory := filepath.Join(directory, "artifact")
	if err := realDirectory(contentDirectory, false); err != nil {
		return nil, err
	}
	data, err := readFile(ctx, filepath.Join(contentDirectory, reference.ID))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != reference.Size || hex.EncodeToString(digest[:]) != reference.SHA256 {
		return nil, errors.New("artifact content failed its integrity check")
	}
	return data, nil
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
