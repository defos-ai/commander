package controller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"commander/framework/domain/controllers"
)

type RegistrationConfig struct {
	ID, Type, Version, AgentSelector string
	HeartbeatInterval, LeaseDuration time.Duration
	Logger                           *slog.Logger
}

type registrationUpdate struct {
	ctx    context.Context
	status controllers.Status
	reason string
	done   chan error
}

// Registration serializes this process's status and heartbeat writes. The host
// marks it ready after recovery and consumers are running, then closes it last.
type Registration struct {
	updates chan registrationUpdate
	done    chan struct{}
	cancel  context.CancelFunc
}

func Register(ctx context.Context, store controllers.ControllerStore, config RegistrationConfig) (*Registration, error) {
	if ctx == nil || store == nil {
		return nil, errors.New("controller registration requires a context and store")
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = 10 * time.Second
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = 45 * time.Second
	}
	if config.HeartbeatInterval < 0 || config.LeaseDuration <= config.HeartbeatInterval {
		return nil, errors.New("controller lease must exceed its positive heartbeat interval")
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if config.Version == "" {
		config.Version = controllerBuildVersion()
	}
	hostname, _ := os.Hostname()
	now := time.Now().UTC()
	record := controllers.Controller{
		ID: config.ID, Type: config.Type, Status: controllers.Starting,
		Server:  controllers.ServerInfo{Hostname: hostname, OS: runtime.GOOS, Arch: runtime.GOARCH},
		Version: config.Version, AgentSelector: config.AgentSelector,
		StartedAt: now, LastHeartbeat: now, LeaseExpiresAt: now.Add(config.LeaseDuration),
	}
	if err := record.Validate(); err != nil {
		return nil, err
	}
	initial, cancelInitial := context.WithTimeout(ctx, 5*time.Second)
	defer cancelInitial()
	if err := store.Upsert(initial, record); err != nil {
		return nil, err
	}
	// Keep advertising during host shutdown until its consumers and runner stop.
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r := &Registration{updates: make(chan registrationUpdate), done: make(chan struct{}), cancel: cancel}
	go r.run(lifetime, store, config, record)
	return r, nil
}

func (r *Registration) SetStatus(ctx context.Context, status controllers.Status, reason string) error {
	if status != controllers.Ready && status != controllers.Degraded && status != controllers.Draining {
		return errors.New("controller registration status must be ready, degraded or draining")
	}
	return r.update(ctx, status, reason)
}

func (r *Registration) update(ctx context.Context, status controllers.Status, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	request := registrationUpdate{ctx: ctx, status: status, reason: reason, done: make(chan error, 1)}
	select {
	case r.updates <- request:
	case <-r.done:
		return ErrControllerClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Registration) Close(ctx context.Context) error {
	err := r.update(ctx, controllers.Stopped, "")
	r.cancel()
	select {
	case <-r.done:
		if errors.Is(err, ErrControllerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
	}
}

func (r *Registration) run(ctx context.Context, store controllers.ControllerStore, config RegistrationConfig, record controllers.Controller) {
	defer close(r.done)
	ticker := time.NewTicker(config.HeartbeatInterval)
	defer ticker.Stop()
	persist := func(parent context.Context) error {
		now := time.Now().UTC()
		if !now.After(record.LastHeartbeat) {
			now = record.LastHeartbeat.Add(time.Nanosecond)
		}
		record.LastHeartbeat, record.LeaseExpiresAt = now, now.Add(config.LeaseDuration)
		if record.Status == controllers.Stopped {
			record.LeaseExpiresAt = now
		}
		write, cancel := context.WithTimeout(parent, 5*time.Second)
		defer cancel()
		stop := context.AfterFunc(ctx, cancel)
		defer stop()
		return store.Upsert(write, record)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-r.updates:
			if ctx.Err() != nil {
				request.done <- ErrControllerClosed
				return
			}
			if err := request.ctx.Err(); err != nil {
				request.done <- err
				continue
			}
			candidate := record
			candidate.Status, candidate.StatusReason = request.status, request.reason
			if err := candidate.Validate(); err != nil {
				request.done <- err
				continue
			}
			record = candidate
			request.done <- persist(request.ctx)
			if request.status == controllers.Stopped {
				return
			}
		case <-ticker.C:
			if err := persist(ctx); err != nil && ctx.Err() == nil {
				config.Logger.Error("Controller heartbeat failed", "error", err)
			}
		}
	}
}

func controllerBuildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}
	return "development"
}
