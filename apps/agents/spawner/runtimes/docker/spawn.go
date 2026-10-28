package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/id"
)

// Spawn uses the controller execution command identity.
// An uncertain Docker response never authorizes a second Create for that request.
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ctx.Err(); err != nil {
		return runtimes.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return runtimes.Result{}, err
	}
	reference, err := runtimes.PinPackage(ctx, requestID, reference, m.config.Resolver)
	if err != nil {
		return runtimes.Result{}, err
	}
	saved, exists, err := m.readRecord(ctx, requestID, reference)
	if err != nil {
		return runtimes.Result{}, err
	}
	if !exists {
		if err := action.RunWithProgress(ctx, "Docker preparation", func() error {
			var preparationErr error
			saved, preparationErr = m.prepare(ctx, requestID, reference)
			return preparationErr
		}); err != nil {
			return runtimes.Result{}, err
		}
	}
	var container Container
	var current *observation
	if err := action.RunWithProgress(ctx, "Docker launch reconciliation", func() error {
		var launchErr error
		container, current, launchErr = m.launch(ctx, saved, exists)
		return launchErr
	}); err != nil {
		return runtimes.Result{}, err
	}
	var result runtimes.Result
	err = action.RunWithProgress(ctx, "Docker registration observation", func() error {
		var registrationErr error
		result, registrationErr = m.registered(ctx, saved, container, current)
		return registrationErr
	})
	return result, err
}

func (m *Manager) launch(ctx context.Context, saved record, exists bool) (Container, *observation, error) {
	if !exists {
		if err := m.reserve(ctx, saved); err != nil {
			return Container{}, nil, err
		}
		created, err := m.config.Engine.Create(ctx, Spec{Name: saved.name(), Image: saved.Image, Network: saved.Network, Environment: m.environment(saved), Labels: saved.labels()})
		if err != nil {
			return Container{}, nil, fmt.Errorf("container creation for %s is uncertain; retry inspects this request: %w", saved.AgentID, err)
		}
		if err := m.verify(saved, created); err != nil {
			return Container{}, nil, err
		}
	}
	container, err := m.inspect(ctx, saved)
	if err != nil {
		return Container{}, nil, err
	}
	if container.Status != "created" && container.Status != "running" {
		return Container{}, nil, fmt.Errorf("agent %s container is %s; a replacement requires a new task", saved.AgentID, container.Status)
	}
	if container.Status == "created" && m.ownedIntents[saved.RequestID] != saved.Owner {
		// Checkpoints identify the intent, not an exclusive runtime owner. A
		// restored manager cannot safely compete to Start this container.
		return Container{}, nil, fmt.Errorf("recorded container for %s has not started; launch ownership is uncertain, refusing to start it", saved.AgentID)
	}
	current, err := m.observe(saved)
	if err != nil {
		return Container{}, nil, err
	}
	if container.Status == "created" {
		// Only a never-started container may start. An exited instance must not
		// reuse its identity, even if the executable/image is unchanged.
		if err := m.config.Engine.Start(ctx, container.ID); err != nil {
			return Container{}, nil, fmt.Errorf("container start for %s is uncertain; retry inspects this instance: %w", saved.AgentID, err)
		}
		container, err = m.inspect(ctx, saved)
		if err != nil {
			return Container{}, nil, err
		}
	}
	if container.Status != "running" {
		return Container{}, nil, fmt.Errorf("agent %s container is %s; a replacement requires a new task", saved.AgentID, container.Status)
	}
	return container, current, nil
}

func (m *Manager) registered(ctx context.Context, saved record, container Container, current *observation) (runtimes.Result, error) {
	result, err := m.awaitRegistration(ctx, current)
	if err != nil {
		return runtimes.Result{}, err
	}
	confirmed, err := m.inspect(ctx, saved)
	if err != nil {
		return runtimes.Result{}, err
	}
	if confirmed.ID != container.ID || confirmed.Status != "running" {
		return runtimes.Result{}, errors.New("Docker container changed or stopped during registration")
	}
	result.RuntimeDetails = runtimes.DockerRuntimeDetails{ContainerID: container.ID}
	return result, nil
}

func (m *Manager) prepare(ctx context.Context, requestID, reference string) (record, error) {
	pkg, err := m.config.Resolver.Resolve(ctx, reference)
	if err != nil {
		return record{}, err
	}
	image, err := pkg.Manifest.DockerImage()
	if err != nil {
		return record{}, err
	}
	if err := m.config.Engine.Pull(ctx, image); err != nil {
		return record{}, err
	}
	agentID, err := id.AgentID(pkg.Manifest.Type)
	if err != nil {
		return record{}, err
	}
	saved := record{Runtime: "docker", RequestID: requestID, Package: reference, AgentID: agentID, AgentType: pkg.Manifest.Type, Image: image, Network: m.config.Network, Owner: id.New()}
	if err := ctx.Err(); err != nil {
		return record{}, err
	}
	return saved, nil
}

func (m *Manager) inspect(ctx context.Context, saved record) (Container, error) {
	container, err := m.config.Engine.Inspect(ctx, saved.name())
	if errors.Is(err, ErrNotFound) {
		return Container{}, fmt.Errorf("recorded container for %s is missing; launch outcome is uncertain, refusing another container", saved.AgentID)
	}
	if err != nil {
		return Container{}, err
	}
	if err := m.verify(saved, container); err != nil {
		return Container{}, err
	}
	return container, nil
}

func (m *Manager) verify(saved record, container Container) error {
	if container.ID == "" || container.Name != saved.name() || container.Image != saved.Image || !container.Safe || len(container.Networks) != 1 || container.Networks[0] != saved.Network {
		return errors.New("Docker container does not match the recorded identity, image, network or launch restrictions")
	}
	for key, value := range saved.labels() {
		if container.Labels[key] != value {
			return errors.New("Docker container ownership does not match the launch record")
		}
	}
	if _, exists := container.Environment["SECRETS_ENCRYPTION_KEY"]; exists {
		return errors.New("Docker container contains an unsupported secrets encryption key")
	}
	for key, value := range m.environment(saved) {
		if actual, ok := container.Environment[key]; !ok || actual != value {
			return errors.New("Docker container bootstrap settings differ from spawner configuration")
		}
	}
	return nil
}
