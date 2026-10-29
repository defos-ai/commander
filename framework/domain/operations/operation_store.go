package operations

import (
	"context"
	"encoding/json"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
)

// Implementations retain and return independent copies. Mutations apply atomically
// to the latest operation. Errors other than ErrWriteUncertain leave it unchanged.
// ErrWriteUncertain means commit outcome or durability is unconfirmed. Methods
// returning an Operation provide the attempted snapshot, which may not be visible.
// Pause new work until recovery rereads and reconciles persisted state.
type OperationStore interface {
	Create(context.Context, Operation) error
	Get(context.Context, string) (Operation, error)
	List(context.Context) ([]Operation, error)
	// RefreshPreflight assesses the saved mission, queued optional tasks and affinity
	// against the supplied inventory. Readiness, version, status and event commit
	// together; identical readiness returns the saved operation without writing.
	RefreshPreflight(ctx context.Context, operationID string, records []agents.Agent) (Operation, error)
	// Recover validates saved work and records recovery without dispatching work.
	// Pending questions and their fallback times remain unchanged. Terminal
	// operations without recoverable tasks return without writing.
	Recover(ctx context.Context, operationID string, now time.Time) (Operation, error)
	// Task queries are scoped to one operation. A missing operation returns
	// ErrUnknownOperation; GetTask returns ErrUnknownTask when its task is absent.
	GetTask(ctx context.Context, operationID, taskID string) (Task, error)
	// ListTasks returns up to limit tasks in assignment order after afterTaskID.
	// An empty cursor starts the first page; an unknown cursor returns ErrUnknownTask.
	// The limit must be 1..1000. Status updates do not change assignment order.
	ListTasks(ctx context.Context, operationID, afterTaskID string, limit int) ([]Task, error)
	// ListEvents returns up to limit events in ascending sequence order, strictly
	// after afterSequence. The limit must be 1..1000. A cursor at or beyond the
	// latest sequence returns an empty page; an unknown operation returns ErrUnknownOperation.
	ListEvents(ctx context.Context, operationID string, afterSequence uint64, limit int) ([]Event, error)
	// QueueTask saves a direct assignment, event, and operation status together.
	// Replaying the same task ID and assignment returns the saved state unchanged,
	// including after dispatch; a conflicting assignment returns ErrOperationConflict.
	QueueTask(ctx context.Context, operationID string, task Task) (Operation, error)
	// DispatchTask atomically claims a queued task using the latest operation,
	// respecting questions, preflight, and the saved same-agent binding. It preserves
	// the queued command ID and saves the running task, event, status, and timestamp
	// together. A competing claim returns ErrOperationConflict without dispatching.
	DispatchTask(ctx context.Context, operationID string, dispatch TaskDispatch) (Operation, error)
	// FinishTask saves a running or interrupted task's outcome together with its
	// continuation, automatic follow-ups, events, and operation status. Repeated
	// outcomes return the saved snapshot unchanged; conflicting final outcomes
	// return ErrOperationConflict. Late results preserve terminal operation status.
	FinishTask(ctx context.Context, operationID string, result TaskResult) (Operation, error)
	// ResolveContinuation checks the current decision version and commits the
	// resolution, follow-up tasks, events, and operation status together. A stale
	// version cannot create another branch, including when retrying a saved decision.
	ResolveContinuation(ctx context.Context, operationID, taskID string, version uint64, next []actions.NextAction) (Operation, error)
	// AskQuestion saves the pending question and its deadline with status, event,
	// and any linked decision handoff. Linked questions require the current version;
	// duplicate question IDs return ErrOperationConflict without writing.
	AskQuestion(ctx context.Context, operationID string, question Question, version uint64) (Operation, error)
	// AnswerQuestion saves the answer, origin and linked decision together. Model
	// answers before FallbackAt return ErrQuestionOperatorWaiting without writing.
	// Operator answers remain valid before and after FallbackAt.
	AnswerQuestion(ctx context.Context, operationID, questionID, answer string, answeredBy AnswerOrigin) (Operation, error)
	// Complete rechecks eligibility and saves output, report reference, resolved
	// model continuations, status, and event together. The caller must persist
	// the immutable report first. Terminal operations return ErrFinished unchanged.
	Complete(ctx context.Context, operationID, reportID string, output json.RawMessage) (Operation, error)
	// Fail saves the reason, terminal status, queued task cancellations, and event
	// together. Running tasks can still finish; terminal operations return ErrFinished.
	Fail(ctx context.Context, operationID, reason string) (Operation, error)
	// Cancel saves terminal status, queued task cancellations, and its event together.
	// Dispatched tasks retain their state and may finish later. Terminal operations
	// return ErrFinished without changing the saved state.
	Cancel(ctx context.Context, operationID string) (Operation, error)
}
