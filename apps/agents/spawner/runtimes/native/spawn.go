package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	downloads "github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	actions "github.com/defos-ai/commander/framework/domain/actions"
	agents "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/id"
)

// Spawn uses the controller execution command identity,
// never an identifier supplied by a model in action input. Each new request
// gets its own agent ID; a retry can only observe that same child.
func (m *Manager) Spawn(ctx context.Context, requestID, reference string) (runtimes.Result, error) {
	if strings.TrimSpace(requestID) == "" || len(requestID) > 1024 {
		return runtimes.Result{}, errors.New("launch request identity is required and must be at most 1024 bytes")
	}
	if _, err := packages.ParseSelector(reference); err != nil {
		return runtimes.Result{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	stopForwarding := context.AfterFunc(m.ctx, cancel)
	defer func() { stopForwarding(); cancel() }()
	// Serialize requests so ownership and Close are independent of scheduling.
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ctx.Err(); err != nil {
		return runtimes.Result{}, err
	}
	if m.closed {
		return runtimes.Result{}, errors.New("native spawner is closed")
	}
	if err := ctx.Err(); err != nil {
		return runtimes.Result{}, err
	}
	reference, err := runtimes.PinPackage(ctx, requestID, reference, m.config.Resolver)
	if err != nil {
		return runtimes.Result{}, err
	}
	if current := m.children[requestID]; current != nil {
		if current.result.Package != reference {
			return runtimes.Result{}, errors.New("launch request already belongs to a different package")
		}
		if current.exited() {
			return runtimes.Result{}, fmt.Errorf("agent %s has exited; a replacement requires a new task", current.result.AgentID)
		}
		return m.awaitRegistration(ctx, current)
	}
	if err := m.existingRecord(ctx, requestID, reference); err != nil {
		return runtimes.Result{}, err
	}
	return m.launch(ctx, requestID, reference)
}

func (m *Manager) launch(ctx context.Context, requestID, reference string) (runtimes.Result, error) {
	var pkg downloads.Package
	var path string
	err := action.RunWithProgress(ctx, "Native package preparation", func() error {
		var preparationErr error
		pkg, preparationErr = m.config.Resolver.Resolve(ctx, reference)
		if preparationErr != nil {
			return preparationErr
		}
		path, preparationErr = m.config.Resolver.DownloadNative(ctx, pkg, runtime.GOOS, runtime.GOARCH, filepath.Join(m.config.Directory, "downloads"))
		return preparationErr
	})
	if err != nil {
		return runtimes.Result{}, err
	}
	var current *child
	err = action.RunWithProgress(ctx, "Native launch reconciliation", func() error {
		var launchErr error
		current, launchErr = m.start(ctx, requestID, reference, pkg.Manifest.Type, path)
		return launchErr
	})
	if err != nil {
		return runtimes.Result{}, err
	}
	return m.awaitRegistration(ctx, current)
}

func (m *Manager) start(ctx context.Context, requestID, reference, agentType, path string) (*child, error) {
	started := false
	defer func() {
		if !started {
			_ = os.RemoveAll(filepath.Dir(path))
		}
	}()
	agentID, err := id.AgentID(agentType)
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(m.config.Directory, "agents", agentID)
	if err := os.Mkdir(directory, 0700); err != nil {
		return nil, fmt.Errorf("create child working directory: %w", err)
	}
	subscription, err := m.subscribe(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !started {
			_ = subscription.Close()
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	saved := record{Runtime: "native", RequestID: requestID, Package: reference, AgentID: agentID, AgentType: agentType}
	if err := m.reserve(ctx, saved); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command := exec.Command(path)
	command.Dir = directory
	command.Env = m.environment(saved, directory)
	// Child output can contain secrets. Do not put it in action errors or logs.
	configureCommand(command)
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start agent %s: %w", agentID, err)
	}
	started = true
	current := &child{
		command: command, done: make(chan struct{}), download: filepath.Dir(path), subscription: subscription,
		registrationDone: make(chan struct{}),
		result: runtimes.Result{AgentID: agentID, AgentType: agentType, Package: reference,
			Runtime: runtimes.RuntimeNative, RuntimeDetails: runtimes.NativeRuntimeDetails{PID: command.Process.Pid}},
	}
	m.children[requestID] = current
	go current.wait()
	go func() {
		current.registrationErr = m.observeRegistration(subscription, current)
		close(current.registrationDone)
	}()
	return current, nil
}

func (m *Manager) subscribe(ctx context.Context) (comms.Subscription, error) {
	return m.config.Bus.Subscribe(ctx, comms.Inbox{
		Topic: messages.ControllerEvents(messages.DefaultControllerType),
		Name:  "spawn-" + id.MessageID(), Mode: comms.TransientExclusive,
	})
}

func (m *Manager) awaitRegistration(ctx context.Context, current *child) (runtimes.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, m.config.RegistrationTimeout)
	defer cancel()
	var result runtimes.Result
	err := action.RunWithProgress(ctx, "Native registration observation", func() error {
		var registrationErr error
		result, registrationErr = m.waitRegistration(ctx, current)
		return registrationErr
	})
	return result, err
}

func (m *Manager) waitRegistration(ctx context.Context, current *child) (runtimes.Result, error) {
	select {
	case <-ctx.Done():
		return runtimes.Result{}, fmt.Errorf("waiting for agent %s registration (retry observes this instance): %w", current.result.AgentID, ctx.Err())
	case <-current.registrationDone:
		if current.registrationErr != nil {
			return runtimes.Result{}, current.registrationErr
		}
		if current.exited() {
			return runtimes.Result{}, fmt.Errorf("agent %s exited during registration", current.result.AgentID)
		}
		return cloneResult(current.result), nil
	}
}

// Observation belongs to the host, not an action's wait. Continue draining the
// inbox after a canceled wait so late registration is retained without queuing
// unrelated controller events indefinitely.
func (m *Manager) observeRegistration(subscription comms.Subscription, current *child) (result error) {
	ctx, cancel := context.WithCancel(m.ctx)
	defer cancel()
	defer func() {
		if err := subscription.Close(); err != nil && !errors.Is(err, comms.ErrClosed) {
			result = errors.Join(result, err)
		}
	}()
	// A process exiting must interrupt Next even when it never announced itself.
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-current.done:
			cancel()
		case <-watchDone:
		}
	}()
	for {
		delivery, err := subscription.Next(ctx)
		if err != nil {
			if current.exited() {
				return fmt.Errorf("agent %s exited before registration", current.result.AgentID)
			}
			return fmt.Errorf("observe agent %s registration: %w", current.result.AgentID, err)
		}
		message := delivery.Message()
		if err := delivery.Ack(ctx); err != nil {
			return fmt.Errorf("acknowledge registration observation: %w", err)
		}
		if message.AgentID != current.result.AgentID || (message.Type != messages.AgentStarted && message.Type != messages.AgentHeartbeat) {
			continue
		}
		if message.Validate() != nil || time.Since(message.CreatedAt) > agents.DefaultHeartbeatTimeout {
			continue
		}
		var snapshot agents.AgentSnapshot
		if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
			return errors.New("spawned agent published invalid registration")
		}
		if snapshot.ID != current.result.AgentID || string(snapshot.Type) != current.result.AgentType {
			return fmt.Errorf("registration does not match assigned agent %s and package type", current.result.AgentID)
		}
		if snapshot.Status != agents.AgentReady && snapshot.Status != agents.AgentWorking {
			continue
		}
		if snapshot.Server.Validate() != nil {
			return errors.New("spawned agent published invalid server metadata")
		}
		seen := make(map[string]bool, len(snapshot.Actions))
		for _, definition := range snapshot.Actions {
			if definition.Validate() != nil || seen[definition.Name] || (definition.Status != actions.Ready && definition.Status != actions.Unavailable && definition.Status != actions.Unknown) {
				return errors.New("spawned agent published invalid action metadata")
			}
			seen[definition.Name] = true
		}
		if current.exited() {
			return fmt.Errorf("agent %s exited during registration", current.result.AgentID)
		}
		current.result.Actions = snapshot.Actions
		return nil
	}
}
