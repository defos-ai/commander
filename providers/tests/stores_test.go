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
	dir := t.TempDir()
	first, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	op := operations.Operation{ID: id.OperationID(), Status: operations.Planning}
	if err := first.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	gate, results := make(chan struct{}), make(chan error, 2)

	for _, store := range []*operationfs.OperationFsStore{first, second} {
		go func() {
			<-gate
			_, err := store.Update(t.Context(), op.ID, func(next *operations.Operation) error {
				next.Events = append(next.Events, operations.Event{Sequence: uint64(len(next.Events) + 1)})
				return nil
			})
			results <- err
		}()
	}
	close(gate)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := reopened.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Events) != 2 || stored.Events[0].Sequence != 1 || stored.Events[1].Sequence != 2 {
		t.Fatalf("concurrent mutations lost an event: %+v", stored)
	}

	rejected := errors.New("invalid transition")
	_, err = reopened.Update(t.Context(), op.ID, func(next *operations.Operation) error {
		next.Status = operations.Completed
		next.Events[0].Sequence = 100
		return rejected
	})
	if !errors.Is(err, rejected) {
		t.Fatalf("expected callback rejection, got %v", err)
	}
	after, err := first.Get(t.Context(), op.ID)
	if err != nil || !reflect.DeepEqual(after, stored) {
		t.Fatalf("rejected mutation changed storage: %+v, %v", after, err)
	}
	payload, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]any
	if err := json.Unmarshal(payload, &public); err != nil {
		t.Fatal(err)
	}
	if _, exists := public["version"]; exists {
		t.Fatal("storage revision escaped into the domain operation")
	}
}

func TestExecutionClaimAndOutcomeRemainStableAcrossReopen(t *testing.T) {
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
