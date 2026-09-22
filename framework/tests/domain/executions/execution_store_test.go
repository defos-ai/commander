package executions_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"commander/framework/comms/messages"
	"commander/framework/domain/executions"
	providers "commander/providers/controller"
	fsexecutions "commander/providers/fs/controller/executions"
)

const agentID = "web-quality-agent-a5452d64"
const otherAgentID = "other-agent-2f619c37"

func openStore(t *testing.T, root string) executions.ExecutionStore {
	t.Helper()
	storage, err := providers.OpenDatabase(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := storage.Close(); err != nil {
			t.Error(err)
		}
	})
	return storage.ExecutionStore
}

func command(t *testing.T) messages.Message {
	t.Helper()
	message, err := messages.NewMessage(messages.RunAction, agentID, map[string]any{"action_name": "example", "input": map[string]string{"value": "original"}})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func response(t *testing.T, request messages.Message, kind messages.MessageType) messages.Message {
	t.Helper()
	message, err := messages.NewMessage(kind, request.AgentID, map[string]string{"result": "saved"})
	if err != nil {
		t.Fatal(err)
	}
	message.CorrelationID, message.Work = request.ID, request.Work
	return message
}

func TestOnlyOneConcurrentClaimReservesDispatch(t *testing.T) {
	// Arrange
	root, request := t.TempDir(), command(t)
	const count = 12
	stores := make([]executions.ExecutionStore, count)
	for index := range stores {
		stores[index] = openStore(t, root)
	}
	type outcome struct {
		record  executions.Execution
		claimed bool
		err     error
	}
	results := make(chan outcome, count)
	start := make(chan struct{})
	var group sync.WaitGroup

	// Act
	for _, store := range stores {
		group.Go(func() {
			<-start
			record, claimed, err := store.Claim(t.Context(), request)
			results <- outcome{record, claimed, err}
		})
	}
	close(start)
	group.Wait()
	close(results)

	// Assert
	claims := 0
	var original *executions.Execution
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.claimed {
			claims++
		}
		if original == nil {
			original = &result.record
		}
		if !reflect.DeepEqual(result.record, *original) || result.record.Result != nil || result.record.StartedAt.IsZero() {
			t.Fatalf("claims disagree: %+v and %+v", result.record, original)
		}
	}
	if claims != 1 {
		t.Fatalf("%d callers reserved dispatch; want 1", claims)
	}
}

func TestTerminalOutcomesSurviveRestartAndDuplicateDelivery(t *testing.T) {
	for _, kind := range []messages.MessageType{messages.ActionSucceeded, messages.CommandFailed} {
		t.Run(string(kind), func(t *testing.T) {
			// Arrange
			root, request := t.TempDir(), command(t)
			store := openStore(t, root)
			if _, claimed, err := store.Claim(t.Context(), request); err != nil || !claimed {
				t.Fatalf("claim: claimed=%v, error=%v", claimed, err)
			}
			result := response(t, request, kind)

			// Act
			completeErr := store.Complete(t.Context(), result)
			reopened := openStore(t, root)
			retryErr := reopened.Complete(t.Context(), result)
			record, claimed, claimErr := reopened.Claim(t.Context(), request)
			read, readErr := reopened.Get(t.Context(), agentID, request.ID)

			// Assert
			if completeErr != nil || retryErr != nil || claimErr != nil || readErr != nil || claimed {
				t.Fatalf("complete=%v retry=%v claim=%v read=%v; claimed=%v", completeErr, retryErr, claimErr, readErr, claimed)
			}
			if record.Result == nil || record.CompletedAt == nil || !reflect.DeepEqual(*record.Result, result) || !reflect.DeepEqual(record, read) {
				t.Fatalf("saved terminal outcome changed: %+v", record)
			}
		})
	}
}

func TestRepeatedLogicalCommandIgnoresTimestampAndPayloadKeyOrder(t *testing.T) {
	// Arrange
	store, request := openStore(t, t.TempDir()), command(t)
	original, _, err := store.Claim(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	retry := request
	retry.CreatedAt = retry.CreatedAt.Add(time.Second)
	retry.Payload = json.RawMessage(`{"input":{"value":"original"}, "action_name":"example"}`)

	// Act
	record, claimed, claimErr := store.Claim(t.Context(), retry)

	// Assert
	if claimErr != nil || claimed || !reflect.DeepEqual(record, original) {
		t.Fatalf("logical retry changed reservation: claimed=%v error=%v record=%+v", claimed, claimErr, record)
	}
}

func TestExecutionPayloadComparisonPreservesLargeNumbers(t *testing.T) {
	// Arrange
	store, request := openStore(t, t.TempDir()), command(t)
	request.Payload = json.RawMessage(`{"action_name":"example","input":{"id":9007199254740992}}`)
	if _, _, err := store.Claim(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	changed := request
	changed.Payload = json.RawMessage(`{"action_name":"example","input":{"id":9007199254740993}}`)

	// Act
	_, claimed, err := store.Claim(t.Context(), changed)

	// Assert
	if claimed || !errors.Is(err, executions.ErrExecutionConflict) {
		t.Fatalf("different large integer was treated as identical: claimed=%v error=%v", claimed, err)
	}
}

func TestReceivedOutcomeRetryIgnoresSerializationDetails(t *testing.T) {
	// Arrange
	store, request := openStore(t, t.TempDir()), command(t)
	if _, _, err := store.Claim(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	result := response(t, request, messages.ActionSucceeded)
	result.Payload = json.RawMessage(`{"result":"saved","count":9007199254740993}`)
	if err := store.Complete(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	retry := result
	retry.CreatedAt = retry.CreatedAt.Add(time.Second)
	retry.Payload = json.RawMessage(`{ "count":9007199254740993, "result":"saved" }`)

	// Act
	retryErr := store.Complete(t.Context(), retry)
	record, readErr := store.Get(t.Context(), agentID, request.ID)

	// Assert
	if retryErr != nil || readErr != nil || record.Result == nil || !reflect.DeepEqual(*record.Result, result) {
		t.Fatalf("response retry changed saved outcome: complete=%v read=%v record=%+v", retryErr, readErr, record)
	}
}

func TestConflictingCommandsAndResultsCannotOverwriteExecution(t *testing.T) {
	// Arrange
	store, request := openStore(t, t.TempDir()), command(t)
	if _, _, err := store.Claim(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	result := response(t, request, messages.ActionSucceeded)
	if err := store.Complete(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	changedRequest := request
	changedRequest.Payload = json.RawMessage(`{"action_name":"something_else"}`)
	changedResult := result
	changedResult.Payload = json.RawMessage(`{"result":"changed"}`)

	// Act
	_, claimed, claimErr := store.Claim(t.Context(), changedRequest)
	completeErr := store.Complete(t.Context(), changedResult)
	record, readErr := store.Get(t.Context(), agentID, request.ID)

	// Assert
	if claimed || !errors.Is(claimErr, executions.ErrExecutionConflict) || !errors.Is(completeErr, executions.ErrExecutionConflict) {
		t.Fatalf("conflicting writes: claimed=%v claim=%v complete=%v", claimed, claimErr, completeErr)
	}
	if readErr != nil || !reflect.DeepEqual(record.Command, request) || record.Result == nil || !reflect.DeepEqual(*record.Result, result) {
		t.Fatalf("original outcome replaced: %+v, %v", record, readErr)
	}
}

func TestWrongWorkContextCannotCompleteAClaim(t *testing.T) {
	// Arrange
	store, request := openStore(t, t.TempDir()), command(t)
	if _, _, err := store.Claim(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	result := response(t, request, messages.ActionSucceeded)
	result.Work = messages.WorkContext{OperationID: otherAgentID, TaskID: agentID}

	// Act
	completeErr := store.Complete(t.Context(), result)
	record, readErr := store.Get(t.Context(), agentID, request.ID)

	// Assert
	if completeErr == nil || readErr != nil || record.Result != nil || record.CompletedAt != nil {
		t.Fatalf("wrong task result completed claim: %+v, complete=%v read=%v", record, completeErr, readErr)
	}
}

func TestExecutionKeysIsolateAgents(t *testing.T) {
	// Arrange
	store, request := openStore(t, t.TempDir()), command(t)
	otherRequest := request
	otherRequest.AgentID = otherAgentID
	if _, _, err := store.Claim(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Claim(t.Context(), otherRequest); err != nil {
		t.Fatal(err)
	}
	result := response(t, request, messages.ActionSucceeded)

	// Act
	completeErr := store.Complete(t.Context(), result)
	record, ownErr := store.Get(t.Context(), agentID, request.ID)
	otherAgent, otherAgentErr := store.Get(t.Context(), otherAgentID, request.ID)

	// Assert
	if completeErr != nil || ownErr != nil || otherAgentErr != nil {
		t.Fatalf("access: complete=%v own=%v agent=%v", completeErr, ownErr, otherAgentErr)
	}
	if record.Result == nil || otherAgent.Result != nil {
		t.Fatalf("execution records overlap: own=%+v agent=%+v", record, otherAgent)
	}
}

func TestMissingReadsAndCancelledWritesDoNotCreateState(t *testing.T) {
	// Arrange
	root := filepath.Join(t.TempDir(), "missing")
	store, err := fsexecutions.NewExecutionFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	request := command(t)
	result := response(t, request, messages.ActionSucceeded)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	// Act
	_, readErr := store.Get(t.Context(), agentID, request.ID)
	unknownErr := store.Complete(t.Context(), result)
	_, claimed, claimErr := store.Claim(cancelled, request)
	completeErr := store.Complete(cancelled, result)
	_, cancelReadErr := store.Get(cancelled, agentID, request.ID)
	_, statErr := os.Stat(root)

	// Assert
	if !errors.Is(readErr, executions.ErrUnknownExecution) || !errors.Is(unknownErr, executions.ErrUnknownExecution) {
		t.Fatalf("missing record: get=%v complete=%v", readErr, unknownErr)
	}
	if claimed || !errors.Is(claimErr, context.Canceled) || !errors.Is(completeErr, context.Canceled) || !errors.Is(cancelReadErr, context.Canceled) {
		t.Fatalf("cancelled calls: claimed=%v claim=%v complete=%v get=%v", claimed, claimErr, completeErr, cancelReadErr)
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("read or cancelled operation created state: %v", statErr)
	}
}
