package comms_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/defos-ai/commander/framework/comms/messages"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/comms"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/comms"
)

func TestConsumerSettlesDuplicateMessagesIndependently(t *testing.T) {
	for _, concurrency := range []int{1, 3} {
		t.Run(fmt.Sprintf("concurrency_%d", concurrency), func(t *testing.T) {
			ctx, cancel := fixtures.ConsumerContext(t)
			defer cancel(nil)
			failed := fakes.NewDelivery(messages.Message{ID: "same-id", Type: "conflicting"})
			success := fakes.NewDelivery(messages.Message{ID: "same-id", Type: "original"})
			other := fakes.NewDelivery(messages.Message{ID: "other-id", Type: "original"})
			handlerErr := errors.New("conflicting payload")
			finished := errors.New("all deliveries settled")
			settled, callbacks := 0, 0
			recordSettlement := func() {
				settled++
				if settled == 3 {
					cancel(finished)
				}
			}
			consumer := fixtures.Consumer(concurrency, failed, success, other)
			consumer.ContinueOnError = func(message messages.Message, err error) bool {
				if message.Type != "conflicting" || !errors.Is(err, handlerErr) || failed.Rejected() != 1 {
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
			if failed.Acknowledged() != 0 || failed.Rejected() != 1 || success.Acknowledged() != 1 || success.Rejected() != 0 || other.Acknowledged() != 1 {
				t.Fatalf("settlement crossed deliveries: failed=%+v success=%+v other=%+v", failed, success, other)
			}
		})
	}
}

func TestConsumerStopsOnSettlementFailure(t *testing.T) {
	for _, concurrency := range []int{1, 3} {
		for _, reject := range []bool{false, true} {
			t.Run(fmt.Sprintf("concurrency_%d/reject_%t", concurrency, reject), func(t *testing.T) {
				ctx, cancel := fixtures.ConsumerContext(t)
				defer cancel(nil)
				transportErr := errors.New("transport settlement failed")
				d := fakes.NewDelivery(messages.Message{ID: "message"})
				var handlerErr error
				if reject {
					handlerErr = errors.New("processing failed")
					d.FailReject(transportErr)
				} else {
					d.FailAck(transportErr)
				}
				consumer := fixtures.Consumer(concurrency, d)
				consumer.ContinueOnError = func(messages.Message, error) bool {
					t.Error("ContinueOnError must not hide settlement failure")
					return true
				}
				consumer.AfterAck = func(messages.Message) { t.Error("AfterAck called after settlement failure") }

				err := consumer.Consume(ctx, func(context.Context, messages.Message) error { return handlerErr })

				if !errors.Is(err, transportErr) || (reject && !errors.Is(err, handlerErr)) {
					t.Fatalf("settlement failure lost: %v", err)
				}
				if reject && (d.Rejected() != 1 || d.Acknowledged() != 0) || !reject && (d.Acknowledged() != 1 || d.Rejected() != 0) {
					t.Fatalf("unexpected settlement: %+v", d)
				}
			})
		}
	}
}

func TestConsumerLeavesCanceledWorkUnsettled(t *testing.T) {
	for _, concurrency := range []int{1, 3} {
		t.Run(fmt.Sprintf("concurrency_%d", concurrency), func(t *testing.T) {
			ctx, cancel := fixtures.ConsumerContext(t)
			defer cancel(nil)
			d := fakes.NewDelivery(messages.Message{ID: "canceled"})
			consumer := fixtures.Consumer(concurrency, d)
			consumer.AfterAck = func(messages.Message) { t.Error("canceled work acknowledged") }
			cause := errors.New("host stopped")

			err := consumer.Consume(ctx, func(context.Context, messages.Message) error {
				cancel(cause)
				return nil
			})

			if !errors.Is(err, cause) || d.Acknowledged() != 0 || d.Rejected() != 0 {
				t.Fatalf("canceled delivery settled: error=%v delivery=%+v", err, d)
			}
		})
	}
}

func TestConsumerBoundsInFlightWorkAndJoinsHandlers(t *testing.T) {
	ctx, cancel := fixtures.ConsumerContext(t)
	defer cancel(nil)
	first, second, third := fakes.NewDelivery(messages.Message{}), fakes.NewDelivery(messages.Message{}), fakes.NewDelivery(messages.Message{})
	consumer := fixtures.Consumer(2, first, second, third)
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
	if consumer.Subscription.(*fakes.Subscription).Remaining() != 1 {
		t.Fatal("received beyond the in-flight limit")
	}
	for _, d := range []*fakes.Delivery{first, second, third} {
		if d.Acknowledged() != 0 || d.Rejected() != 0 {
			t.Fatalf("canceled delivery settled: %+v", d)
		}
	}
}
