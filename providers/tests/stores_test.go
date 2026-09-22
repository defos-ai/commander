package providers_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	agentcommands "commander/framework/comms/clients/agent/commands"
	"commander/framework/comms/messages"
	"commander/framework/domain/actions"
	"commander/framework/domain/executions"
	"commander/framework/domain/operations"
	"commander/framework/id"
	executionfs "commander/providers/fs/controller/executions"
	operationfs "commander/providers/fs/controller/operations"
)

func TestOperationUpdatesAreAtomicAcrossStoreInstances(t *testing.T) {
	// Arrange: independent store instances share one durable operation version.
	dir := t.TempDir()
	first, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	op := operations.Operation{ID: id.OperationID(), Version: 1, Status: operations.Planning}
	if err := first.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	type result struct {
		operation operations.Operation
		err       error
	}
	gate, results := make(chan struct{}), make(chan result, 2)

	// Act: both writers attempt to replace version one with different content.
	for i, store := range []*operationfs.OperationFsStore{first, second} {
		candidate := op.Clone()
		candidate.Version = 2
		candidate.Output, err = json.Marshal(map[string]int{"writer": i})
		if err != nil {
			t.Fatal(err)
		}
		go func() { <-gate; results <- result{candidate, store.Update(t.Context(), candidate, 1)} }()
	}
	close(gate)
	winner, loser := <-results, <-results
	if winner.err != nil {
		winner, loser = loser, winner
	}
	reopened, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := reopened.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Assert: one write commits, the loser conflicts, and reopening preserves it.
	var actual, expected map[string]int
	if err := json.Unmarshal(stored.Output, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(winner.operation.Output, &expected); err != nil {
		t.Fatal(err)
	}
	if winner.err != nil || !errors.Is(loser.err, operations.ErrOperationConflict) || stored.ID != op.ID || stored.Version != 2 || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("lost or merged concurrent update: winner=%v, loser=%v, stored=%+v", winner.err, loser.err, stored)
	}
	if err := reopened.Update(t.Context(), loser.operation, 1); !errors.Is(err, operations.ErrOperationConflict) {
		t.Fatalf("stale retry overwrote committed state: %v", err)
	}
}

func TestExecutionClaimAndOutcomeRemainStableAcrossReopen(t *testing.T) {
	// Arrange: two hosts can see the same command before either owns its claim.
	dir := t.TempDir()
	first, err := executionfs.NewExecutionFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := executionfs.NewExecutionFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	call := actions.ActionCall{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)}
	command, err := agentcommands.NewRunAction("review-agent-00000001", call)
	if err != nil {
		t.Fatal(err)
	}
	type claim struct {
		created bool
		err     error
	}
	gate, claims := make(chan struct{}), make(chan claim, 2)

	// Act: compete for one reservation, then persist and replay its result.
	for _, store := range []*executionfs.ExecutionFsStore{first, second} {
		go func() { <-gate; _, created, err := store.Claim(t.Context(), command); claims <- claim{created, err} }()
	}
	close(gate)
	a, b := <-claims, <-claims
	if a.err != nil || b.err != nil || a.created == b.created {
		t.Fatalf("claim must have one owner: %+v %+v", a, b)
	}
	result, err := messages.NewMessage(messages.ActionSucceeded, command.AgentID, actions.ActionOutput{ActionName: call.ActionName, Output: json.RawMessage(`{"ok":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	result.CorrelationID, result.Work = command.ID, command.Work
	if err := first.Complete(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	if err := second.Complete(t.Context(), result); err != nil {
		t.Fatalf("identical result replay failed: %v", err)
	}
	reopened, err := executionfs.NewExecutionFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, created, err := reopened.Claim(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}

	// Assert: restart returns the original outcome and rejects conflicting reuse.
	if created || saved.Result == nil || saved.CompletedAt == nil || !reflect.DeepEqual(*saved.Result, result) {
		t.Fatalf("reopen lost the accepted outcome: %+v", saved)
	}
	conflicting := command
	conflicting.Payload = json.RawMessage(`{"action_name":"inspect","input":{"page":2}}`)
	if _, _, err := reopened.Claim(t.Context(), conflicting); !errors.Is(err, executions.ErrExecutionConflict) {
		t.Fatalf("command identity was reused: %v", err)
	}
	conflicting = result
	conflicting.Payload = json.RawMessage(`{"action_name":"inspect","output":{"ok":false}}`)
	if err := reopened.Complete(t.Context(), conflicting); !errors.Is(err, executions.ErrExecutionConflict) {
		t.Fatalf("accepted result was replaced: %v", err)
	}
	unchanged, err := reopened.Get(t.Context(), command.AgentID, command.ID)
	if err != nil || !reflect.DeepEqual(saved, unchanged) {
		t.Fatalf("conflict changed saved execution: %+v, %v", unchanged, err)
	}
}
