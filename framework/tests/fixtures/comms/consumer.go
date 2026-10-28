package fixtures

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/comms"
)

func Consumer(concurrency int, deliveries ...*fakes.Delivery) comms.Consumer {
	s := fakes.NewSubscription(deliveries...)
	return comms.Consumer{Subscription: s, Concurrency: concurrency, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func ConsumerContext(t *testing.T) (context.Context, context.CancelCauseFunc) {
	t.Helper()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(stop)
	return context.WithCancelCause(ctx)
}
