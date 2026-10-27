package kubernetes

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/id"
)

type observation struct {
	agentID string
	done    chan struct{}
	result  runtimes.Result
	err     error
}

// The caller holds Manager.mu and subscribes before releasing the scheduling
// gate. Observation survives an action timeout but ends with the Manager host.
func (m *Manager) observe(saved record) (*observation, error) {
	if current := m.observations[saved.RequestID]; current != nil {
		if current.agentID != saved.AgentID {
			return nil, errors.New("Kubernetes registration observation belongs to another agent")
		}
		return current, nil
	}
	subscription, err := m.config.Bus.Subscribe(m.ctx, comms.Inbox{
		Topic: messages.ControllerEvents(messages.DefaultControllerType),
		Name:  "spawn-" + id.MessageID(), Mode: comms.TransientExclusive,
	})
	if err != nil {
		return nil, err
	}
	current := &observation{agentID: saved.AgentID, done: make(chan struct{})}
	m.observations[saved.RequestID] = current
	go func() {
		current.result, current.err = m.readRegistration(subscription, saved)
		if err := subscription.Close(); err != nil && !errors.Is(err, comms.ErrClosed) {
			current.err = errors.Join(current.err, err)
		}
		close(current.done)
	}()
	return current, nil
}

func (m *Manager) readRegistration(subscription comms.Subscription, saved record) (runtimes.Result, error) {
	for {
		delivery, err := subscription.Next(m.ctx)
		if err != nil {
			return runtimes.Result{}, fmt.Errorf("observe agent %s registration: %w", saved.AgentID, err)
		}
		message := delivery.Message()
		if err := delivery.Ack(m.ctx); err != nil {
			return runtimes.Result{}, fmt.Errorf("acknowledge registration observation: %w", err)
		}
		if message.AgentID != saved.AgentID || (message.Type != messages.AgentStarted && message.Type != messages.AgentHeartbeat) {
			continue
		}
		now := time.Now()
		if message.Validate() != nil || !message.CreatedAt.After(now.Add(-agents.DefaultHeartbeatTimeout)) ||
			message.CreatedAt.After(now.Add(5*time.Second)) {
			continue
		}
		var snapshot agents.AgentSnapshot
		if json.Unmarshal(message.Payload, &snapshot) != nil {
			return runtimes.Result{}, errors.New("spawned agent published invalid registration")
		}
		if snapshot.ID != saved.AgentID || string(snapshot.Type) != saved.AgentType {
			return runtimes.Result{}, errors.New("registration does not match assigned agent ID and package type")
		}
		if snapshot.Status != agents.AgentReady && snapshot.Status != agents.AgentWorking {
			continue
		}
		if snapshot.Server.Validate() != nil {
			return runtimes.Result{}, errors.New("spawned agent published invalid server metadata")
		}
		seen := make(map[string]bool, len(snapshot.Actions))
		for _, definition := range snapshot.Actions {
			if definition.Validate() != nil || seen[definition.Name] ||
				(definition.Status != actions.Ready && definition.Status != actions.Unavailable && definition.Status != actions.Unknown) {
				return runtimes.Result{}, errors.New("spawned agent published invalid action metadata")
			}
			seen[definition.Name] = true
		}
		return cloneRegistrationResult(runtimes.Result{AgentID: saved.AgentID, AgentType: saved.AgentType,
			Package: saved.Package, Runtime: runtimes.RuntimeKubernetes, Actions: snapshot.Actions}), nil
	}
}

func cloneRegistrationResult(result runtimes.Result) runtimes.Result {
	result.Actions = append([]actions.Action{}, result.Actions...)
	for i := range result.Actions {
		result.Actions[i] = result.Actions[i].Clone()
	}
	return result
}
