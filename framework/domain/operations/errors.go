package operations

import "errors"

var (
	ErrClosed                     = errors.New("runner is closed")
	ErrFinished                   = errors.New("operation has finished")
	ErrActiveTasks                = errors.New("operation still has active tasks")
	ErrUnresolvedWork             = errors.New("operation still has unresolved automatic or operator work")
	ErrQuestionPending            = errors.New("a pending question blocks this work")
	ErrCompletionOperatorRequired = errors.New("mission completion requires an operator")
	ErrNotRunning                 = errors.New("task has no live execution in this runner")
)

var (
	ErrUnknownOperation        = errors.New("unknown operation")
	ErrUnknownTask             = errors.New("unknown task")
	ErrUnknownQuestion         = errors.New("unknown question")
	ErrQuestionAnswered        = errors.New("question is already answered")
	ErrQuestionOperatorWaiting = errors.New("question is waiting for an operator answer; model answers require fallback_at to pass")
	ErrContinuationConflict    = errors.New("continuation is not pending at the supplied version; refresh get_operation")
	ErrTaskLimitExceeded       = errors.New("operation task limit exceeded")
	ErrOperationConflict       = errors.New("operation conflict")
	ErrWriteUncertain          = errors.New("operation write durability is uncertain")
)
