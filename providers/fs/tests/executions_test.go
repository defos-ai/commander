package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
	executionfs "github.com/defos-ai/commander/providers/fs/executions"
)

func TestExecutionCheckpointsSurviveReopenAndCompletion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "database")
	store := checkpointStore(t, root)
	command, result := checkpointMessages(t)
	if _, _, err := store.Claim(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	data := json.RawMessage(`{"version":"v1","number":9007199254740993}`)
	if err := store.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "package-selected", data); err != nil {
		t.Fatal(err)
	}
	data[0] = '!'
	reopened := checkpointStore(t, root)
	saved, err := reopened.Get(t.Context(), command.AgentID, command.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved.Checkpoints["package-selected"]) != `{"version":"v1","number":9007199254740993}` {
		t.Fatalf("checkpoint changed after save: %s", saved.Checkpoints["package-selected"])
	}
	saved.Checkpoints["package-selected"][0] = '!'
	delete(saved.Checkpoints, "package-selected")

	// Key order and whitespace do not turn a retry into a conflicting write.
	retry := json.RawMessage(`{ "number": 9007199254740993, "version": "v1" }`)
	if err := reopened.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "package-selected", retry); err != nil {
		t.Fatalf("identical checkpoint retry: %v", err)
	}
	if err := reopened.Complete(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	before, err := reopened.Get(t.Context(), command.AgentID, command.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Result == nil || len(before.Checkpoints) != 1 {
		t.Fatalf("completion lost checkpoint or result: %+v", before)
	}
	if err := reopened.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "package-selected", retry); err != nil {
		t.Fatalf("completed execution rejected exact retry: %v", err)
	}
	conflict := json.RawMessage(`{"version":"v1","number":9007199254740992}`)
	if err := reopened.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "package-selected", conflict); !errors.Is(err, executions.ErrCheckpointConflict) {
		t.Fatalf("conflicting checkpoint was not rejected: %v", err)
	}
	if err := reopened.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "launch-intent", json.RawMessage(`{}`)); !errors.Is(err, executions.ErrExecutionCompleted) {
		t.Fatalf("completed execution accepted a new checkpoint: %v", err)
	}
	after, err := store.Get(t.Context(), command.AgentID, command.ID)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("retry or rejection changed saved execution: %+v, %v", after, err)
	}
}

func TestExecutionCheckpointSizeSurvivesJSONEncoding(t *testing.T) {
	root := t.TempDir()
	store := checkpointStore(t, root)
	command, result := checkpointMessages(t)
	if _, _, err := store.Claim(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	const prefix, suffix, escapable = `{"text":"`, `"}`, "<>&\u2028\u2029"
	remaining := executions.MaxCheckpointSize - len(prefix) - len(suffix)
	encodedEscapable, err := json.Marshal(escapable)
	if err != nil {
		t.Fatal(err)
	}
	encodedLength := len(encodedEscapable) - 2 // Exclude the surrounding JSON quotes.
	data := json.RawMessage(prefix + strings.Repeat(escapable, remaining/encodedLength) + strings.Repeat("x", remaining%encodedLength) + suffix)
	encoded, err := json.Marshal(data)
	if err != nil || len(encoded) != executions.MaxCheckpointSize {
		t.Fatalf("invalid boundary fixture: encoded length=%d, error=%v", len(encoded), err)
	}
	if err := store.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "selected", data); err != nil {
		t.Fatal(err)
	}
	assertSaved := func() {
		t.Helper()
		saved, err := checkpointStore(t, root).Get(t.Context(), command.AgentID, command.ID)
		if err != nil {
			t.Fatalf("accepted checkpoint became unreadable: %v", err)
		}
		savedEncoded, err := json.Marshal(saved.Checkpoints["selected"])
		if err != nil || string(savedEncoded) != string(encoded) || len(saved.Checkpoints) != 1 {
			t.Fatalf("checkpoint changed: count=%d, encoded length=%d, error=%v", len(saved.Checkpoints), len(savedEncoded), err)
		}
	}
	assertSaved()
	rawRemaining := remaining - 1
	tooLarge := json.RawMessage(prefix + strings.Repeat(escapable, rawRemaining/len(escapable)) + strings.Repeat("x", rawRemaining%len(escapable)) + suffix)
	if err := store.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "too-large", tooLarge); err == nil {
		t.Fatal("checkpoint exceeding its encoded JSON size limit was accepted")
	}
	assertSaved()
	if _, claimed, err := store.Claim(t.Context(), command); err != nil || claimed {
		t.Fatalf("claim replay: claimed=%t, error=%v", claimed, err)
	}
	assertSaved()
	for range 2 {
		if err := store.Complete(t.Context(), result); err != nil {
			t.Fatal(err)
		}
		assertSaved()
	}
	if err := store.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "selected", data); err != nil {
		t.Fatal(err)
	}
	assertSaved()
}

func TestExecutionCheckpointRejectionDoesNotCreateOrChangeState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "database")
	store := checkpointStore(t, root)
	command, _ := checkpointMessages(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.SaveCheckpoint(ctx, command.AgentID, command.ID, "selected", json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save: %v", err)
	}
	if err := store.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "selected", json.RawMessage(`{}`)); !errors.Is(err, executions.ErrUnknownExecution) {
		t.Fatalf("unclaimed save: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected saves created database paths: %v", err)
	}
	before, _, err := store.Claim(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCheckpoint(ctx, command.AgentID, command.ID, "selected", json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save on claimed execution: %v", err)
	}
	if err := store.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "selected", json.RawMessage(`invalid`)); err == nil {
		t.Fatal("invalid checkpoint JSON accepted")
	}
	after, err := store.Get(t.Context(), command.AgentID, command.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected saves changed claimed execution: %+v, %v", after, err)
	}
}

func TestExecutionCheckpointWritesAreAtomicAcrossInstances(t *testing.T) {
	root := t.TempDir()
	first, second := checkpointStore(t, root), checkpointStore(t, root)
	command, _ := checkpointMessages(t)
	if _, _, err := first.Claim(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	gate, results := make(chan struct{}), make(chan error, 8)
	for index := range 8 {
		store := []*executionfs.ExecutionFsStore{first, second}[index%2]
		go func() {
			<-gate
			results <- store.SaveCheckpoint(t.Context(), command.AgentID, command.ID, fmt.Sprintf("step-%d", index), json.RawMessage(`{}`))
		}()
	}
	close(gate)
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	saved, err := first.Get(t.Context(), command.AgentID, command.ID)
	if err != nil || len(saved.Checkpoints) != 8 {
		t.Fatalf("concurrent writes lost checkpoints: %+v, %v", saved.Checkpoints, err)
	}

	gate = make(chan struct{})
	for index, store := range []*executionfs.ExecutionFsStore{first, second} {
		go func() {
			<-gate
			results <- store.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "winner", json.RawMessage(fmt.Sprintf(`{"owner":%d}`, index)))
		}()
	}
	close(gate)
	a, b := <-results, <-results
	if !((a == nil && errors.Is(b, executions.ErrCheckpointConflict)) || (b == nil && errors.Is(a, executions.ErrCheckpointConflict))) {
		t.Fatalf("conflicting writers must have exactly one winner: %v, %v", a, b)
	}
	saved, err = first.Get(t.Context(), command.AgentID, command.ID)
	if err != nil || len(saved.Checkpoints) != 9 {
		t.Fatalf("conflicting writes changed other checkpoints: %+v, %v", saved.Checkpoints, err)
	}
}

func TestExecutionCheckpointAndCompletionSerialize(t *testing.T) {
	root := t.TempDir()
	first, second := checkpointStore(t, root), checkpointStore(t, root)
	command, result := checkpointMessages(t)
	if _, _, err := first.Claim(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	checkpoint, completion := make(chan error, 1), make(chan error, 1)
	go func() {
		<-gate
		checkpoint <- first.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "launch-intent", json.RawMessage(`{}`))
	}()
	go func() {
		<-gate
		completion <- second.Complete(t.Context(), result)
	}()
	close(gate)
	checkpointErr, completionErr := <-checkpoint, <-completion
	if completionErr != nil {
		t.Fatalf("completion failed: %v", completionErr)
	}
	if checkpointErr != nil && !errors.Is(checkpointErr, executions.ErrExecutionCompleted) {
		t.Fatalf("checkpoint failed unexpectedly: %v", checkpointErr)
	}
	saved, err := first.Get(t.Context(), command.AgentID, command.ID)
	if err != nil || saved.Result == nil {
		t.Fatalf("completion was lost: %+v, %v", saved, err)
	}
	_, exists := saved.Checkpoints["launch-intent"]
	if exists != (checkpointErr == nil) {
		t.Fatalf("checkpoint persistence contradicts acknowledgement: exists=%t, error=%v", exists, checkpointErr)
	}
}

func checkpointStore(t *testing.T, root string) *executionfs.ExecutionFsStore {
	t.Helper()
	store, err := executionfs.NewExecutionFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func checkpointMessages(t *testing.T) (messages.Message, messages.Message) {
	t.Helper()
	command, err := agentcommands.NewRunAction("test-agent-00000001", actions.ActionCall{ActionName: "inspect", Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := messages.NewMessage(messages.ActionSucceeded, command.AgentID, actions.ActionOutput{ActionName: "inspect", Output: json.RawMessage(`{"ok":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	result.CorrelationID, result.Work = command.ID, command.Work
	return command, result
}
