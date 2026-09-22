package comms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"commander/framework/comms/messages"
)

// Handler returns nil when processing is complete and the delivery can be
// acknowledged. A business failure can be handled successfully by recording or
// publishing its failure outcome. Processing failures must return an error.
type Handler func(context.Context, messages.Message) error

// Consumer receives messages and acknowledges successful handling. The caller
// owns cancellation and subscription lifetime. Completion always runs through a
// handler; Consumer alone acknowledges successful processing.
// Only one Consume call may read a subscription at a time. Consumer holds no
// deduplication state; handlers own business idempotency.
type Consumer struct {
	Subscription Subscription
	Logger       *slog.Logger

	// Concurrency bounds simultaneous handlers. Zero or one preserves FIFO
	// processing. Higher values require concurrency-safe handlers. Callbacks remain
	// serialized; copies of one ID are acknowledged only after all have succeeded.
	Concurrency int

	// ContinueOnError allows consumption to continue after a handler fails.
	// The failed delivery remains unacknowledged. Receive, ACK and cancellation
	// errors always end consumption. By default, handler errors do too.
	ContinueOnError func(messages.Message, error) bool

	// AfterAck runs after successful acknowledgement. Use it for local bookkeeping;
	// required processing belongs in the handler, before ACK.
	AfterAck func(messages.Message)
}

// Consume processes messages until cancellation or an error, then waits for
// active handlers. It does not close the subscription or retry failed deliveries.
func (c Consumer) Consume(ctx context.Context, handler Handler) error {
	if c.Subscription == nil || handler == nil || c.Concurrency < 0 {
		return errors.New("consumer requires a subscription, handler, and nonnegative concurrency")
	}
	logger := c.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if c.Concurrency > 1 {
		return c.consumeConcurrent(ctx, handler, logger)
	}
	for {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		message, err := c.Subscription.Next(ctx)
		if err != nil {
			if cause := context.Cause(ctx); cause != nil {
				return cause
			}
			return err
		}
		if err := c.consumeMessage(ctx, message, handler, logger); err != nil {
			return err
		}
	}
}

func (c Consumer) consumeMessage(ctx context.Context, message messages.Message, handler Handler, logger *slog.Logger) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	messageLogger := consumerMessageLogger(logger, message)
	messageLogger.DebugContext(ctx, "message received")
	if err := handler(ctx, message); err != nil {
		messageLogger.ErrorContext(ctx, "message rejected; delivery retained", "error_type", fmt.Sprintf("%T", err))
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		if c.ContinueOnError != nil && c.ContinueOnError(message, err) {
			return nil
		}
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := c.Subscription.Ack(ctx, message.ID); err != nil {
		messageLogger.ErrorContext(ctx, "message acknowledgement failed", "error_type", fmt.Sprintf("%T", err))
		return err
	}
	messageLogger.DebugContext(ctx, "message acknowledged")
	if c.AfterAck != nil {
		c.AfterAck(message)
	}
	return nil
}

func (c Consumer) consumeConcurrent(ctx context.Context, handler Handler, logger *slog.Logger) (result error) {
	ctx, cancel := context.WithCancelCause(ctx)
	var workers sync.WaitGroup
	defer func() {
		cancel(result)
		workers.Wait()
	}()
	type outcome struct {
		message messages.Message
		err     error
	}
	type group struct {
		pending  int
		rejected bool
		done     []messages.Message
	}
	groups := make(map[string]*group)
	completed := make(chan outcome, c.Concurrency)
	var receiving chan outcome
	var stopReceive context.CancelFunc
	unsettled := 0

	// Registration belongs to the coordinator. Next may return a delivery even
	// as its receive context is cancelled; that copy must count before any ACK.
	accept := func(next outcome) error {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		if next.err != nil {
			return next.err
		}
		message := next.message
		g := groups[message.ID]
		if g == nil {
			g = &group{}
			groups[message.ID] = g
		}
		g.pending++
		unsettled++
		consumerMessageLogger(logger, message).DebugContext(ctx, "message received")
		workers.Go(func() {
			err := context.Cause(ctx)
			if err == nil {
				err = handler(ctx, message)
			}
			completed <- outcome{message, err}
		})
		return nil
	}
	for {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		if receiving == nil && unsettled < c.Concurrency {
			receiveCtx, stop := context.WithCancel(ctx)
			stopReceive = stop
			next := make(chan outcome, 1)
			receiving = next
			workers.Go(func() {
				message, err := c.Subscription.Next(receiveCtx)
				next <- outcome{message, err}
			})
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case next := <-receiving:
			stopReceive()
			receiving = nil
			if err := accept(next); err != nil {
				return err
			}
		case finished := <-completed:
			if err := context.Cause(ctx); err != nil {
				return err
			}
			g := groups[finished.message.ID]
			g.pending--
			if finished.err != nil {
				consumerMessageLogger(logger, finished.message).ErrorContext(ctx, "message rejected; delivery retained", "error_type", fmt.Sprintf("%T", finished.err))
				if err := context.Cause(ctx); err != nil {
					return err
				}
				if c.ContinueOnError == nil || !c.ContinueOnError(finished.message, finished.err) {
					return finished.err
				}
				g.rejected = true
				unsettled -= len(g.done) + 1
				g.done = nil
			} else if g.rejected {
				unsettled--
			} else {
				g.done = append(g.done, finished.message)
			}

			// Ack(ID) covers every copy returned by Next. Stop and join the sole
			// receive before inspecting complete groups, including any raced copy.
			if receiving != nil {
				stopReceive()
				next := <-receiving
				receiving = nil
				if !errors.Is(next.err, context.Canceled) {
					if err := accept(next); err != nil {
						return err
					}
				}
			}
			for messageID, g := range groups {
				if g.pending != 0 || g.rejected {
					continue
				}
				if err := context.Cause(ctx); err != nil {
					return err
				}
				if err := c.Subscription.Ack(ctx, messageID); err != nil {
					consumerMessageLogger(logger, g.done[0]).ErrorContext(ctx, "message acknowledgement failed", "error_type", fmt.Sprintf("%T", err))
					return err
				}
				delete(groups, messageID)
				unsettled -= len(g.done)
				for _, message := range g.done {
					consumerMessageLogger(logger, message).DebugContext(ctx, "message acknowledged")
					if c.AfterAck != nil {
						c.AfterAck(message)
					}
				}
			}
		}
	}
}

func consumerMessageLogger(logger *slog.Logger, message messages.Message) *slog.Logger {
	logger = logger.With("message_id", message.ID, "message_type", message.Type,
		"agent_id", message.AgentID, "correlation_id", message.CorrelationID)
	if message.AdapterID != "" {
		logger = logger.With("adapter_id", message.AdapterID)
	}
	if message.Work != (messages.WorkContext{}) {
		logger = logger.With("operation_id", message.Work.OperationID,
			"task_id", message.Work.TaskID, "step_id", message.Work.StepID)
	}
	return logger
}
