package testing

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
)

var ErrBusy = errors.New("test work has not settled")

// StateCleaner checks saved work before clearing public test data. The check is
// not isolation from other processes: callers must stop submitting work and run
// cleanup serially between settled tests. Runtime state and the bus are retained.
type StateCleaner struct {
	operations operations.OperationStore
	agents     agents.AgentStore
	cleaners   []TestCleaner
	cleaning   atomic.Bool
}

var _ TestCleaner = (*StateCleaner)(nil)

func NewStateCleaner(operationStore operations.OperationStore, agentStore agents.AgentStore, cleaners ...TestCleaner) (*StateCleaner, error) {
	if operationStore == nil || agentStore == nil || len(cleaners) == 0 {
		return nil, errors.New("test state cleanup requires operation and agent stores and provider cleaners")
	}
	for _, cleaner := range cleaners {
		if cleaner == nil {
			return nil, errors.New("test state cleanup requires non-nil provider cleaners")
		}
	}
	return &StateCleaner{operations: operationStore, agents: agentStore, cleaners: slices.Clone(cleaners)}, nil
}

func (c *StateCleaner) Clean() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.CleanContext(ctx)
}

func (c *StateCleaner) CleanContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("test state cleanup requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.cleaning.CompareAndSwap(false, true) {
		return ErrBusy
	}
	defer c.cleaning.Store(false)
	if err := c.checkSettled(ctx); err != nil {
		return err
	}
	for _, cleaner := range c.cleaners {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := CleanContext(ctx, cleaner); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (c *StateCleaner) checkSettled(ctx context.Context) error {
	items, err := c.operations.List(ctx)
	if err != nil {
		return fmt.Errorf("read operations before test cleanup: %w", err)
	}
	for _, operation := range items {
		if !operation.Status.Terminal() {
			return fmt.Errorf("operation %s is %s: %w", operation.ID, operation.Status, ErrBusy)
		}
		for _, task := range operation.Tasks {
			switch task.Status {
			case operations.TaskQueued, operations.TaskRunning, operations.TaskInterrupted:
				return fmt.Errorf("task %s is %s: %w", task.ID, task.Status, ErrBusy)
			}
		}
		for _, question := range operation.Questions {
			if question.Pending() {
				return fmt.Errorf("question %s is pending: %w", question.ID, ErrBusy)
			}
		}
	}
	records, err := c.agents.List(ctx)
	if err != nil {
		return fmt.Errorf("read agents before test cleanup: %w", err)
	}
	for _, agent := range records {
		if agent.Work != nil {
			return fmt.Errorf("agent %s has current work: %w", agent.ID, ErrBusy)
		}
	}
	return ctx.Err()
}
