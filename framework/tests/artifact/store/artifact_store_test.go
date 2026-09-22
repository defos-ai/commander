package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"commander/framework/artifact"
	"commander/framework/artifact/store"
	fsartifact "commander/providers/fs/artifact"
)

const (
	agentID = "web-quality-agent-a5452d64"
	otherID = "1d477a20-2af1-4c2b-b92a-1729a1c32f13"
)

func openStore(t *testing.T, root string) *fsartifact.ArtifactFsStore {
	t.Helper()
	s, err := fsartifact.NewArtifactFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Exercise the storage contract without depending on manifest or content paths.
func TestArtifactsCanBeReadAfterReopeningStorage(t *testing.T) {
	for _, source := range []string{"bytes", "file", "empty"} {
		t.Run(source, func(t *testing.T) {
			// Arrange
			root := filepath.Join(t.TempDir(), "storage")
			var storage store.ArtifactStore = openStore(t, root)
			data := []byte("evidence with UTF-8: Київ")
			if source == "empty" {
				data = []byte{}
			}
			input := artifact.Artifact{Name: "evidence.txt", MediaType: "text/plain", Data: data}
			if source == "file" {
				input.Path = filepath.Join(t.TempDir(), "source.txt")
				input.Data = nil
				if err := os.WriteFile(input.Path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			owner := artifact.Owner{AgentID: agentID, ActionName: "capture", CommandID: otherID}

			// Act
			ref, err := storage.Put(t.Context(), input, owner)
			if err != nil {
				t.Fatal(err)
			}
			got, readErr := openStore(t, root).Get(t.Context(), ref)

			// Assert
			if readErr != nil || !bytes.Equal(got, data) {
				t.Fatalf("reopened artifact = %q, %v; want %q", got, readErr, data)
			}
			digest := sha256.Sum256(data)
			if err := ref.Validate(); err != nil || ref.Name != input.Name || ref.MediaType != input.MediaType ||
				ref.AgentID != owner.AgentID || ref.ActionName != owner.ActionName || ref.CommandID != owner.CommandID ||
				ref.Size != int64(len(data)) || ref.SHA256 != hex.EncodeToString(digest[:]) {
				t.Fatalf("artifact reference lost metadata: %+v, %v", ref, err)
			}
		})
	}
}

func TestAgentStoreRejectsForeignOwnership(t *testing.T) {
	// Arrange
	storage := openStore(t, filepath.Join(t.TempDir(), "storage"))
	bound, err := storage.ForAgent(agentID)
	if err != nil {
		t.Fatal(err)
	}
	input := artifact.Artifact{Name: "evidence.txt", Data: []byte("private")}
	foreignOwner := artifact.Owner{AgentID: "other-agent-1d477a20", ActionName: "capture"}
	ref, err := storage.Put(t.Context(), input, foreignOwner)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	_, writeErr := bound.Put(t.Context(), input, foreignOwner)
	data, readErr := bound.Get(t.Context(), ref)
	_, rebindErr := bound.ForAgent("other-agent-1d477a20")
	ownRef, ownWriteErr := bound.Put(t.Context(), input, artifact.Owner{AgentID: agentID, ActionName: "capture"})
	if ownWriteErr != nil {
		t.Fatal(ownWriteErr)
	}
	ownData, ownReadErr := bound.Get(t.Context(), ownRef)

	// Assert
	if writeErr == nil || readErr == nil || data != nil || rebindErr == nil {
		t.Fatalf("foreign ownership accepted: write=%v read=%v rebind=%v", writeErr, readErr, rebindErr)
	}
	if ownReadErr != nil || !bytes.Equal(ownData, input.Data) {
		t.Fatalf("own artifact = %q, %v", ownData, ownReadErr)
	}
}

func TestModifiedReferencesCannotReadArtifacts(t *testing.T) {
	// Arrange
	storage := openStore(t, t.TempDir())
	ref, err := storage.Put(t.Context(), artifact.Artifact{Name: "evidence.txt", Data: []byte("private")},
		artifact.Owner{AgentID: agentID, ActionName: "capture"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"name", "size", "owner", "id"} {
		t.Run(field, func(t *testing.T) {
			// Arrange
			modified := ref
			switch field {
			case "name":
				modified.Name = "renamed.txt"
			case "size":
				modified.Size++
			case "owner":
				modified.AgentID = otherID
			case "id":
				modified.ID = "../private"
			}

			// Act
			data, err := storage.Get(t.Context(), modified)

			// Assert
			if err == nil || data != nil {
				t.Fatalf("modified reference exposed content: %q, %v", data, err)
			}
		})
	}
}

func TestCancelledArtifactWriteDoesNotCreateStorage(t *testing.T) {
	// Arrange
	root := filepath.Join(t.TempDir(), "storage")
	storage := openStore(t, root)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// Act
	_, err := storage.Put(ctx, artifact.Artifact{Name: "evidence.txt", Data: []byte("unsaved")},
		artifact.Owner{AgentID: agentID, ActionName: "capture"})
	_, statErr := os.Stat(root)

	// Assert
	if !errors.Is(err, context.Canceled) || !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("cancelled write: %v; storage existence: %v", err, statErr)
	}
}
