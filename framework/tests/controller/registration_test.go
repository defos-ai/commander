package controller_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/domain/controllers"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestRegistrationAdvertisesUntilHostShutdownFinishes(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	hostCtx, stopHost := context.WithCancel(ctx)
	defer stopHost()
	store := fakes.NewRegistrationStore()
	registration, err := controller.Register(hostCtx, store, controller.RegistrationConfig{
		ID: "default-controller-12345678", Type: "default", AgentSelector: "round-robin", RuleSelector: "custom-rules",
		HeartbeatInterval: 10 * time.Millisecond, LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registration.Close(context.Background()) })
	previous := fixtures.Receive(t, ctx, store.Observations())
	if previous.Status != controllers.Starting || previous.Version == "" || previous.Server.OS == "" || previous.Server.Arch == "" || previous.RuleSelector != "custom-rules" {
		t.Fatalf("incomplete starting registration: %+v", previous)
	}
	if err := registration.SetStatus(ctx, controllers.Degraded, "invalid\nreason"); err == nil {
		t.Fatal("invalid status reason was accepted")
	}
	if heartbeat := fixtures.Receive(t, ctx, store.Observations()); heartbeat.Status != controllers.Starting || heartbeat.StatusReason != "" {
		t.Fatalf("rejected status update changed subsequent heartbeats: %+v", heartbeat)
	}
	for _, status := range []controllers.Status{controllers.Ready, controllers.Degraded, controllers.Draining} {
		if status == controllers.Draining {
			stopHost()
		}
		if err := registration.SetStatus(ctx, status, ""); err != nil {
			t.Fatal(err)
		}
		for {
			current := fixtures.Receive(t, ctx, store.Observations())
			if !current.LastHeartbeat.After(previous.LastHeartbeat) || !current.StartedAt.Equal(previous.StartedAt) || current.RuleSelector != previous.RuleSelector {
				t.Fatalf("registration lost observation ordering: previous=%+v current=%+v", previous, current)
			}
			previous = current
			if current.Status == status {
				break
			}
		}
	}
	renewal := fixtures.Receive(t, ctx, store.Observations())
	if renewal.Status != controllers.Draining || !renewal.LeaseExpiresAt.After(previous.LeaseExpiresAt) {
		t.Fatalf("host cancellation stopped lease renewal before shutdown: %+v", renewal)
	}
	if err := registration.Close(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		last := fixtures.Receive(t, ctx, store.Observations())
		if last.Status == controllers.Stopped {
			if !last.LeaseExpiresAt.Equal(last.LastHeartbeat) {
				t.Fatalf("stopped registration retained a live lease: %+v", last)
			}
			break
		}
	}
	if err := registration.SetStatus(ctx, controllers.Ready, ""); !errors.Is(err, controller.ErrControllerClosed) {
		t.Fatalf("closed registration accepted status: %v", err)
	}
	if err := registration.Close(ctx); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
}

func TestRegistrationCloseCancelsBlockedHeartbeat(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	store := fakes.NewRegistrationStore()
	registration, err := controller.Register(ctx, store, controller.RegistrationConfig{
		ID: "default-controller-12345678", Type: "default", AgentSelector: "round-robin",
		HeartbeatInterval: 10 * time.Millisecond, LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registration.Close(context.Background()) })
	store.BlockHeartbeat()
	fixtures.Receive(t, ctx, store.Blocked())
	shutdown, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if err := registration.Close(shutdown); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown did not respect deadline: %v", err)
	}
	if err := registration.Close(ctx); err != nil {
		t.Fatalf("blocked heartbeat was not cancelled: %v", err)
	}
}
