package fakes

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
)

type CheckpointDroppingBus struct {
	comms.Bus
	armed    atomic.Bool
	lost     atomic.Bool
	dropped  chan struct{}
	release  chan struct{}
	released bool
}

func NewCheckpointDroppingBus(bus comms.Bus) *CheckpointDroppingBus {
	return &CheckpointDroppingBus{Bus: bus, dropped: make(chan struct{}, 1), release: make(chan struct{})}
}

func (bus *CheckpointDroppingBus) Arm() { bus.armed.Store(true) }

func (bus *CheckpointDroppingBus) Dropped() <-chan struct{} { return bus.dropped }

func (bus *CheckpointDroppingBus) Unblock() {
	if !bus.released {
		bus.released = true
		close(bus.release)
	}
}

func (bus *CheckpointDroppingBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	if message.Type == agentevents.CheckpointResponded && bus.armed.Load() {
		if !bus.lost.Swap(true) {
			bus.dropped <- struct{}{}
			return nil
		}
		select {
		case <-bus.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return bus.Bus.Publish(ctx, topic, message)
}

type LateArtifactAction struct {
	*action.Base
	started     chan struct{}
	release     chan struct{}
	released    bool
	invocations atomic.Int32
	propose     bool
	contents    string
}

func NewLateArtifactAction(contents string, propose bool) *LateArtifactAction {
	return &LateArtifactAction{Base: action.NewBase(actionmodel.Action{Name: "late-artifact",
		InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}),
		started: make(chan struct{}, 1), release: make(chan struct{}), contents: contents, propose: propose}
}

func (a *LateArtifactAction) Started() <-chan struct{} { return a.started }

func (a *LateArtifactAction) Invocations() int32 { return a.invocations.Load() }

func (a *LateArtifactAction) Unblock() {
	if !a.released {
		a.released = true
		close(a.release)
	}
}

func (a *LateArtifactAction) Run(ctx context.Context, _ json.RawMessage) (action.Result, error) {
	invocation := a.invocations.Add(1)
	select {
	case a.started <- struct{}{}:
	default:
	}
	select {
	case <-a.release:
	case <-ctx.Done():
		return action.Result{}, ctx.Err()
	}
	result := action.Result{Output: json.RawMessage(`{"summary":"Source ready"}`), Artifacts: []action.Artifact{{
		Name: "Agent source", Files: []artifacts.FileSource{{Name: "source.tar.gz", MediaType: "application/gzip", Data: []byte(a.contents)}},
	}}}
	if a.propose && invocation == 1 {
		result.NextActions = []action.NextAction{{ActionName: "late-artifact", Input: json.RawMessage(`{}`)}}
	}
	return result, nil
}

type SecretObservingBus struct {
	comms.Bus
	delivered chan messages.Message
}

func NewSecretObservingBus(bus comms.Bus) *SecretObservingBus {
	return &SecretObservingBus{Bus: bus, delivered: make(chan messages.Message, 256)}
}

func (bus *SecretObservingBus) Deliveries() <-chan messages.Message { return bus.delivered }

func (bus *SecretObservingBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	if err := bus.Bus.Publish(ctx, topic, message); err != nil {
		return err
	}
	if message.Type == agentevents.SecretDelivered {
		select {
		case bus.delivered <- message:
		default:
		}
	}
	return nil
}
