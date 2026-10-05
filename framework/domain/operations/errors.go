package operations

import "errors"

var (
	ErrClosed          = errors.New("runner is closed")
	ErrFinished        = errors.New("operation has finished")
	ErrActiveTasks     = errors.New("operation still has active tasks")
	ErrUnresolvedWork  = errors.New("operation still has unresolved automatic or operator work")
	ErrQuestionPending = errors.New("a pending question blocks this work")
	ErrNotRunning      = errors.New("task has no live execution in this runner")
)

var (
	ErrUnknownOperation     = errors.New("unknown operation")
	ErrUnknownTask          = errors.New("unknown task")
	ErrUnknownQuestion      = errors.New("unknown question")
	ErrQuestionAnswered     = errors.New("question is already answered")
	ErrQuestionExpired      = errors.New("question has expired")
	ErrContinuationConflict = errors.New("continuation is not pending at the supplied version; refresh get_operation")
	ErrTaskLimitExceeded    = errors.New("operation task limit exceeded")
	ErrOperationConflict    = errors.New("operation conflict")
	ErrWriteUncertain       = errors.New("operation write durability is uncertain")
)
