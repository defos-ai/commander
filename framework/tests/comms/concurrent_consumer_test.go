package comms_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"commander/framework/comms"
	"commander/framework/comms/messages"
)

func TestConcurrentConsumerAllowsIndependentRequestsAndWaitsForShutdown(t *testing.T) {
	// Arrange: a blocked request precedes an independent query on the same bus.
	root := t.TempDir()
	bus := openBus(t, root)
	sub := subscribe(t, bus, "work")
	blocked, query := message(t, "long request"), message(t, "query")
	publishForConsumer(t, bus, blocked, query)
	consumer := quietConsumer(sub)
	consumer.Concurrency = 2
	ctx, cancel := context.WithCancel(consumerContext(t))
	defer cancel()
	started, stopped, acknowledged := make(chan struct{}), make(chan struct{}), make(chan struct{})
	consumer.AfterAck = func(m messages.Message) {
		if m.ID == query.ID {
			close(acknowledged)
		}
	}
	finished := make(chan error, 1)

	// Act: consume concurrently while the first request is still running.
	go func() {
		finished <- consumer.Consume(ctx, func(ctx context.Context, m messages.Message) error {
			if m.ID == blocked.ID {
				close(started)
				defer close(stopped)
				<-ctx.Done()
				return ctx.Err()
			}
			select {
			case <-started:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-acknowledged:
	case <-ctx.Done():
		t.Fatal("blocked request prevented the independent query from completing")
	}

	// Assert: only completed processing was acknowledged.
	if retainedDeliveries(t, root, query.ID) != 0 || retainedDeliveries(t, root, blocked.ID) != 1 {
		t.Fatal("each request must be acknowledged only after its own processing")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("consumer did not stop")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("consumer returned before its active request stopped")
	}
}

func TestConcurrentConsumerWaitsForDuplicateReturnedDuringReceiveCancellation(t *testing.T) {
	// Arrange: Next has registered a duplicate but returns it only when cancelled.
	root := t.TempDir()
	bus := openBus(t, root)
	sub := subscribe(t, bus, "work")
	duplicate := message(t, "same request twice")
	publishForConsumer(t, bus, duplicate, duplicate)
	duplicateRead, duplicateStarted := make(chan struct{}), make(chan struct{})
	releaseDuplicate := make(chan struct{})
	reads := 0
	tracked := &countConcurrentAcknowledgements{Subscription: &receiveOverride{
		Subscription: sub,
		next: func(ctx context.Context) (messages.Message, error) {
			m, err := sub.Next(ctx)
			reads++
			if reads == 2 && err == nil {
				close(duplicateRead)
				<-ctx.Done()
			}
			return m, err
		},
	}}
	consumer := quietConsumer(tracked)
	consumer.Concurrency = 2
	ctx, cancel := context.WithCancelCause(consumerContext(t))
	defer cancel(nil)
	finished := make(chan error, 1)
	complete := errors.New("both copies completed")
	var calls atomic.Int32
	acknowledged := 0
	consumer.AfterAck = func(messages.Message) {
		acknowledged++
		if acknowledged == 2 {
			cancel(complete)
		}
	}

	// Act: finish the first copy while the already-delivered duplicate is pending.
	go func() {
		finished <- consumer.Consume(ctx, func(ctx context.Context, _ messages.Message) error {
			if calls.Add(1) == 1 {
				select {
				case <-duplicateRead:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			close(duplicateStarted)
			select {
			case <-releaseDuplicate:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-duplicateStarted:
	case <-ctx.Done():
		t.Fatal("consumer failed to process the delivery returned during cancellation")
	}

	// Assert: the first success cannot settle either copy before the second succeeds.
	if retainedDeliveries(t, root, duplicate.ID) != 2 {
		t.Fatal("unfinished duplicate was acknowledged by the first copy")
	}
	close(releaseDuplicate)
	select {
	case err := <-finished:
		if !errors.Is(err, complete) {
			t.Fatalf("consume: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("consumer did not finish")
	}
	if calls.Load() != 2 || acknowledged != 2 || tracked.attempts != 1 || retainedDeliveries(t, root, duplicate.ID) != 0 {
		t.Fatalf("copies=%d callbacks=%d ACKs=%d", calls.Load(), acknowledged, tracked.attempts)
	}
}

func TestConcurrentConsumerRetainsRejectedDuplicatesWhileProcessingOtherMessages(t *testing.T) {
	// Arrange: one copy fails; continued processing must never settle that ID.
	root := t.TempDir()
	bus := openBus(t, root)
	sub := subscribe(t, bus, "work")
	duplicate, marker := message(t, "duplicate"), message(t, "independent query")
	publishForConsumer(t, bus, duplicate, duplicate)
	consumer := quietConsumer(sub)
	consumer.Concurrency = 3
	ctx, cancel := context.WithCancelCause(consumerContext(t))
	defer cancel(nil)
	failure, complete := errors.New("invalid occurrence"), errors.New("independent query completed")
	rejected, secondStarted, thirdCompleted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	var calls atomic.Int32
	acknowledged := make([]string, 0)
	consumer.ContinueOnError = func(_ messages.Message, err error) bool {
		if !errors.Is(err, failure) {
			return false
		}
		close(rejected)
		return true
	}
	consumer.AfterAck = func(m messages.Message) {
		acknowledged = append(acknowledged, m.ID)
		if m.ID == marker.ID {
			cancel(complete)
		}
	}

	// Act: deliver another successful copy after rejection, followed by a query.
	go func() {
		finished <- consumer.Consume(ctx, func(ctx context.Context, m messages.Message) error {
			if m.ID == marker.ID {
				select {
				case <-thirdCompleted:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			switch calls.Add(1) {
			case 1:
				select {
				case <-secondStarted:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			case 2:
				close(secondStarted)
				return failure
			default:
				close(thirdCompleted)
				return nil
			}
		})
	}()
	select {
	case <-rejected:
	case <-ctx.Done():
		t.Fatal("duplicate rejection was not observed")
	}
	publishForConsumer(t, bus, duplicate, marker)
	select {
	case err := <-finished:
		if !errors.Is(err, complete) {
			t.Fatalf("consume: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("consumer did not process the independent query")
	}

	// Assert: all copies remain, including later successes; only the query was ACKed.
	if calls.Load() != 3 || retainedDeliveries(t, root, duplicate.ID) != 3 || retainedDeliveries(t, root, marker.ID) != 0 {
		t.Fatal("continued consumption acknowledged a rejected duplicate or lost the query")
	}
	if len(acknowledged) != 1 || acknowledged[0] != marker.ID {
		t.Fatalf("acknowledged: %v", acknowledged)
	}
}

func TestConcurrentConsumerFailureCancelsAndJoinsOtherHandlers(t *testing.T) {
	for _, source := range []string{"handler", "receive", "acknowledgement"} {
		t.Run(source, func(t *testing.T) {
			// Arrange: one handler must finish cancellation cleanup before return.
			root := t.TempDir()
			bus := openBus(t, root)
			sub := subscribe(t, bus, "work")
			blocked, trigger := message(t, "blocked"), message(t, "failure")
			publishForConsumer(t, bus, blocked, trigger)
			failure := errors.New(source + " unavailable")
			started, stopping, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
			releaseCleanup := make(chan struct{})
			var release sync.Once
			defer release.Do(func() { close(releaseCleanup) })
			if source == "receive" {
				underlying, receives := sub, 0
				sub = &receiveOverride{Subscription: underlying, next: func(ctx context.Context) (messages.Message, error) {
					receives++
					if receives == 2 {
						select {
						case <-started:
							return messages.Message{}, failure
						case <-ctx.Done():
							return messages.Message{}, ctx.Err()
						}
					}
					return underlying.Next(ctx)
				}}
			} else if source == "acknowledgement" {
				sub = &failFirstAck{Subscription: sub, failure: failure}
			}
			consumer := quietConsumer(sub)
			consumer.Concurrency = 2
			ctx, cancel := context.WithCancel(consumerContext(t))
			defer cancel()
			finished := make(chan error, 1)

			// Act: trigger the failure while the other request is active.
			go func() {
				finished <- consumer.Consume(ctx, func(ctx context.Context, m messages.Message) error {
					if m.ID == blocked.ID {
						close(started)
						defer close(stopped)
						<-ctx.Done()
						close(stopping)
						<-releaseCleanup
						return ctx.Err()
					}
					select {
					case <-started:
						if source == "handler" {
							return failure
						}
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			}()
			select {
			case <-stopping:
			case <-ctx.Done():
				t.Fatal("failure did not cancel the active handler")
			}

			// Assert: return waits for cleanup, and neither delivery is acknowledged.
			select {
			case err := <-finished:
				t.Fatalf("returned before handler cleanup: %v", err)
			default:
			}
			release.Do(func() { close(releaseCleanup) })
			select {
			case err := <-finished:
				if !errors.Is(err, failure) {
					t.Fatalf("lost original failure: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("consumer did not join the handler")
			}
			select {
			case <-stopped:
			default:
				t.Fatal("handler is still running")
			}
			if retainedDeliveries(t, root, blocked.ID) != 1 || retainedDeliveries(t, root, trigger.ID) != 1 {
				t.Fatal("failed or cancelled work was acknowledged")
			}
		})
	}
}

type countConcurrentAcknowledgements struct {
	comms.Subscription
	attempts int
}

func (s *countConcurrentAcknowledgements) Ack(ctx context.Context, messageID string) error {
	s.attempts++
	return s.Subscription.Ack(ctx, messageID)
}
