package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"commander/framework/comms/clients/controller/commands"
	"commander/framework/controller/runner"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/operations"
)

func TestQuestionDefaultsAndValidation(t *testing.T) {
	// Arrange: a live operation accepts free text or up to four choices.
	config := automationConfig(t)
	r, op := questionRunner(t, config)
	for _, tc := range []struct {
		name    string
		seconds int
		options []string
		invalid bool
	}{
		{name: "free text defaults to two minutes"},
		{name: "four options and custom deadline", seconds: 15, options: []string{"A", "B", "C", "D"}},
		{name: "maximum deadline", seconds: 86400},
		{name: "negative deadline", seconds: -1, invalid: true},
		{name: "excessive deadline", seconds: 86401, invalid: true},
		{name: "too many options", options: []string{"A", "B", "C", "D", "E"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Act.
			q, err := r.Ask(t.Context(), commands.AskQuestionCommand{OperationID: op.ID, Text: "What next?", Options: tc.options, TimeoutSeconds: tc.seconds})

			// Assert: invalid questions never enter the operation; valid ones preserve real answers.
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid question was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := time.Duration(tc.seconds) * time.Second
			if tc.seconds == 0 {
				want = 2 * time.Minute
			}
			if q.Status != operations.QuestionPending || !q.Pending() || q.AnsweredAt != nil || q.ExpiresAt.Sub(q.AskedAt) != want {
				t.Fatalf("unexpected deadline or state: %+v", q)
			}
			answered, err := r.Answer(t.Context(), commands.AnswerQuestionCommand{OperationID: op.ID, QuestionID: q.ID, Answer: "Keep reviewing"})
			if err != nil {
				t.Fatal(err)
			}
			last := answered.Questions[len(answered.Questions)-1]
			if last.Status != operations.QuestionAnswered || last.Pending() || last.Answer != "Keep reviewing" || last.AnsweredAt == nil {
				t.Fatalf("answer not preserved: %+v", last)
			}
			if _, err := r.Answer(t.Context(), commands.AnswerQuestionCommand{OperationID: op.ID, QuestionID: q.ID, Answer: "Replace previous answer"}); err == nil || !strings.Contains(err.Error(), "already answered") {
				t.Fatalf("answered question accepted another answer: %v", err)
			}
		})
	}
	saved, err := config.OperationStore.Get(t.Context(), op.ID)
	if err != nil || len(saved.Questions) != 3 {
		t.Fatalf("invalid questions were persisted: %+v, %v", saved.Questions, err)
	}
}

func TestQuestionExpiryPersistsAndResumesDecision(t *testing.T) {
	// Arrange: a completed task waits for its operator, then inventory becomes unavailable.
	config := automationConfig(t)
	store := &failFirstExpiryStore{OperationStore: config.OperationStore}
	catalog := &questionCatalog{}
	config.OperationStore, config.AgentCatalog = store, catalog
	r, op := questionRunner(t, config)
	task := completedQuestionTask(t, r, op.ID)
	q, err := r.Ask(t.Context(), commands.AskQuestionCommand{OperationID: op.ID, TaskID: task.ID, Version: task.Continuation.Version, Text: "Continue?", TimeoutSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	catalog.unavailable.Store(true)

	// Act: the first expiration write fails; the scheduler must retry without an agent catalog.
	saved := waitQuestionExpired(t, config.OperationStore, op.ID)

	// Assert: expiration, event and continuation are one durable update, without an invented answer.
	if !store.failed.Load() {
		t.Fatal("fixture did not exercise the failed expiration write")
	}
	assertExpiredQuestion(t, saved, q.ID)
	c := saved.Tasks[0].Continuation
	if saved.Status != operations.Planning || c.DecisionBy != operations.DecisionModel || c.Status != operations.ContinuationPending || c.Version != task.Continuation.Version+2 || c.QuestionID != q.ID {
		t.Fatalf("decision not returned to model: status=%s continuation=%+v", saved.Status, c)
	}
	if _, err := r.Answer(t.Context(), commands.AnswerQuestionCommand{OperationID: op.ID, QuestionID: q.ID, Answer: "Too late"}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("late answer accepted: %v", err)
	}
	if _, err := r.Resolve(t.Context(), commands.ResolveContinuationCommand{OperationID: op.ID, TaskID: task.ID, Version: c.Version, Actions: []actionmodel.NextAction{}}); err != nil {
		t.Fatalf("expired question still blocks continuation: %v", err)
	}
	if _, err := r.Complete(t.Context(), commands.CompleteOperationCommand{OperationID: op.ID, Output: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("expired question still blocks completion: %v", err)
	}
}

func TestLateAnswerExpiresQuestionBeforeScheduler(t *testing.T) {
	// Arrange: keep inventory lookup in progress so no periodic expiration can run.
	config := automationConfig(t)
	catalog := &blockedQuestionCatalog{entered: make(chan struct{}), release: make(chan struct{})}
	config.AgentCatalog = catalog
	r, op := questionRunner(t, config)
	t.Cleanup(func() { close(catalog.release) })
	select {
	case <-catalog.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not enter inventory lookup")
	}
	q, err := r.Ask(t.Context(), commands.AskQuestionCommand{OperationID: op.ID, Text: "Continue?", TimeoutSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(q.ExpiresAt))

	// Act: the answer arrives at or after its deadline, before the next sweep.
	_, err = r.Answer(t.Context(), commands.AnswerQuestionCommand{OperationID: op.ID, QuestionID: q.ID, Answer: "Late guidance"})

	// Assert: reject the answer but commit expiration, allowing subsequent work.
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("late answer accepted: %v", err)
	}
	saved, err := config.OperationStore.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertExpiredQuestion(t, saved, q.ID)
	if _, err := r.Assign(t.Context(), commands.AssignTaskCommand{OperationID: op.ID, ActionName: "inspect"}); err != nil {
		t.Fatalf("expired question still blocks assignment: %v", err)
	}
}

func TestQuestionExpiryAfterRestart(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		name := "active operation resumes model"
		if terminal {
			name = "cancelled operation preserves history without resuming"
		}
		t.Run(name, func(t *testing.T) {
			// Arrange: persist an operator question and stop the process before its deadline.
			config := automationConfig(t)
			r, op := questionRunner(t, config)
			task := completedQuestionTask(t, r, op.ID)
			q, err := r.Ask(t.Context(), commands.AskQuestionCommand{OperationID: op.ID, TaskID: task.ID, Version: task.Continuation.Version, Text: "Continue?"})
			if err != nil {
				t.Fatal(err)
			}
			if terminal {
				if _, err := r.Cancel(t.Context(), commands.CancelOperationCommand{OperationID: op.ID}); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			saved, err := config.OperationStore.Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			// Simulate the deadline passing while the process was down.
			saved.Questions[0].AskedAt = time.Now().Add(-3 * time.Minute)
			saved.Questions[0].ExpiresAt = time.Now().Add(-time.Minute)
			saved.Version++
			if err := config.OperationStore.Update(t.Context(), saved, saved.Version-1); err != nil {
				t.Fatal(err)
			}

			// Act: recovery must persist expiry before NewRunner returns, and only once.
			for range 2 {
				restarted, err := runner.NewRunner(t.Context(), automationExecutor{empty: true}, config)
				if err != nil {
					t.Fatal(err)
				}
				saved, err = config.OperationStore.Get(t.Context(), op.ID)
				if closeErr := restarted.Close(t.Context()); closeErr != nil {
					t.Fatal(closeErr)
				}
				if err != nil {
					t.Fatal(err)
				}

				// Assert: restart never fabricates an answer or reopens terminal work.
				assertExpiredQuestion(t, saved, q.ID)
				c := saved.Tasks[0].Continuation
				if terminal {
					if saved.Status != operations.Cancelled || c.DecisionBy != operations.DecisionUser || c.Status != operations.ContinuationWaiting || c.Version != task.Continuation.Version+1 {
						t.Fatalf("cancelled operation resumed: %+v", saved)
					}
				} else if saved.Status != operations.Planning || c.DecisionBy != operations.DecisionModel || c.Status != operations.ContinuationPending || c.Version != task.Continuation.Version+2 {
					t.Fatalf("active operation did not resume once: %+v", saved)
				}
			}
		})
	}
}

func TestFutureQuestionDeadlineSurvivesCancelledOperationRestart(t *testing.T) {
	// Arrange: cancellation finishes the operation while an unanswered deadline remains.
	config := automationConfig(t)
	r, op := questionRunner(t, config)
	q, err := r.Ask(t.Context(), commands.AskQuestionCommand{OperationID: op.ID, Text: "Continue?"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Cancel(t.Context(), commands.CancelOperationCommand{OperationID: op.ID}); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved, err := config.OperationStore.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved.Questions[0].ExpiresAt = time.Now().Add(time.Second)
	saved.Version++
	if err := config.OperationStore.Update(t.Context(), saved, saved.Version-1); err != nil {
		t.Fatal(err)
	}

	// Act: restart before the deadline, then let the scheduler retain and expire it.
	restarted, err := runner.NewRunner(t.Context(), automationExecutor{empty: true}, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close(context.Background()) })
	saved = waitQuestionExpired(t, config.OperationStore, op.ID)

	// Assert: even terminal questions get a durable expiration, without reopening work.
	assertExpiredQuestion(t, saved, q.ID)
	if saved.Status != operations.Cancelled {
		t.Fatalf("cancelled operation reopened: %s", saved.Status)
	}
}

func questionRunner(t *testing.T, config runner.Config) (*runner.Runner, operations.Operation) {
	t.Helper()
	r, err := runner.NewRunner(t.Context(), automationExecutor{empty: true}, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	op, err := r.Start(t.Context(), commands.StartOperationCommand{MissionName: "automation", Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return r, op
}

func completedQuestionTask(t *testing.T, r *runner.Runner, operationID string) operations.Task {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	task, err := r.Assign(ctx, commands.AssignTaskCommand{OperationID: operationID, AgentID: "fixture-agent-1234abcd", ActionName: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	task, err = r.WaitTask(ctx, operationID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func waitQuestionExpired(t *testing.T, store operations.OperationStore, operationID string) operations.Operation {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		op, err := store.Get(ctx, operationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.Questions[0].Status == operations.QuestionExpired {
			return op
		}
		select {
		case <-ctx.Done():
			t.Fatal("question was not expired")
		case <-ticker.C:
		}
	}
}

func assertExpiredQuestion(t *testing.T, op operations.Operation, questionID string) {
	t.Helper()
	q := op.Questions[0]
	if q.ID != questionID || q.Status != operations.QuestionExpired || q.Pending() || q.ExpiresAt.IsZero() || time.Now().Before(q.ExpiresAt) || q.AnsweredAt != nil || q.Answer != "" {
		t.Fatalf("invalid expiration history: %+v", q)
	}
	count := 0
	for _, event := range op.Events {
		if event.Type == operations.QuestionExpiredEvent && event.QuestionID == q.ID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("got %d expiration events, want exactly one", count)
	}
}

type failFirstExpiryStore struct {
	operations.OperationStore
	failed atomic.Bool
}

func (s *failFirstExpiryStore) Update(ctx context.Context, op operations.Operation, version uint64) error {
	if len(op.Questions) > 0 && op.Questions[0].Status == operations.QuestionExpired && s.failed.CompareAndSwap(false, true) {
		return errors.New("fixture storage unavailable")
	}
	return s.OperationStore.Update(ctx, op, version)
}

type questionCatalog struct{ unavailable atomic.Bool }

func (c *questionCatalog) Agents(context.Context) ([]agentmodel.Agent, error) {
	if c.unavailable.Load() {
		return nil, errors.New("fixture inventory unavailable")
	}
	return []agentmodel.Agent{{AgentSnapshot: agentmodel.AgentSnapshot{ID: "fixture-agent-1234abcd", Status: agentmodel.AgentReady, Actions: []actionmodel.Action{{Name: "inspect", Status: actionmodel.Ready}}}}}, nil
}

type blockedQuestionCatalog struct {
	entered, release chan struct{}
	once             sync.Once
}

func (c *blockedQuestionCatalog) Agents(ctx context.Context) ([]agentmodel.Agent, error) {
	c.once.Do(func() { close(c.entered) })
	select {
	case <-c.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
