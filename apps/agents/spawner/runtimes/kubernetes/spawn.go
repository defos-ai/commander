package kubernetes

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

// Spawn fences creation with an immutable controller checkpoint. Retrying a
// recorded launch only observes that Pod, even when the first response was lost.
func (m *Manager) Spawn(ctx context.Context, requestID, reference string) (runtimes.Result, error) {
	if strings.TrimSpace(requestID) == "" || len(requestID) > 1024 {
		return runtimes.Result{}, errors.New("launch request identity is required and must be at most 1024 bytes")
	}
	if _, err := packages.ParseSelector(reference); err != nil {
		return runtimes.Result{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	defer func() { stop(); cancel() }()
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
	saved, createdUID, err := m.restoreOrCreatePod(ctx, requestID, reference)
	if err != nil {
		return runtimes.Result{}, err
	}
	pod, state, err := m.inspect(ctx, saved, createdUID)
	if err != nil {
		return runtimes.Result{}, err
	}
	if err := m.confirmLaunch(ctx, saved, pod, state); err != nil {
		return runtimes.Result{}, err
	}
	current, err := m.observe(saved)
	if err != nil {
		return runtimes.Result{}, err
	}
	if state.Gated {
		if err := m.activate(ctx, saved, pod); err != nil {
			return runtimes.Result{}, err
		}
	}
	return m.waitReady(ctx, saved, pod.Metadata.UID, current)
}

func (m *Manager) restoreOrCreatePod(ctx context.Context, requestID, reference string) (record, string, error) {
	saved, exists, err := m.readRecord(ctx, requestID, reference)
	if err != nil || exists {
		return saved, "", err
	}
	if err := requireNoReceipts(ctx); err != nil {
		return record{}, "", err
	}
	saved, err = m.prepare(ctx, requestID, reference)
	if err != nil {
		return record{}, "", err
	}
	created, err := m.config.Client.Create(ctx, saved.Pod)
	if err != nil {
		return record{}, "", fmt.Errorf("Pod creation for %s is uncertain; retry inspects this request: %w", saved.AgentID, err)
	}
	if err := m.rememberUID(saved, created); err != nil {
		return record{}, "", err
	}
	state, err := InspectPod(saved.Pod, created)
	if err != nil {
		return record{}, "", err
	}
	if !state.Gated {
		return record{}, "", errors.New("created Kubernetes Pod lost its launch gate before activation")
	}
	if err := m.confirmReceipt(ctx, saved, created); err != nil {
		return record{}, "", err
	}
	return saved, created.Metadata.UID, nil
}

func (m *Manager) confirmLaunch(ctx context.Context, saved record, pod Pod, state PodState) error {
	receipt, found, err := m.readReceipt(ctx, saved)
	if err != nil {
		return err
	}
	if found && receipt.PodUID != pod.Metadata.UID {
		return errors.New("recorded Kubernetes Pod was replaced; refusing another agent instance")
	}
	if !found && !state.Gated {
		return errors.New("activated Kubernetes Pod has no durable UID receipt; launch outcome is uncertain")
	}
	if state.Gated {
		if m.ownedIntents[saved.RequestID] != saved.Owner {
			return errors.New("recorded Kubernetes Pod is gated; launch ownership is uncertain, refusing activation")
		}
		_, ran, err := action.LoadCheckpoint(ctx, "launch-instance")
		if err != nil {
			return err
		}
		if ran {
			return errors.New("gated Kubernetes Pod has a recorded running instance; refusing activation")
		}
	}
	if err := m.confirmReceipt(ctx, saved, pod); err != nil {
		return err
	}
	if state.Phase == "Running" {
		return m.confirmInstance(ctx, saved, pod, state)
	}
	return nil
}

func (m *Manager) activate(ctx context.Context, saved record, pod Pod) error {
	activated, err := m.config.Client.ReleaseGate(ctx, pod)
	if err != nil {
		return fmt.Errorf("Pod activation for %s is uncertain; retry inspects this instance: %w", saved.AgentID, err)
	}
	state, err := InspectPod(saved.Pod, activated)
	if err != nil {
		return err
	}
	if activated.Metadata.UID != pod.Metadata.UID || state.Gated {
		return errors.New("Kubernetes Pod identity or launch gate changed during activation")
	}
	if state.Phase == "Running" {
		return m.confirmInstance(ctx, saved, activated, state)
	}
	return nil
}

func requireNoReceipts(ctx context.Context) error {
	for _, name := range []string{"launch-receipt", "launch-instance"} {
		_, found, err := action.LoadCheckpoint(ctx, name)
		if err != nil {
			return err
		}
		if found {
			return errors.New("Kubernetes launch intent is missing but a receipt exists; refusing another Pod")
		}
	}
	return nil
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
	agentID, err := id.AgentID(pkg.Manifest.Type)
	if err != nil {
		return record{}, err
	}
	saved := record{Runtime: runtimes.RuntimeKubernetes, RequestID: requestID, Package: reference, AgentID: agentID, AgentType: pkg.Manifest.Type, Image: image, Owner: id.New()}
	saved.Pod, err = BuildPod(m.config.Pod, saved.spec())
	if err != nil {
		return record{}, err
	}
	if err := m.reserve(ctx, saved); err != nil {
		return record{}, err
	}
	return saved, nil
}

func (m *Manager) inspect(ctx context.Context, saved record, uid string) (Pod, PodState, error) {
	pod, err := m.config.Client.Get(ctx, saved.Pod.Metadata.Name)
	if errors.Is(err, ErrNotFound) {
		return Pod{}, PodState{}, errors.New("recorded Kubernetes Pod is missing; launch outcome is uncertain, refusing another Pod")
	}
	if err != nil {
		return Pod{}, PodState{}, err
	}
	if uid != "" && pod.Metadata.UID != uid {
		return Pod{}, PodState{}, errors.New("recorded Kubernetes Pod was replaced; refusing another agent instance")
	}
	if err := m.rememberUID(saved, pod); err != nil {
		return Pod{}, PodState{}, err
	}
	state, err := InspectPod(saved.Pod, pod)
	if err == nil && state.Phase == "Running" {
		_, err = m.rememberInstance(saved, pod, state)
	}
	return pod, state, err
}

// Keep positive identity observations even when persisting their receipt fails.
// A later call on this host must not learn a replacement as the original Pod.
func (m *Manager) rememberUID(saved record, pod Pod) error {
	if pod.Metadata.Name != saved.Pod.Metadata.Name || pod.Metadata.Namespace != saved.Pod.Metadata.Namespace || !opaqueVersion(pod.Metadata.UID) {
		return errors.New("Kubernetes Pod identity does not match the launch intent")
	}
	known := m.observedUIDs[saved.RequestID]
	if known != "" && known != pod.Metadata.UID {
		return errors.New("observed Kubernetes Pod was replaced; refusing another agent instance")
	}
	m.observedUIDs[saved.RequestID] = pod.Metadata.UID
	return nil
}
