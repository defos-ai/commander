package comms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/defos-ai/commander/framework/comms/messages"
)

// Handler returns nil when processing is complete and the delivery can be
// acknowledged. A business failure can be handled successfully by recording or
// publishing its failure outcome. Processing failures must return an error.
type Handler func(context.Context, messages.Message) error

// Only one Consume call may read a subscription at a time. The caller owns
// cancellation and subscription lifetime; handlers own business idempotency.
type Consumer struct {
	Subscription Subscription
	Logger       *slog.Logger

	// Concurrency bounds simultaneous handlers. Zero or one preserves FIFO
	// processing. Higher values require concurrency-safe handlers. Callbacks remain
	// serialized; each delivery is settled independently.
	Concurrency int

	// ContinueOnError allows consumption to continue after rejecting a failed
	// delivery. Receive, settlement and cancellation errors always end consumption.
	// By default, handler errors do too.
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
		delivery, err := c.Subscription.Next(ctx)
		if err != nil {
			if cause := context.Cause(ctx); cause != nil {
				return cause
			}
			return err
		}
		if err := context.Cause(ctx); err != nil {
			return err
		}
		message := delivery.Message()
		consumerMessageLogger(logger, message).DebugContext(ctx, "message received")
		if err := c.settle(ctx, delivery, handler(ctx, message), logger); err != nil {
			return err
		}
	}
}

func (c Consumer) settle(ctx context.Context, delivery Delivery, handlerErr error, logger *slog.Logger) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	message := delivery.Message()
	messageLogger := consumerMessageLogger(logger, message)
	if handlerErr != nil {
		if err := delivery.Reject(ctx); err != nil {
			messageLogger.ErrorContext(ctx, "message rejection failed", "error_type", fmt.Sprintf("%T", err))
			return errors.Join(handlerErr, fmt.Errorf("reject delivery: %w", err))
		}
		messageLogger.ErrorContext(ctx, "message rejected", "error_type", fmt.Sprintf("%T", handlerErr))
		if c.ContinueOnError != nil && c.ContinueOnError(message, handlerErr) {
			return nil
		}
		return handlerErr
	}
	if err := delivery.Ack(ctx); err != nil {
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
		delivery Delivery
		err      error
	}
	completed := make(chan outcome, c.Concurrency)
	var receiving chan outcome
	unsettled := 0
	for {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		if receiving == nil && unsettled < c.Concurrency {
			next := make(chan outcome, 1)
			receiving = next
			workers.Go(func() {
				delivery, err := c.Subscription.Next(ctx)
				next <- outcome{delivery, err}
			})
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case next := <-receiving:
			receiving = nil
			if err := context.Cause(ctx); err != nil {
				return err
			}
			if next.err != nil {
				return next.err
			}
			unsettled++
			message := next.delivery.Message()
			consumerMessageLogger(logger, message).DebugContext(ctx, "message received")
			workers.Go(func() {
				err := context.Cause(ctx)
				if err == nil {
					err = handler(ctx, message)
				}
				completed <- outcome{next.delivery, err}
			})
		case finished := <-completed:
			if err := c.settle(ctx, finished.delivery, finished.err, logger); err != nil {
				return err
			}
			unsettled--
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
