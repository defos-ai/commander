package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
)

func (m *Manager) waitReady(ctx context.Context, saved record, uid string, current *observation) (runtimes.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, m.config.RegistrationTimeout)
	defer cancel()
	ticker := time.NewTicker(m.config.PollInterval)
	defer ticker.Stop()
	observed := current.done
	registered := false
	var instance PodState
	for {
		pod, state, err := m.inspect(ctx, saved, uid)
		if err != nil {
			return runtimes.Result{}, err
		}
		if state.Gated {
			return runtimes.Result{}, errors.New("Kubernetes Pod is still gated after activation")
		}
		if state.Phase == "Running" {
			if instance.ContainerID == "" {
				if err := m.confirmInstance(ctx, saved, pod, state); err != nil {
					return runtimes.Result{}, err
				}
				instance = state
			} else if !sameInstance(instance, state) {
				return runtimes.Result{}, errors.New("Kubernetes container changed during registration; a replacement requires a new task")
			}
		}
		if !registered {
			select {
			case <-observed:
				if current.err != nil {
					return runtimes.Result{}, current.err
				}
				registered, observed = true, nil
			default:
			}
		}
		if registered && state.Phase == "Running" {
			// Persistence and registration may take time; verify the same process
			// once more before returning an observed readiness snapshot.
			_, confirmed, err := m.inspect(ctx, saved, uid)
			if err != nil {
				return runtimes.Result{}, err
			}
			if confirmed.Gated || confirmed.Phase != "Running" || !sameInstance(instance, confirmed) {
				return runtimes.Result{}, errors.New("Kubernetes Pod changed or stopped during registration")
			}
			result := cloneRegistrationResult(current.result)
			result.RuntimeDetails = runtimes.KubernetesRuntimeDetails{Namespace: pod.Metadata.Namespace, PodName: pod.Metadata.Name, PodUID: uid}
			return result, nil
		}
		select {
		case <-ctx.Done():
			return runtimes.Result{}, fmt.Errorf("waiting for agent %s registration and running Pod (retry observes this instance): %w", saved.AgentID, ctx.Err())
		case <-observed:
			if current.err != nil {
				return runtimes.Result{}, current.err
			}
			registered, observed = true, nil
		case <-ticker.C:
		}
	}
}

func sameInstance(a, b PodState) bool {
	return a.ContainerID == b.ContainerID && a.StartedAt == b.StartedAt
}
