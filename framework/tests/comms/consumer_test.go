package comms_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"commander/framework/comms"
	"commander/framework/comms/messages"
)

func TestConsumerConsumesAndAcknowledgesMessagesAfterHandling(t *testing.T) {
	// Arrange
	root := t.TempDir()
	bus := openBus(t, root)
	sub := subscribe(t, bus, "work")
	want := []messages.Message{message(t, "first result"), message(t, "second result")}
	publishForConsumer(t, bus, want...)
	consumer := quietConsumer(sub)
	resultRoot := t.TempDir()
	ctx, cancel := context.WithCancelCause(consumerContext(t))
	defer cancel(nil)
	finished := errors.New("all results handled")
	var handled, acknowledged []string
	consumer.AfterAck = func(received messages.Message) {
		if retainedDeliveries(t, root, received.ID) != 0 {
			t.Fatal("acknowledged callback ran before delivery was removed")
		}
		result, err := os.ReadFile(filepath.Join(resultRoot, received.ID))
		if err != nil || string(result) != string(received.Payload) {
			t.Fatalf("persisted result: %q, %v", result, err)
		}
		acknowledged = append(acknowledged, received.ID)
		if len(acknowledged) == len(want) {
			cancel(finished)
		}
	}

	// Act
	err := consumer.Consume(ctx, func(_ context.Context, received messages.Message) error {
		if retainedDeliveries(t, root, received.ID) != 1 {
			t.Fatal("delivery must remain until handling completes")
		}
		handled = append(handled, received.ID)
		return os.WriteFile(filepath.Join(resultRoot, received.ID), received.Payload, 0o600)
	})

	// Assert
	if !errors.Is(err, finished) {
		t.Fatalf("consume result: %v", err)
	}
	wantIDs := []string{want[0].ID, want[1].ID}
	if !reflect.DeepEqual(handled, wantIDs) || !reflect.DeepEqual(acknowledged, wantIDs) {
		t.Fatalf("messages must be handled and acknowledged in order: handled=%v acknowledged=%v", handled, acknowledged)
	}
}

func TestConsumerRetainsFailedDeliveryWhileContinuing(t *testing.T) {
	for _, failure := range []string{"result publication unavailable", "invalid command payload"} {
		t.Run(failure, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			bus := openBus(t, root)
			sub := subscribe(t, bus, "work")
			first, second := message(t, "failed"), message(t, "next")
			publishForConsumer(t, bus, first, second)
			consumer := quietConsumer(sub)
			handlingFailure := errors.New(failure)
			ctx, cancel := context.WithCancelCause(consumerContext(t))
			defer cancel(nil)
			finished := errors.New("next delivery handled")
			var rejected, acknowledged []string
			consumer.ContinueOnError = func(m messages.Message, err error) bool {
				if !errors.Is(err, handlingFailure) {
					t.Fatalf("unexpected handler error: %v", err)
				}
				rejected = append(rejected, m.ID)
				return true
			}
			consumer.AfterAck = func(m messages.Message) {
				acknowledged = append(acknowledged, m.ID)
				cancel(finished)
			}

			// Act
			err := consumer.Consume(ctx, func(_ context.Context, m messages.Message) error {
				if m.ID == first.ID {
					return handlingFailure
				}
				return nil
			})
			retainedFirst := retainedDeliveries(t, root, first.ID)
			retainedSecond := retainedDeliveries(t, root, second.ID)

			// Assert
			if !errors.Is(err, finished) {
				t.Fatalf("consume result: %v", err)
			}
			if retainedFirst != 1 || retainedSecond != 0 {
				t.Fatalf("retained deliveries: failed=%d handled=%d", retainedFirst, retainedSecond)
			}
			if !reflect.DeepEqual(rejected, []string{first.ID}) || !reflect.DeepEqual(acknowledged, []string{second.ID}) {
				t.Fatalf("error and acknowledgement callbacks: rejected=%v acknowledged=%v", rejected, acknowledged)
			}
		})
	}
}

func TestConsumerStopsAndRetainsDeliveryOnStorageFailure(t *testing.T) {
	// Arrange
	root := t.TempDir()
	bus := openBus(t, root)
	sub := subscribe(t, bus, "work")
	m, next := message(t, "failed save"), message(t, "later work")
	publishForConsumer(t, bus, m, next)
	consumer := quietConsumer(sub)
	storageFailure := errors.New("storage unavailable")
	calls := 0

	// Act
	err := consumer.Consume(consumerContext(t), func(context.Context, messages.Message) error {
		calls++
		return fmt.Errorf("save outcome: %w", storageFailure)
	})

	// Assert
	if !errors.Is(err, storageFailure) || calls != 1 {
		t.Fatalf("handler failure: error=%v calls=%d", err, calls)
	}
	if retainedDeliveries(t, root, m.ID) != 1 || retainedDeliveries(t, root, next.ID) != 1 {
		t.Fatal("storage failure must retain the delivery and stop before processing later work")
	}
}

func TestConsumerRetainsDeliveryWhenHandlingIsCancelled(t *testing.T) {
	for _, handlerErr := range []error{nil, context.Canceled} {
		t.Run(fmt.Sprint(handlerErr), func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			bus := openBus(t, root)
			sub := subscribe(t, bus, "work")
			m := message(t, "cancelled work")
			publishForConsumer(t, bus, m)
			consumer := quietConsumer(sub)
			ctx, cancel := context.WithCancelCause(consumerContext(t))
			defer cancel(nil)
			shutdown := errors.New("service shutting down")
			errorCallbacks, acknowledged := 0, 0
			consumer.ContinueOnError = func(messages.Message, error) bool {
				errorCallbacks++
				return true
			}
			consumer.AfterAck = func(messages.Message) { acknowledged++ }

			// Act
			err := consumer.Consume(ctx, func(context.Context, messages.Message) error {
				cancel(shutdown)
				return handlerErr
			})

			// Assert
			if !errors.Is(err, shutdown) {
				t.Fatalf("lost cancellation cause: %v", err)
			}
			if retainedDeliveries(t, root, m.ID) != 1 {
				t.Fatal("cancellation during handling must retain the delivery")
			}
			if errorCallbacks != 0 || acknowledged != 0 {
				t.Fatalf("cancellation must bypass callbacks: error=%d acknowledged=%d", errorCallbacks, acknowledged)
			}
		})
	}
}

func TestConsumerStopsAndRetainsDeliveryOnAcknowledgementFailure(t *testing.T) {
	// Arrange
	root := t.TempDir()
	bus := openBus(t, root)
	sub := subscribe(t, bus, "work")
	m := message(t, "save once")
	publishForConsumer(t, bus, m)
	ackFailure := errors.New("acknowledgement storage unavailable")
	faulty := &failFirstAck{Subscription: sub, failure: ackFailure}
	consumer := quietConsumer(faulty)
	calls := 0
	errorCallbacks, acknowledged := 0, 0
	consumer.ContinueOnError = func(messages.Message, error) bool {
		errorCallbacks++
		return true
	}
	consumer.AfterAck = func(messages.Message) { acknowledged++ }

	// Act
	err := consumer.Consume(consumerContext(t), func(context.Context, messages.Message) error {
		calls++
		return nil
	})
	retainedAfterFailure := retainedDeliveries(t, root, m.ID)

	// Assert
	if !errors.Is(err, ackFailure) || retainedAfterFailure != 1 {
		t.Fatalf("failed acknowledgement: error=%v retained=%d", err, retainedAfterFailure)
	}
	if calls != 1 || faulty.attempts != 1 {
		t.Fatalf("ACK failure must not repeat processing: handler calls=%d ack attempts=%d", calls, faulty.attempts)
	}
	if errorCallbacks != 0 || acknowledged != 0 {
		t.Fatalf("ACK failure must stop without callbacks: error=%d acknowledged=%d", errorCallbacks, acknowledged)
	}
}

func TestConsumerPassesLaterDuplicateToIdempotentHandler(t *testing.T) {
	// Arrange
	root := t.TempDir()
	bus := openBus(t, root)
	sub := subscribe(t, bus, "work")
	want := message(t, "same logical request")
	publishForConsumer(t, bus, want)
	consumer := quietConsumer(sub)
	ctx, cancel := context.WithCancelCause(consumerContext(t))
	defer cancel(nil)
	finished := errors.New("both deliveries handled")
	applied := make(map[string]bool)
	calls, effects, acknowledged := 0, 0, 0
	consumer.AfterAck = func(received messages.Message) {
		if retainedDeliveries(t, root, received.ID) != 0 {
			t.Fatal("delivery must be removed before publishing its duplicate")
		}
		acknowledged++
		if acknowledged == 1 {
			publishForConsumer(t, bus, want)
		} else {
			cancel(finished)
		}
	}
	handler := func(_ context.Context, m messages.Message) error {
		calls++
		if !applied[m.ID] {
			applied[m.ID] = true
			effects++
		}
		return nil
	}

	// Act
	err := consumer.Consume(ctx, handler)

	// Assert
	if !errors.Is(err, finished) {
		t.Fatalf("consume result: %v", err)
	}
	if calls != 2 || effects != 1 {
		t.Fatalf("handler owns idempotency: deliveries=%d business effects=%d", calls, effects)
	}
	if retainedDeliveries(t, root, want.ID) != 0 {
		t.Fatal("both occurrences must be acknowledged")
	}
}

func TestConsumerStopsOnReceiveFailure(t *testing.T) {
	// Arrange
	bus := openBus(t, t.TempDir())
	sub := subscribe(t, bus, "work")
	failure := errors.New("broker receive unavailable")
	consumer := quietConsumer(&receiveOverride{
		Subscription: sub,
		next: func(context.Context) (messages.Message, error) {
			return messages.Message{}, failure
		},
	})
	handled, errorCallbacks, acknowledged := 0, 0, 0
	consumer.ContinueOnError = func(messages.Message, error) bool {
		errorCallbacks++
		return true
	}
	consumer.AfterAck = func(messages.Message) { acknowledged++ }

	// Act
	err := consumer.Consume(consumerContext(t), func(context.Context, messages.Message) error {
		handled++
		return nil
	})

	// Assert
	if !errors.Is(err, failure) {
		t.Fatalf("lost receive failure: %v", err)
	}
	if handled != 0 || errorCallbacks != 0 || acknowledged != 0 {
		t.Fatalf("receive failure must bypass processing: handled=%d error=%d acknowledged=%d", handled, errorCallbacks, acknowledged)
	}
}

func TestConsumerPreservesCancellationCauseWhileReceiving(t *testing.T) {
	// Arrange
	bus := openBus(t, t.TempDir())
	sub := subscribe(t, bus, "work")
	waiting := make(chan struct{})
	consumer := quietConsumer(&receiveOverride{
		Subscription: sub,
		next: func(ctx context.Context) (messages.Message, error) {
			close(waiting)
			return sub.Next(ctx)
		},
	})
	deadline, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	ctx, cancel := context.WithCancelCause(deadline)
	defer cancel(nil)
	shutdown := errors.New("host stopped")
	handled, errorCallbacks, acknowledged := 0, 0, 0
	consumer.ContinueOnError = func(messages.Message, error) bool {
		errorCallbacks++
		return true
	}
	consumer.AfterAck = func(messages.Message) { acknowledged++ }

	// Act
	go func() {
		select {
		case <-waiting:
			cancel(shutdown)
		case <-ctx.Done():
		}
	}()
	err := consumer.Consume(ctx, func(context.Context, messages.Message) error {
		handled++
		return nil
	})

	// Assert
	if !errors.Is(err, shutdown) {
		t.Fatalf("lost cancellation cause while receiving: %v", err)
	}
	if handled != 0 || errorCallbacks != 0 || acknowledged != 0 {
		t.Fatalf("receive cancellation must bypass processing: handled=%d error=%d acknowledged=%d", handled, errorCallbacks, acknowledged)
	}
}

func quietConsumer(sub comms.Subscription) comms.Consumer {
	return comms.Consumer{Subscription: sub, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func publishForConsumer(t *testing.T, bus comms.Bus, items ...messages.Message) {
	t.Helper()
	for _, m := range items {
		if err := bus.Publish(t.Context(), "work", m); err != nil {
			t.Fatal(err)
		}
	}
}

func consumerContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// Inspect persisted deliveries rather than relying on provider cursor state:
// the FS provider retains unacknowledged messages without redelivering them.
func retainedDeliveries(t *testing.T, root, messageID string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var records []struct {
			Message messages.Message `json:"message"`
		}
		if err := json.Unmarshal(data, &records); err != nil {
			return err
		}
		for _, record := range records {
			if record.Message.ID == messageID {
				count++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

type failFirstAck struct {
	comms.Subscription
	failure  error
	attempts int
}

type receiveOverride struct {
	comms.Subscription
	next func(context.Context) (messages.Message, error)
}

func (s *receiveOverride) Next(ctx context.Context) (messages.Message, error) {
	return s.next(ctx)
}

func (s *failFirstAck) Ack(ctx context.Context, messageID string) error {
	s.attempts++
	if s.attempts == 1 {
		return fmt.Errorf("persist acknowledgement: %w", s.failure)
	}
	return s.Subscription.Ack(ctx, messageID)
}
