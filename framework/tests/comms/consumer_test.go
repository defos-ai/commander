package comms_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
)

func TestConsumerSettlesDuplicateMessagesIndependently(t *testing.T) {
	for _, concurrency := range []int{1, 3} {
		t.Run(fmt.Sprintf("concurrency_%d", concurrency), func(t *testing.T) {
			ctx, cancel := consumerContext(t)
			defer cancel(nil)
			failed := &delivery{message: messages.Message{ID: "same-id", Type: "conflicting"}}
			success := &delivery{message: messages.Message{ID: "same-id", Type: "original"}}
			other := &delivery{message: messages.Message{ID: "other-id", Type: "original"}}
			handlerErr := errors.New("conflicting payload")
			finished := errors.New("all deliveries settled")
			settled, callbacks := 0, 0
			recordSettlement := func() {
				settled++
				if settled == 3 {
					cancel(finished)
				}
			}
			consumer := newConsumer(concurrency, failed, success, other)
			consumer.ContinueOnError = func(message messages.Message, err error) bool {
				if message.Type != "conflicting" || !errors.Is(err, handlerErr) || failed.rejected != 1 {
					t.Errorf("continued without rejecting failed delivery: message=%+v error=%v", message, err)
				}
				recordSettlement()
				return true
			}
			consumer.AfterAck = func(message messages.Message) {
				if message.Type != "original" {
					t.Errorf("AfterAck called for rejected delivery: %+v", message)
				}
				callbacks++
				recordSettlement()
			}

			err := consumer.Consume(ctx, func(_ context.Context, message messages.Message) error {
				if message.Type == "conflicting" {
					return handlerErr
				}
				return nil
			})

			if !errors.Is(err, finished) || callbacks != 2 {
				t.Fatalf("consumer did not settle all copies: error=%v callbacks=%d", err, callbacks)
			}
			if failed.acked != 0 || failed.rejected != 1 || success.acked != 1 || success.rejected != 0 || other.acked != 1 {
				t.Fatalf("settlement crossed deliveries: failed=%+v success=%+v other=%+v", failed, success, other)
			}
		})
	}
}

func TestConsumerStopsOnSettlementFailure(t *testing.T) {
	for _, concurrency := range []int{1, 3} {
		for _, reject := range []bool{false, true} {
			t.Run(fmt.Sprintf("concurrency_%d/reject_%t", concurrency, reject), func(t *testing.T) {
				ctx, cancel := consumerContext(t)
				defer cancel(nil)
				transportErr := errors.New("transport settlement failed")
				d := &delivery{message: messages.Message{ID: "message"}}
				var handlerErr error
				if reject {
					handlerErr = errors.New("processing failed")
					d.rejectErr = transportErr
				} else {
					d.ackErr = transportErr
				}
				consumer := newConsumer(concurrency, d)
				consumer.ContinueOnError = func(messages.Message, error) bool {
					t.Error("ContinueOnError must not hide settlement failure")
					return true
				}
				consumer.AfterAck = func(messages.Message) { t.Error("AfterAck called after settlement failure") }

				err := consumer.Consume(ctx, func(context.Context, messages.Message) error { return handlerErr })

				if !errors.Is(err, transportErr) || (reject && !errors.Is(err, handlerErr)) {
					t.Fatalf("settlement failure lost: %v", err)
				}
				if reject && (d.rejected != 1 || d.acked != 0) || !reject && (d.acked != 1 || d.rejected != 0) {
					t.Fatalf("unexpected settlement: %+v", d)
				}
			})
		}
	}
}

func TestConsumerLeavesCanceledWorkUnsettled(t *testing.T) {
	for _, concurrency := range []int{1, 3} {
		t.Run(fmt.Sprintf("concurrency_%d", concurrency), func(t *testing.T) {
			ctx, cancel := consumerContext(t)
			defer cancel(nil)
			d := &delivery{message: messages.Message{ID: "canceled"}}
			consumer := newConsumer(concurrency, d)
			consumer.AfterAck = func(messages.Message) { t.Error("canceled work acknowledged") }
			cause := errors.New("host stopped")

			err := consumer.Consume(ctx, func(context.Context, messages.Message) error {
				cancel(cause)
				return nil
			})

			if !errors.Is(err, cause) || d.acked != 0 || d.rejected != 0 {
				t.Fatalf("canceled delivery settled: error=%v delivery=%+v", err, d)
			}
		})
	}
}

func TestConsumerBoundsInFlightWorkAndJoinsHandlers(t *testing.T) {
	ctx, cancel := consumerContext(t)
	defer cancel(nil)
	first, second, third := &delivery{}, &delivery{}, &delivery{}
	consumer := newConsumer(2, first, second, third)
	started, stopped := make(chan struct{}, 3), make(chan struct{}, 3)
	done := make(chan error, 1)

	go func() {
		done <- consumer.Consume(ctx, func(ctx context.Context, _ messages.Message) error {
			started <- struct{}{}
			<-ctx.Done()
			stopped <- struct{}{}
			return ctx.Err()
		})
	}()
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("handlers did not start")
		}
	}
	cancel(context.Canceled)
	err := <-done

	if !errors.Is(err, context.Canceled) || len(stopped) != 2 || len(started) != 0 {
		t.Fatalf("handler concurrency/lifetime incorrect: error=%v stopped=%d extra_started=%d", err, len(stopped), len(started))
	}
	if len(consumer.Subscription.(*subscription).deliveries) != 1 {
		t.Fatal("received beyond the in-flight limit")
	}
	for _, d := range []*delivery{first, second, third} {
		if d.acked != 0 || d.rejected != 0 {
			t.Fatalf("canceled delivery settled: %+v", d)
		}
	}
}

type delivery struct {
	message           messages.Message
	ackErr, rejectErr error
	acked, rejected   int
}

func (d *delivery) Message() messages.Message { return d.message }
func (d *delivery) Ack(context.Context) error {
	d.acked++
	return d.ackErr
}
func (d *delivery) Reject(context.Context) error {
	d.rejected++
	return d.rejectErr
}

type subscription struct{ deliveries chan comms.Delivery }

func (s *subscription) Next(ctx context.Context) (comms.Delivery, error) {
	select {
	case d := <-s.deliveries:
		return d, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (*subscription) Close() error { return nil }

func newConsumer(concurrency int, deliveries ...*delivery) comms.Consumer {
	s := &subscription{deliveries: make(chan comms.Delivery, len(deliveries))}
	for _, d := range deliveries {
		s.deliveries <- d
	}
	return comms.Consumer{Subscription: s, Concurrency: concurrency, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func consumerContext(t *testing.T) (context.Context, context.CancelCauseFunc) {
	t.Helper()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(stop)
	return context.WithCancelCause(ctx)
}
