package operations

import "time"

// Progress is a read-only assessment of saved work at one point in time.
// It is not persisted and does not resolve questions or continuations.
type Progress struct {
	TaskCounts         map[TaskStatus]int
	TotalTasks         int
	FinishedTasks      int
	PendingDecisions   int
	PendingQuestions   int
	LastQuestion       *Question
	RemainingQuestions int
	// Settled means a terminal operation with known outcomes for every task.
	// It does not establish agent idleness or authorize deletion of runtime state.
	Settled bool
}

func (o Operation) AssessProgress(now time.Time) Progress {
	terminal := o.Status.Terminal()
	result := Progress{TaskCounts: make(map[TaskStatus]int), TotalTasks: len(o.Tasks)}
	for _, task := range o.Tasks {
		result.TaskCounts[task.Status]++
		// Interrupted work has an unknown outcome even if FinishedAt is set.
		switch task.Status {
		case TaskSucceeded, TaskFailed, TaskCancelled:
			result.FinishedTasks++
		}
		if c := task.Continuation; !terminal && c != nil && c.Status == ContinuationPending && c.DecisionBy == DecisionModel {
			result.PendingDecisions++
		}
	}
	result.Settled = terminal && result.FinishedTasks == result.TotalTasks
	if terminal {
		return result
	}

	var latest *Question
	for i := range o.Questions {
		question := &o.Questions[i]
		if !question.Pending() {
			continue
		}
		result.PendingQuestions++
		if latest == nil || question.AskedAt.After(latest.AskedAt) || question.AskedAt.Equal(latest.AskedAt) && question.ID > latest.ID {
			latest = question
		}
	}
	if latest != nil {
		question := latest.Clone()
		result.LastQuestion = &question
		result.RemainingQuestions = result.PendingQuestions - 1
	}
	return result
}
