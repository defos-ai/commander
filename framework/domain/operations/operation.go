package operations

import (
	"encoding/json"
	"time"

	actionmodel "commander/framework/domain/actions"
	"commander/framework/domain/missions"
)

type Status string

const (
	Planning  Status = "planning"
	Running   Status = "running"
	Waiting   Status = "waiting"
	Completed Status = "completed"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
)

func (s Status) Terminal() bool { return s == Completed || s == Failed || s == Cancelled }

type TaskStatus string

const (
	TaskQueued      TaskStatus = "queued"
	TaskRunning     TaskStatus = "running"
	TaskSucceeded   TaskStatus = "succeeded"
	TaskFailed      TaskStatus = "failed"
	TaskCancelled   TaskStatus = "cancelled"
	TaskInterrupted TaskStatus = "interrupted"
)

// Task is one externally selected action assignment. Result is retained even if
// the operation is cancelled while its already-dispatched work finishes.
type Task struct {
	ID           string                    `json:"id"`
	OperationID  string                    `json:"operation_id"`
	ParentTaskID string                    `json:"parent_task_id,omitempty"`
	StepID       string                    `json:"step_id,omitempty"`
	AgentID      string                    `json:"agent_id"`
	ActionName   string                    `json:"action_name"`
	CommandID    string                    `json:"command_id,omitempty"`
	DeadlineAt   *time.Time                `json:"deadline_at,omitempty"`
	Input        json.RawMessage           `json:"input,omitempty"`
	Status       TaskStatus                `json:"status"`
	Result       *actionmodel.ActionOutput `json:"result,omitempty"`
	Continuation *Continuation             `json:"continuation,omitempty"`
	Error        string                    `json:"error,omitempty"`
	StartedAt    time.Time                 `json:"started_at"`
	FinishedAt   *time.Time                `json:"finished_at,omitempty"`
}

type QuestionStatus string

const (
	QuestionPending  QuestionStatus = "pending"
	QuestionAnswered QuestionStatus = "answered"
	QuestionExpired  QuestionStatus = "expired"
)

// A pending question blocks its StepID. An empty StepID blocks all new tasks.
// Work already assigned continues and its results remain available.
type Question struct {
	ID         string         `json:"id"`
	Status     QuestionStatus `json:"status"`
	StepID     string         `json:"step_id,omitempty"`
	TaskID     string         `json:"task_id,omitempty"`
	Text       string         `json:"text"`
	Options    []string       `json:"options,omitempty"`
	Answer     string         `json:"answer,omitempty"`
	AskedAt    time.Time      `json:"asked_at"`
	AnsweredAt *time.Time     `json:"answered_at,omitempty"`
	ExpiresAt  time.Time      `json:"expires_at"`
}

// Pending questions block new work until an answer or durable expiration.
func (q Question) Pending() bool { return q.Status == QuestionPending }

type EventType string

const (
	OperationStarted          EventType = "operation.started"
	PreflightUpdated          EventType = "operation.preflight_updated"
	TaskDispatched            EventType = "task.dispatched"
	ContinuationReady         EventType = "continuation.ready"
	ContinuationResolvedEvent EventType = "continuation.resolved"
	TaskAssigned              EventType = "task.assigned"
	TaskFinished              EventType = "task.finished"
	TaskInterruptedEvent      EventType = "task.interrupted"
	OperationRecovered        EventType = "operation.recovered"
	QuestionAsked             EventType = "question.asked"
	QuestionAnsweredEvent     EventType = "question.answered"
	QuestionExpiredEvent      EventType = "question.expired"
	OperationCompleted        EventType = "operation.completed"
	OperationFailed           EventType = "operation.failed"
	OperationCancelled        EventType = "operation.cancelled"
)

// Event is a cursor-addressable update. TaskID and QuestionID identify the
// corresponding record in Operation; large action results remain artifact refs.
type Event struct {
	Sequence    uint64    `json:"sequence"`
	OperationID string    `json:"operation_id"`
	Type        EventType `json:"type"`
	Status      Status    `json:"status"`
	TaskID      string    `json:"task_id,omitempty"`
	QuestionID  string    `json:"question_id,omitempty"`
	Message     string    `json:"message,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type Operation struct {
	ID        string           `json:"id"`
	Mission   missions.Mission `json:"mission"`
	Input     json.RawMessage  `json:"input"`
	Status    Status           `json:"status"`
	Preflight *Preflight       `json:"preflight,omitempty"`
	Tasks     []Task           `json:"tasks"`
	Questions []Question       `json:"questions"`
	Output    json.RawMessage  `json:"output,omitempty"`
	ReportID  string           `json:"report_id,omitempty"`
	Error     string           `json:"error,omitempty"`
	Events    []Event          `json:"events"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

func (o Operation) Clone() Operation {
	if o.Preflight != nil {
		preflight := o.Preflight.Clone()
		o.Preflight = &preflight
	}
	o.Mission = o.Mission.Clone()
	o.Input = append(json.RawMessage(nil), o.Input...)
	o.Output = append(json.RawMessage(nil), o.Output...)
	o.Tasks = append([]Task{}, o.Tasks...)
	for i := range o.Tasks {
		t := &o.Tasks[i]
		t.Input = append(json.RawMessage(nil), t.Input...)
		if t.FinishedAt != nil {
			value := *t.FinishedAt
			t.FinishedAt = &value
		}
		if t.DeadlineAt != nil {
			value := *t.DeadlineAt
			t.DeadlineAt = &value
		}
		if t.Continuation != nil {
			continuation := t.Continuation.Clone()
			t.Continuation = &continuation
		}
		if t.Result != nil {
			result := *t.Result
			result.NextActions = actionmodel.CloneNextActions(result.NextActions)
			result.Output = append(json.RawMessage(nil), result.Output...)
			result.Artifacts = append(result.Artifacts[:0:0], result.Artifacts...)
			if result.OutputArtifact != nil {
				ref := *result.OutputArtifact
				result.OutputArtifact = &ref
			}
			t.Result = &result
		}
	}
	o.Questions = append([]Question{}, o.Questions...)
	for i := range o.Questions {
		q := &o.Questions[i]
		q.Options = append([]string(nil), q.Options...)
		if q.AnsweredAt != nil {
			value := *q.AnsweredAt
			q.AnsweredAt = &value
		}
	}
	o.Events = append([]Event{}, o.Events...)
	return o
}
