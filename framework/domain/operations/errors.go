package operations

import "errors"

var (
	ErrClosed          = errors.New("runner is closed")
	ErrFinished        = errors.New("operation has finished")
	ErrQuestionPending = errors.New("a pending question blocks this work")
	ErrNotRunning      = errors.New("task has no live execution in this runner")
)

var (
	ErrUnknownOperation  = errors.New("unknown operation")
	ErrUnknownTask       = errors.New("unknown task")
	ErrOperationConflict = errors.New("operation conflict")
	ErrWriteUncertain    = errors.New("operation write durability is uncertain")
)
