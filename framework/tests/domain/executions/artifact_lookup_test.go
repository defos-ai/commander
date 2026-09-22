package executions_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"commander/framework/artifact"
	"commander/framework/comms/messages"
	actionmodel "commander/framework/domain/actions"
	"commander/framework/domain/executions"
	"commander/framework/id"
	artifactprovider "commander/providers/artifact"
	fsstore "commander/providers/fs/controller/executions"
)

func TestReportedArtifactsRemainReadableAfterReopeningDatabase(t *testing.T) {
	// Arrange
	root, request := t.TempDir(), command(t)
	store := openStore(t, filepath.Join(root, "database"))
	storage, err := artifactprovider.OpenStorage(t.Context(), filepath.Join(root, "storage"), artifactprovider.StorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	content := []byte("a completed action's artifact")
	ref, err := storage.ArtifactStore.Put(t.Context(), artifact.Artifact{Name: "result.txt", MediaType: "text/plain", Data: content}, artifact.Owner{
		AgentID: request.AgentID, CommandID: request.ID, ActionName: "example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Claim(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	result := artifactResult(t, request, actionmodel.ActionOutput{ActionName: "example", Output: json.RawMessage(`{}`), Artifacts: []artifact.Ref{ref}})
	if err := store.Complete(t.Context(), result); err != nil {
		t.Fatal(err)
	}

	// Act
	reopened := openStore(t, filepath.Join(root, "database"))
	found, findErr := reopened.FindArtifact(t.Context(), ref.ID)
	bytes, readErr := storage.ArtifactStore.Get(t.Context(), found)

	// Assert
	if findErr != nil || readErr != nil || found != ref || string(bytes) != string(content) {
		t.Fatalf("artifact after restart: reference=%+v lookup=%v read=%v content=%q", found, findErr, readErr, bytes)
	}
}

func TestArtifactBytesAndDispatchClaimDoNotAuthorizeReads(t *testing.T) {
	// Arrange
	root, request := t.TempDir(), command(t)
	store := openStore(t, filepath.Join(root, "database"))
	storage, err := artifactprovider.OpenStorage(t.Context(), filepath.Join(root, "storage"), artifactprovider.StorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	ref, err := storage.ArtifactStore.Put(t.Context(), artifact.Artifact{Name: "unaccepted.txt", Data: []byte("not yet reported")}, artifact.Owner{
		AgentID: request.AgentID, CommandID: request.ID, ActionName: "example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Claim(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	failed := response(t, request, messages.CommandFailed)
	failed.Payload = json.RawMessage(`{"error_type":"execution_error","message":"action failed"}`)

	// Act
	_, pendingErr := store.FindArtifact(t.Context(), ref.ID)
	completeErr := store.Complete(t.Context(), failed)
	_, failedErr := store.FindArtifact(t.Context(), ref.ID)

	// Assert
	if completeErr != nil || !errors.Is(pendingErr, artifact.ErrUnknownArtifact) || !errors.Is(failedErr, artifact.ErrUnknownArtifact) {
		t.Fatalf("unaccepted artifact became visible: pending=%v complete=%v failed=%v", pendingErr, completeErr, failedErr)
	}
}

func TestStructuredOutputArtifactCanBeLookedUp(t *testing.T) {
	// Arrange
	store, request := openStore(t, t.TempDir()), command(t)
	ref := artifactReference(request)
	result := artifactResult(t, request, actionmodel.ActionOutput{ActionName: "example", OutputArtifact: &ref})
	completeArtifactExecution(t, store, request, result)

	// Act
	found, err := store.FindArtifact(t.Context(), ref.ID)

	// Assert
	if err != nil || found != ref {
		t.Fatalf("structured output reference: %+v, %v", found, err)
	}
}

func TestArtifactLookupRejectsCorruptedResultMetadata(t *testing.T) {
	for _, test := range []struct {
		name   string
		modify func(*actionmodel.ActionOutput)
	}{
		{"wrong owner", func(output *actionmodel.ActionOutput) { output.Artifacts[0].AgentID = otherAgentID }},
		{"wrong command", func(output *actionmodel.ActionOutput) { output.Artifacts[0].CommandID = id.New() }},
		{"wrong action", func(output *actionmodel.ActionOutput) { output.ActionName = "different" }},
		{"invalid digest", func(output *actionmodel.ActionOutput) { output.Artifacts[0].SHA256 = "bad" }},
		{"conflicting metadata", func(output *actionmodel.ActionOutput) {
			other := output.Artifacts[0]
			other.Name = "conflicting.txt"
			output.Artifacts = append(output.Artifacts, other)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, request := openStore(t, t.TempDir()), command(t)
			ref := artifactReference(request)
			output := actionmodel.ActionOutput{ActionName: "example", Output: json.RawMessage(`{}`), Artifacts: []artifact.Ref{ref}}
			test.modify(&output)
			// Complete validates the envelope; the consumer normally validates the
			// payload first. Simulate corrupt persisted data bypassing that consumer.
			result := artifactResult(t, request, output)
			completeArtifactExecution(t, store, request, result)

			// Act
			found, err := store.FindArtifact(t.Context(), ref.ID)

			// Assert
			if err == nil || errors.Is(err, artifact.ErrUnknownArtifact) || found.ID != "" {
				t.Fatalf("corrupted metadata returned a reference or hid corruption: %+v, %v", found, err)
			}
		})
	}
}

func TestArtifactLookupRejectsConflictingExecutionOwners(t *testing.T) {
	// Arrange
	store := openStore(t, t.TempDir())
	first, second := command(t), command(t)
	ref := artifactReference(first)
	other := ref
	other.CommandID = second.ID
	for _, pair := range []struct {
		request messages.Message
		ref     artifact.Ref
	}{{first, ref}, {second, other}} {
		result := artifactResult(t, pair.request, actionmodel.ActionOutput{ActionName: "example", Output: json.RawMessage(`{}`), Artifacts: []artifact.Ref{pair.ref}})
		completeArtifactExecution(t, store, pair.request, result)
	}

	// Act
	found, err := store.FindArtifact(t.Context(), ref.ID)

	// Assert
	if err == nil || found.ID != "" {
		t.Fatalf("artifact with two owners was returned: %+v, %v", found, err)
	}
}

func TestArtifactLookupValidationAndMissingReadsDoNotCreatePaths(t *testing.T) {
	// Arrange
	root := filepath.Join(t.TempDir(), "missing")
	store, err := fsstore.NewExecutionFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	// Act
	_, missingErr := store.FindArtifact(t.Context(), id.New())
	_, cancelledErr := store.FindArtifact(cancelled, id.New())
	_, artifactErr := store.FindArtifact(t.Context(), "../artifact")
	_, nilContextErr := store.FindArtifact(nil, id.New())
	_, statErr := os.Stat(root)

	// Assert
	if !errors.Is(missingErr, artifact.ErrUnknownArtifact) || !errors.Is(cancelledErr, context.Canceled) {
		t.Fatalf("missing/cancelled lookup: missing=%v cancelled=%v", missingErr, cancelledErr)
	}
	if artifactErr == nil || nilContextErr == nil || !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid lookup or unexpected filesystem mutation: artifact=%v context=%v stat=%v", artifactErr, nilContextErr, statErr)
	}
}

func TestArtifactLookupIgnoresAnUncommittedClaimCollection(t *testing.T) {
	// Arrange
	root, request := t.TempDir(), command(t)
	store := openStore(t, root)
	directory := filepath.Join(root, "executions", request.AgentID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	// FS collection creation precedes the atomic claim write. A reader may
	// observe this empty collection while a different command is being claimed.
	if err := os.WriteFile(filepath.Join(directory, request.ID+".json"), []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err := store.FindArtifact(t.Context(), id.New())

	// Assert
	if !errors.Is(err, artifact.ErrUnknownArtifact) {
		t.Fatalf("an unrelated uncommitted claim failed the lookup: %v", err)
	}
}

func artifactReference(request messages.Message) artifact.Ref {
	return artifact.Ref{
		ID: id.New(), Name: "result.json", MediaType: "application/json", Size: 2,
		SHA256: strings.Repeat("a", 64), CreatedAt: time.Now().UTC(),
		AgentID: request.AgentID, CommandID: request.ID, ActionName: "example",
	}
}

func artifactResult(t *testing.T, request messages.Message, output actionmodel.ActionOutput) messages.Message {
	t.Helper()
	result, err := messages.NewMessage(messages.ActionSucceeded, request.AgentID, output)
	if err != nil {
		t.Fatal(err)
	}
	result.CorrelationID, result.Work = request.ID, request.Work
	return result
}

func completeArtifactExecution(t *testing.T, store executions.ExecutionStore, request, result messages.Message) {
	t.Helper()
	if _, _, err := store.Claim(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(t.Context(), result); err != nil {
		t.Fatal(err)
	}
}
