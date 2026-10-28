package rabbitmq_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
)

func closeSubscription(t *testing.T, sub comms.Subscription) {
	t.Helper()
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
}

func ack(t *testing.T, ctx context.Context, delivery comms.Delivery) {
	t.Helper()
	if err := delivery.Ack(ctx); err != nil {
		t.Fatal(err)
	}
}

func nextDelivery(t *testing.T, ctx context.Context, sub comms.Subscription, want messages.Message) comms.Delivery {
	t.Helper()
	delivery, err := sub.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if delivery == nil || !reflect.DeepEqual(delivery.Message(), want) {
		t.Fatalf("received message differs from publication: got %v, want %+v", delivery, want)
	}
	return delivery
}

func assertNoDelivery(t *testing.T, ctx context.Context, sub comms.Subscription) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if delivery, err := sub.Next(ctx); !errors.Is(err, context.DeadlineExceeded) || delivery != nil {
		t.Fatalf("expected no delivery: got %v, %v", delivery, err)
	}
}
