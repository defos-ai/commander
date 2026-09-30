package mongo_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"commander/framework/artifact"
	"commander/framework/comms/messages"
	"commander/framework/domain/actions"
	"commander/framework/domain/executions"
	mongoexecutions "commander/providers/mongo/controller/executions"
)

func TestExecutionMongoStoreReplayAndImmutableResult(t *testing.T) {
	catalog, err := mongoexecutions.NewExecutionMongoStore(t.Context(), openStore(t, "executions"))
	if err != nil {
		t.Fatal(err)
	}
	command := executionCommand(1)

	saved, claimed, err := catalog.Claim(t.Context(), command)

	if err != nil || !claimed || !reflect.DeepEqual(saved.Command, command) || saved.StartedAt.IsZero() || saved.Result != nil || saved.CompletedAt != nil {
		t.Fatalf("new reservation = %#v, claimed=%t, error=%v", saved, claimed, err)
	}
	saved.Command.Payload[0] = '!'
	saved, err = catalog.Get(t.Context(), command.AgentID, command.ID)
	if err != nil || !reflect.DeepEqual(saved.Command, command) {
		t.Fatalf("claim returned shared payload bytes: %#v, error=%v", saved, err)
	}
	replay := command
	replay.CreatedAt = replay.CreatedAt.Add(time.Hour)
	work, _ := json.Marshal(command.Work)
	replay.Payload = json.RawMessage(fmt.Sprintf(`{ "work": %s, "input": {"site":"example.test","serial":9007199254740993}, "action_name": "check" }`, work))
	restored, claimed, err := catalog.Claim(t.Context(), replay)
	if err != nil || claimed || !reflect.DeepEqual(restored, saved) {
		t.Fatalf("semantic replay changed the reservation: %#v, claimed=%t, error=%v", restored, claimed, err)
	}
	for _, change := range []func(*messages.Message){
		func(m *messages.Message) { m.Work.StepID = "different" },
		func(m *messages.Message) {
			m.Payload = json.RawMessage(strings.ReplaceAll(string(m.Payload), "9007199254740993", "9007199254740992"))
		},
	} {
		conflict := command
		change(&conflict)
		if _, _, err := catalog.Claim(t.Context(), conflict); !errors.Is(err, executions.ErrExecutionConflict) {
			t.Fatalf("changed command was not rejected: %v", err)
		}
	}
	result := executionResult(command)
	wrongWork := result
	wrongWork.Work.StepID = "different"
	if err := catalog.Complete(t.Context(), wrongWork); err == nil {
		t.Fatal("result for different work was accepted")
	}
	if err := catalog.Complete(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	completed, err := catalog.Get(t.Context(), command.AgentID, command.ID)
	if err != nil || completed.Result == nil || !reflect.DeepEqual(*completed.Result, result) || completed.CompletedAt == nil || completed.CompletedAt.IsZero() {
		t.Fatalf("terminal result was not preserved: %#v, error=%v", completed, err)
	}
	repeated := result
	repeated.CreatedAt = repeated.CreatedAt.Add(time.Hour)
	repeated.Payload = json.RawMessage(`{ "output": {"serial":9007199254740993}, "action_name": "check" }`)
	if err := catalog.Complete(t.Context(), repeated); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*messages.Message){
		func(m *messages.Message) { m.ID = executionResult(executionCommand(2)).ID },
		func(m *messages.Message) { m.Type = messages.CommandFailed },
		func(m *messages.Message) { m.Work.StepID = "different" },
		func(m *messages.Message) {
			m.Payload = json.RawMessage(strings.ReplaceAll(string(m.Payload), "9007199254740993", "9007199254740992"))
		},
	} {
		conflict := result
		change(&conflict)
		if err := catalog.Complete(t.Context(), conflict); !errors.Is(err, executions.ErrExecutionConflict) {
			t.Fatalf("terminal result changed: %v", err)
		}
	}
	unchanged, claimed, err := catalog.Claim(t.Context(), command)
	if err != nil || claimed || !reflect.DeepEqual(unchanged, completed) {
		t.Fatalf("completed replay changed saved timestamps or outcome: %#v, error=%v", unchanged, err)
	}
	unchanged.Result.Payload[0] = '!'
	*unchanged.CompletedAt = time.Time{}
	reloaded, err := catalog.Get(t.Context(), command.AgentID, command.ID)
	if err != nil || !reflect.DeepEqual(reloaded, completed) {
		t.Fatalf("reader mutated the saved result: %#v, error=%v", reloaded, err)
	}
}

func TestConcurrentExecutionClaimsAndCompletions(t *testing.T) {
	first, err := mongoexecutions.NewExecutionMongoStore(t.Context(), openStore(t, "executions"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := mongoexecutions.NewExecutionMongoStore(t.Context(), openStore(t, "executions"))
	if err != nil {
		t.Fatal(err)
	}
	catalogs := []executions.ExecutionStore{first, second}
	command := executionCommand(1)
	type claim struct {
		record  executions.Execution
		created bool
		err     error
	}
	const writers = 12
	claims := make(chan claim, writers)
	start := make(chan struct{})
	for i := range writers {
		go func() {
			<-start
			record, created, err := catalogs[i%2].Claim(t.Context(), command)
			claims <- claim{record, created, err}
		}()
	}

	close(start)
	winners := 0
	var started time.Time
	for range writers {
		result := <-claims
		if result.err != nil {
			t.Error(result.err)
			continue
		}
		if result.created {
			winners++
		}
		if started.IsZero() {
			started = result.record.StartedAt
		}
		if !result.record.StartedAt.Equal(started) || !reflect.DeepEqual(result.record.Command, command) {
			t.Errorf("callers observed different reservations: %#v", result.record)
		}
	}
	if winners != 1 {
		t.Fatalf("claim winners=%d, want 1", winners)
	}
	responses := []messages.Message{executionResult(command), executionResult(command)}
	responses[1].ID = executionResult(executionCommand(2)).ID
	references := []artifact.Ref{executionArtifact(command, 1), executionArtifact(command, 2)}
	for i, ref := range references {
		responses[i].Payload, _ = json.Marshal(actions.ActionOutput{ActionName: "check", Output: json.RawMessage(`{}`), Artifacts: []artifact.Ref{ref}})
	}
	type completion struct {
		index int
		err   error
	}
	completed := make(chan completion, 2)
	start = make(chan struct{})
	for i, catalog := range catalogs {
		go func() { <-start; completed <- completion{i, catalog.Complete(t.Context(), responses[i])} }()
	}

	close(start)
	winner, conflicts := -1, 0
	for range catalogs {
		result := <-completed
		if result.err == nil {
			if winner != -1 {
				t.Error("conflicting completions both succeeded")
			}
			winner = result.index
		} else if errors.Is(result.err, executions.ErrExecutionConflict) {
			conflicts++
		} else {
			t.Error(result.err)
		}
	}
	stored, err := first.Get(t.Context(), command.AgentID, command.ID)
	if winner == -1 || conflicts != 1 || err != nil || stored.Result == nil || !reflect.DeepEqual(*stored.Result, responses[winner]) {
		t.Fatalf("completion race lost its winner: winner=%d conflicts=%d record=%#v error=%v", winner, conflicts, stored, err)
	}
	found, err := second.FindArtifact(t.Context(), references[winner].ID)
	if err != nil || found != references[winner] {
		t.Fatalf("winning completion lost artifact authorization: %#v, error=%v", found, err)
	}
	if _, err := second.FindArtifact(t.Context(), references[1-winner].ID); !errors.Is(err, artifact.ErrUnknownArtifact) {
		t.Fatalf("losing completion authorized an artifact: %v", err)
	}
}

func TestExecutionMongoStoreRejectsMissingInvalidAndCancelledWrites(t *testing.T) {
	database := openStore(t, "executions")
	catalog, err := mongoexecutions.NewExecutionMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	command := executionCommand(1)
	result := executionResult(command)
	if err := catalog.Complete(t.Context(), result); !errors.Is(err, executions.ErrUnknownExecution) {
		t.Fatalf("unclaimed completion: %v", err)
	}
	for _, change := range []func(*messages.Message){
		func(m *messages.Message) { m.ID = "invalid" },
		func(m *messages.Message) { m.AgentID = "invalid" },
		func(m *messages.Message) { m.Type = messages.StopAgent },
		func(m *messages.Message) { m.Work.TaskID = "" },
		func(m *messages.Message) { m.Payload = json.RawMessage(`[]`) },
	} {
		invalid := command
		change(&invalid)
		if _, _, err := catalog.Claim(t.Context(), invalid); err == nil {
			t.Fatalf("invalid command was claimed: %#v", invalid)
		}
	}
	if _, err := catalog.Get(t.Context(), command.AgentID, command.ID); !errors.Is(err, executions.ErrUnknownExecution) {
		t.Fatalf("rejected writes created a reservation: %v", err)
	}
	if _, _, err := catalog.Claim(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := mongoexecutions.NewExecutionMongoStore(cancelled, database); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled constructor: %v", err)
	}
	if _, _, err := catalog.Claim(cancelled, executionCommand(2)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled claim: %v", err)
	}
	if _, err := catalog.Get(cancelled, command.AgentID, command.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled get: %v", err)
	}
	if err := catalog.Complete(cancelled, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled completion: %v", err)
	}
	if _, err := catalog.FindArtifact(cancelled, executionArtifact(command, 1).ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled artifact lookup: %v", err)
	}
	if _, err := catalog.FindArtifact(t.Context(), "invalid"); err == nil {
		t.Fatal("invalid artifact ID was accepted")
	}
	stored, err := catalog.Get(t.Context(), command.AgentID, command.ID)
	if err != nil || stored.Result != nil || stored.CompletedAt != nil {
		t.Fatalf("cancelled write completed a reservation: %#v, error=%v", stored, err)
	}
	if _, err := catalog.Get(t.Context(), command.AgentID, executionCommand(2).ID); !errors.Is(err, executions.ErrUnknownExecution) {
		t.Fatalf("cancelled claim created state: %v", err)
	}
}

func TestExecutionMongoStoreAuthorizesOnlySuccessfulArtifacts(t *testing.T) {
	catalog, err := mongoexecutions.NewExecutionMongoStore(t.Context(), openStore(t, "executions"))
	if err != nil {
		t.Fatal(err)
	}
	command := executionCommand(1)
	output, attachment := executionArtifact(command, 1), executionArtifact(command, 2)
	command.Payload, _ = json.Marshal(actions.ActionCall{ActionName: "check", Work: command.Work, Input: json.RawMessage(fmt.Sprintf(`{"artifact_id":%q}`, output.ID))})
	if _, _, err := catalog.Claim(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []artifact.Ref{output, attachment} {
		if _, err := catalog.FindArtifact(t.Context(), ref.ID); !errors.Is(err, artifact.ErrUnknownArtifact) {
			t.Fatalf("pending command authorized an artifact: %v", err)
		}
	}
	result := executionResult(command)
	result.Payload, _ = json.Marshal(actions.ActionOutput{ActionName: "check", OutputArtifact: &output, Artifacts: []artifact.Ref{attachment}})

	if err := catalog.Complete(t.Context(), result); err != nil {
		t.Fatal(err)
	}

	for _, ref := range []artifact.Ref{output, attachment} {
		found, err := catalog.FindArtifact(t.Context(), ref.ID)
		if err != nil || found != ref {
			t.Fatalf("successful artifact metadata changed: %#v, error=%v", found, err)
		}
	}
	failed := executionCommand(2)
	failedRef := executionArtifact(failed, 3)
	if _, _, err := catalog.Claim(t.Context(), failed); err != nil {
		t.Fatal(err)
	}
	failure := executionResult(failed)
	failure.Type = messages.CommandFailed
	failure.Payload, _ = json.Marshal(map[string]any{"error_type": "fixture", "message": "failed", "artifacts": []artifact.Ref{failedRef}})
	if err := catalog.Complete(t.Context(), failure); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []artifact.Ref{failedRef, executionArtifact(command, 4)} {
		if _, err := catalog.FindArtifact(t.Context(), ref.ID); !errors.Is(err, artifact.ErrUnknownArtifact) {
			t.Fatalf("failed or unknown artifact was authorized: %v", err)
		}
	}
}

func TestExecutionMongoStoreRejectsConflictingArtifactOwnership(t *testing.T) {
	for _, test := range []string{"agent", "command", "action", "within result", "across executions"} {
		t.Run(test, func(t *testing.T) {
			catalog, err := mongoexecutions.NewExecutionMongoStore(t.Context(), openStore(t, "executions"))
			if err != nil {
				t.Fatal(err)
			}
			command := executionCommand(1)
			ref := executionArtifact(command, 1)
			output := actions.ActionOutput{ActionName: "check", Output: json.RawMessage(`{}`)}
			switch test {
			case "agent":
				ref.AgentID = "web-quality-agent-00000002"
			case "command":
				ref.CommandID = executionCommand(2).ID
			case "action":
				ref.ActionName = "different"
			case "within result":
				other := ref
				other.Name = "conflicting-name.json"
				output.Artifacts = append(output.Artifacts, other)
			}
			output.Artifacts = append(output.Artifacts, ref)
			if _, _, err := catalog.Claim(t.Context(), command); err != nil {
				t.Fatal(err)
			}
			result := executionResult(command)
			result.Payload, _ = json.Marshal(output)
			if err := catalog.Complete(t.Context(), result); err != nil {
				t.Fatal(err)
			}
			if test == "across executions" {
				second := executionCommand(2)
				if _, _, err := catalog.Claim(t.Context(), second); err != nil {
					t.Fatal(err)
				}
				ref.CommandID = second.ID
				output.Artifacts = []artifact.Ref{ref}
				result = executionResult(second)
				result.Payload, _ = json.Marshal(output)
				if err := catalog.Complete(t.Context(), result); err != nil {
					t.Fatal(err)
				}
			}

			_, err = catalog.FindArtifact(t.Context(), ref.ID)

			if err == nil || errors.Is(err, artifact.ErrUnknownArtifact) {
				t.Fatalf("conflicting artifact ownership was not reported: %v", err)
			}
		})
	}
}

func executionCommand(index int) messages.Message {
	work := messages.WorkContext{OperationID: "10000000-0000-4000-8000-000000000001", TaskID: "20000000-0000-4000-8000-000000000001", StepID: "review"}
	payload, _ := json.Marshal(actions.ActionCall{ActionName: "check", Work: work, Input: json.RawMessage(`{"serial":9007199254740993,"site":"example.test"}`)})
	return messages.Message{ID: fmt.Sprintf("00000000-0000-4000-8000-%012x", index), Type: messages.RunAction, AgentID: "web-quality-agent-00000001", Work: work, Payload: payload, CreatedAt: time.Date(2026, 9, 30, 12, 0, 0, 123456789, time.UTC)}
}

func executionResult(command messages.Message) messages.Message {
	return messages.Message{ID: "30000000" + command.ID[8:], Type: messages.ActionSucceeded, AgentID: command.AgentID, CorrelationID: command.ID, Work: command.Work, Payload: json.RawMessage(`{"action_name":"check","output":{"serial":9007199254740993}}`), CreatedAt: command.CreatedAt.Add(time.Nanosecond)}
}

func executionArtifact(command messages.Message, index int) artifact.Ref {
	return artifact.Ref{ID: fmt.Sprintf("40000000-0000-4000-8000-%012x", index), Name: "result.json", MediaType: "application/json", Size: 2, SHA256: strings.Repeat("a", 64), AgentID: command.AgentID, ActionName: "check", CommandID: command.ID, CreatedAt: command.CreatedAt.Add(2 * time.Nanosecond)}
}
