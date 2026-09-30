package providers_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"commander/framework/domain/artifacts"
	"commander/framework/id"
	filefs "commander/providers/fs/artifact"
	artifactfs "commander/providers/fs/controller/artifacts"
)

func TestArtifactFsStore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "database")
	first, err := artifactfs.NewArtifactFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := artifactfs.NewArtifactFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	value := artifactFixture()
	if _, err := first.Get(t.Context(), value.ID); !errors.Is(err, artifacts.ErrUnknownArtifact) {
		t.Fatalf("unknown artifact: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read created database state: %v", err)
	}

	if err := first.Create(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "artifacts", value.ID+".json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	replay := value.Clone()
	replay.CreatedAt = value.CreatedAt.In(time.FixedZone("other", 3600))
	if err := second.Create(t.Context(), replay); err != nil {
		t.Fatalf("identical replay: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("replay replaced immutable metadata: %v", err)
	}
	changed := value.Clone()
	changed.Files[0].Name = "changed.png"
	if err := second.Create(t.Context(), changed); !errors.Is(err, artifacts.ErrArtifactConflict) {
		t.Fatalf("changed files overwrote artifact: %v", err)
	}

	stored, err := second.Get(t.Context(), value.ID)
	if err != nil || !stored.Equal(value) {
		t.Fatalf("metadata changed on roundtrip: %+v, %v", stored, err)
	}
	stored.Files[0].Name = "changed by reader"
	value.Files[0].Name = "changed by writer"
	unchanged, err := first.Get(t.Context(), value.ID)
	if err != nil || !unchanged.Equal(replay) {
		t.Fatalf("caller mutated stored metadata: %+v, %v", unchanged, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := first.Create(cancelled, artifactFixture()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write: %v", err)
	}
	if _, err := first.Get(t.Context(), "../outside"); err == nil {
		t.Fatal("invalid artifact ID accepted")
	}
}

func TestConcurrentArtifactFsCreates(t *testing.T) {
	root := t.TempDir()
	value := artifactFixture()
	changed := value.Clone()
	changed.Name = "conflicting evidence"
	gate, results := make(chan struct{}), make(chan error, 12)

	for i := range 12 {
		store, err := artifactfs.NewArtifactFsStore(root)
		if err != nil {
			t.Fatal(err)
		}
		candidate := value
		if i%2 != 0 {
			candidate = changed
		}
		go func() {
			<-gate
			results <- store.Create(t.Context(), candidate)
		}()
	}
	close(gate)
	successes, conflicts := 0, 0
	for range 12 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, artifacts.ErrArtifactConflict):
			conflicts++
		default:
			t.Fatalf("concurrent create: %v", err)
		}
	}

	if successes != 6 || conflicts != 6 {
		t.Fatalf("conflicting creates: successes=%d, conflicts=%d", successes, conflicts)
	}
}

func TestArtifactFilesStaySeparateFromMetadata(t *testing.T) {
	root := t.TempDir()
	storage, database := filepath.Join(root, "storage"), filepath.Join(root, "database")
	files, err := filefs.NewArtifactFileFsStore(storage)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := artifactfs.NewArtifactFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("screenshot bytes")
	value := artifactFixture()
	file, err := files.Put(t.Context(), value.ID, artifacts.FileSource{Name: "../../display.png", MediaType: "image/png", Data: payload})
	if err != nil {
		t.Fatal(err)
	}
	value.Files = []artifacts.ArtifactFile{file}
	if _, err := metadata.Get(t.Context(), value.ID); !errors.Is(err, artifacts.ErrUnknownArtifact) {
		t.Fatalf("file publication created metadata: %v", err)
	}

	if err := metadata.Create(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	stored, err := metadata.Get(t.Context(), value.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := files.Get(t.Context(), stored.ID, stored.Files[0])
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("file roundtrip: %q, %v", data, err)
	}
	if _, err := files.Get(t.Context(), id.ArtifactID(), file); err == nil {
		t.Fatal("another artifact read the file")
	}
	entries, err := os.ReadDir(filepath.Join(storage, "artifacts", value.ID))
	if err != nil || len(entries) != 1 || entries[0].Name() != file.ID {
		t.Fatalf("storage contains metadata or leaked temporary files: %+v, %v", entries, err)
	}
	path := filepath.Join(storage, "artifacts", value.ID, file.ID)
	if err := os.WriteFile(path, []byte("tampered payload"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Get(t.Context(), value.ID, file); err == nil {
		t.Fatal("tampered file passed its integrity check")
	}
}

func TestArtifactFileSources(t *testing.T) {
	root := t.TempDir()
	files, err := filefs.NewArtifactFileFsStore(filepath.Join(root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	artifactID := id.ArtifactID()
	input := filepath.Join(root, "input")
	if err := os.WriteFile(input, []byte("file content"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, source := range []artifacts.FileSource{
		{Name: "empty", Data: []byte{}},
		{Name: "input", Path: input},
	} {
		file, err := files.Put(t.Context(), artifactID, source)
		if err != nil {
			t.Fatal(err)
		}
		data, err := files.Get(t.Context(), artifactID, file)
		if err != nil || int64(len(data)) != file.Size || file.MediaType != "application/octet-stream" {
			t.Fatalf("source roundtrip: %+v, %v", file, err)
		}
	}
	for _, source := range []artifacts.FileSource{
		{Name: "no source"},
		{Name: "two sources", Data: []byte{}, Path: input},
		{Name: "directory", Path: root},
	} {
		if _, err := files.Put(t.Context(), artifactID, source); err == nil {
			t.Fatalf("invalid source accepted: %+v", source)
		}
	}
	link := filepath.Join(root, "symlink")
	if err := os.Symlink(input, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := files.Put(t.Context(), artifactID, artifacts.FileSource{Name: "symlink", Path: link}); err == nil {
		t.Fatal("symlink source accepted")
	}
	file, err := files.Put(t.Context(), artifactID, artifacts.FileSource{Name: "regular", Path: input})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "storage", "artifacts", artifactID, file.ID)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(input, path); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Get(t.Context(), artifactID, file); err == nil {
		t.Fatal("symlink content accepted")
	}
}

func artifactFixture() artifacts.Artifact {
	digest := sha256.Sum256([]byte("data"))
	return artifacts.Artifact{ID: id.ArtifactID(), Name: "page screenshots", AgentID: "web-quality-agent-1234abcd",
		ActionName: "capture-page", CommandID: id.MessageID(), CreatedAt: time.Now().UTC(),
		Files: []artifacts.ArtifactFile{{ID: id.ArtifactFileID(), Name: "desktop.png", MediaType: "image/png", Size: 4, SHA256: hex.EncodeToString(digest[:])}}}
}
