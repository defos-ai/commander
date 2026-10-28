package mongo_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/framework/domain/executions"
	mongoexecutions "github.com/defos-ai/commander/providers/mongo/executions"
	"github.com/defos-ai/commander/providers/mongo/tests/fixtures"
)

func TestExecutionMongoCheckpointsPersistAndRemainImmutable(t *testing.T) {
	database := fixtures.OpenStore(t, "executions")
	catalog, err := mongoexecutions.NewExecutionMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	command := fixtures.ExecutionCommand(1)
	data := json.RawMessage(`{"serial":18446744073709551615,"steps":["a"]}`)
	original := string(data)
	if err := catalog.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "selected", data); !errors.Is(err, executions.ErrUnknownExecution) {
		t.Fatalf("unclaimed checkpoint: %v", err)
	}
	claimed, _, err := catalog.Claim(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "selected", data); err != nil {
		t.Fatal(err)
	}
	data[0] = '!'
	reopened, err := mongoexecutions.NewExecutionMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := reopened.Get(t.Context(), command.AgentID, command.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved.Checkpoints["selected"]) != original || !saved.StartedAt.Equal(claimed.StartedAt) || saved.Result != nil {
		t.Fatalf("checkpoint changed execution state or JSON: %#v", saved)
	}
	saved.Checkpoints["selected"][0] = '?'
	saved.Checkpoints["extra"] = json.RawMessage(`{}`)
	replay := json.RawMessage(`{ "steps": ["a"], "serial": 18446744073709551615 }`)
	if err := reopened.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "selected", replay); err != nil {
		t.Fatalf("semantic replay: %v", err)
	}
	if err := reopened.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "selected",
		json.RawMessage(`{"serial":18446744073709551614,"steps":["a"]}`)); !errors.Is(err, executions.ErrCheckpointConflict) {
		t.Fatalf("distinct large integer replaced checkpoint: %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := reopened.SaveCheckpoint(cancelled, command.AgentID, command.ID, "cancelled", json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled checkpoint: %v", err)
	}
	if err := reopened.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "invalid", json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid JSON was saved")
	}
	result := fixtures.ExecutionResult(command)
	if err := reopened.Complete(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	if err := reopened.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "selected", replay); err != nil {
		t.Fatalf("lost acknowledgement could not be reconciled after completion: %v", err)
	}
	if err := reopened.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "late", json.RawMessage(`{}`)); !errors.Is(err, executions.ErrExecutionCompleted) {
		t.Fatalf("new checkpoint after completion: %v", err)
	}
	restored, err := reopened.Get(t.Context(), command.AgentID, command.ID)
	if err != nil || len(restored.Checkpoints) != 1 || string(restored.Checkpoints["selected"]) != original ||
		restored.Result == nil || !reflect.DeepEqual(*restored.Result, result) {
		t.Fatalf("checkpoint replay or completion changed saved data: %#v, error=%v", restored, err)
	}
}

func TestExecutionMongoConcurrentCheckpoints(t *testing.T) {
	first, err := mongoexecutions.NewExecutionMongoStore(t.Context(), fixtures.OpenStore(t, "executions"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := mongoexecutions.NewExecutionMongoStore(t.Context(), fixtures.OpenStore(t, "executions"))
	if err != nil {
		t.Fatal(err)
	}
	stores := []executions.ExecutionStore{first, second}
	command := fixtures.ExecutionCommand(1)
	if _, _, err := first.Claim(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	const writers = 8
	finished := make(chan error, writers)
	start := make(chan struct{})
	for i := range writers {
		go func() {
			<-start
			finished <- stores[i%2].SaveCheckpoint(t.Context(), command.AgentID, command.ID,
				fmt.Sprintf("stage-%d", i), json.RawMessage(fmt.Sprintf(`{"index":%d}`, i)))
		}()
	}
	close(start)
	for range writers {
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}
	if t.Failed() {
		return
	}
	saved, err := first.Get(t.Context(), command.AgentID, command.ID)
	if err != nil || len(saved.Checkpoints) != writers {
		t.Fatalf("concurrent writes lost checkpoints: %#v, error=%v", saved.Checkpoints, err)
	}
	for i := range writers {
		if string(saved.Checkpoints[fmt.Sprintf("stage-%d", i)]) != fmt.Sprintf(`{"index":%d}`, i) {
			t.Fatalf("checkpoint %d changed: %#v", i, saved.Checkpoints)
		}
	}
	start = make(chan struct{})
	for i := range writers {
		go func() {
			<-start
			finished <- stores[i%2].SaveCheckpoint(t.Context(), command.AgentID, command.ID,
				"winner", json.RawMessage(fmt.Sprintf(`{"index":%d}`, i)))
		}()
	}
	close(start)
	winners, conflicts := 0, 0
	for range writers {
		err := <-finished
		switch {
		case err == nil:
			winners++
		case errors.Is(err, executions.ErrCheckpointConflict):
			conflicts++
		default:
			t.Error(err)
		}
	}
	if winners != 1 || conflicts != writers-1 {
		t.Fatalf("same-name writes: winners=%d conflicts=%d", winners, conflicts)
	}
	restored, err := first.Get(t.Context(), command.AgentID, command.ID)
	if err != nil || len(restored.Checkpoints) != writers+1 {
		t.Fatalf("winning checkpoint or prior progress was lost: %#v, error=%v", restored.Checkpoints, err)
	}
	delete(restored.Checkpoints, "winner")
	if !reflect.DeepEqual(restored.Checkpoints, saved.Checkpoints) {
		t.Fatalf("conflicting writes changed prior progress: %#v", restored.Checkpoints)
	}
}

func TestExecutionMongoCheckpointCompletionRace(t *testing.T) {
	first, err := mongoexecutions.NewExecutionMongoStore(t.Context(), fixtures.OpenStore(t, "executions"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := mongoexecutions.NewExecutionMongoStore(t.Context(), fixtures.OpenStore(t, "executions"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		command := fixtures.ExecutionCommand(i + 1)
		if _, _, err := first.Claim(t.Context(), command); err != nil {
			t.Fatal(err)
		}
		checkpoint := make(chan error, 1)
		completed := make(chan error, 1)
		start := make(chan struct{})
		go func() {
			<-start
			checkpoint <- first.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "intent", json.RawMessage(`{"ready":true}`))
		}()
		go func() {
			<-start
			completed <- second.Complete(t.Context(), fixtures.ExecutionResult(command))
		}()
		close(start)
		checkpointErr, completionErr := <-checkpoint, <-completed
		if completionErr != nil {
			t.Fatal(completionErr)
		}
		if checkpointErr != nil && !errors.Is(checkpointErr, executions.ErrExecutionCompleted) {
			t.Fatal(checkpointErr)
		}
		saved, err := first.Get(t.Context(), command.AgentID, command.ID)
		if err != nil || saved.Result == nil {
			t.Fatalf("completion was lost: %#v, error=%v", saved, err)
		}
		_, present := saved.Checkpoints["intent"]
		if present != (checkpointErr == nil) {
			t.Fatalf("checkpoint write was incorrectly acknowledged: present=%t error=%v", present, checkpointErr)
		}
	}
}
