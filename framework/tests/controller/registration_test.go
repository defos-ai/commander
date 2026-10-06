package controller_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"commander/framework/controller"
	"commander/framework/domain/controllers"
)

func TestRegistrationAdvertisesUntilHostShutdownFinishes(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	hostCtx, stopHost := context.WithCancel(ctx)
	defer stopHost()
	store := &registrationStore{observations: make(chan controllers.Controller, 100)}
	registration, err := controller.Register(hostCtx, store, controller.RegistrationConfig{
		ID: "default-controller-12345678", Type: "default", AgentSelector: "round-robin",
		HeartbeatInterval: 10 * time.Millisecond, LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registration.Close(context.Background()) })
	previous := receive(t, ctx, store.observations)
	if previous.Status != controllers.Starting || previous.Version == "" || previous.Server.OS == "" || previous.Server.Arch == "" {
		t.Fatalf("incomplete starting registration: %+v", previous)
	}
	if err := registration.SetStatus(ctx, controllers.Degraded, "invalid\nreason"); err == nil {
		t.Fatal("invalid status reason was accepted")
	}
	if heartbeat := receive(t, ctx, store.observations); heartbeat.Status != controllers.Starting || heartbeat.StatusReason != "" {
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
			current := receive(t, ctx, store.observations)
			if !current.LastHeartbeat.After(previous.LastHeartbeat) || !current.StartedAt.Equal(previous.StartedAt) {
				t.Fatalf("registration lost observation ordering: previous=%+v current=%+v", previous, current)
			}
			previous = current
			if current.Status == status {
				break
			}
		}
	}
	renewal := receive(t, ctx, store.observations)
	if renewal.Status != controllers.Draining || !renewal.LeaseExpiresAt.After(previous.LeaseExpiresAt) {
		t.Fatalf("host cancellation stopped lease renewal before shutdown: %+v", renewal)
	}
	if err := registration.Close(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		last := receive(t, ctx, store.observations)
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
	store := &registrationStore{observations: make(chan controllers.Controller, 10), blocked: make(chan struct{}, 1)}
	registration, err := controller.Register(ctx, store, controller.RegistrationConfig{
		ID: "default-controller-12345678", Type: "default", AgentSelector: "round-robin",
		HeartbeatInterval: 10 * time.Millisecond, LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registration.Close(context.Background()) })
	store.block.Store(true)
	receive(t, ctx, store.blocked)
	shutdown, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if err := registration.Close(shutdown); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown did not respect deadline: %v", err)
	}
	if err := registration.Close(ctx); err != nil {
		t.Fatalf("blocked heartbeat was not cancelled: %v", err)
	}
}

type registrationStore struct {
	controllers.ControllerStore
	observations chan controllers.Controller
	blocked      chan struct{}
	block        atomic.Bool
}

func (s *registrationStore) Upsert(ctx context.Context, record controllers.Controller) error {
	if s.block.Load() {
		select {
		case s.blocked <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	select {
	case s.observations <- record:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
