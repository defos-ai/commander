package agent

import (
	"context"
	"slices"

	"github.com/defos-ai/commander/framework/agent/action"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

// One execution owns these counts, serialized with snapshots by stateGate.
// Secret transport requests and cached values retain their agent-wide lifetime.
type workSecretWaits struct {
	agent     *Agent
	context   context.Context
	commandID string
	announce  func(context.Context, AgentSnapshot) error
	counts    map[string]int
}

func (w *workSecretWaits) begin(ctx context.Context, name string) (func(), error) {
	if err := w.change(ctx, name, 1); err != nil {
		return nil, err
	}
	return func() { _ = w.change(context.WithoutCancel(ctx), name, -1) }, nil
}

func (w *workSecretWaits) change(ctx context.Context, name string, delta int) error {
	return w.agent.stateChange(ctx, func(next *agentState) error {
		// Detached callers cannot add or remove evidence after this execution ends.
		if err := w.context.Err(); err != nil {
			return err
		}
		if next.status != AgentWorking || next.work == nil || next.work.CommandID != w.commandID {
			return action.ErrSecretUnavailable
		}
		w.counts[name] += delta
		if delta > 0 && w.counts[name] > 1 || delta < 0 && w.counts[name] > 0 {
			return nil
		}
		if w.counts[name] == 0 {
			delete(w.counts, name)
		}
		names := make([]string, 0, len(w.counts))
		for name := range w.counts {
			names = append(names, name)
		}
		slices.Sort(names)
		next.work = next.work.Clone()
		next.work.MissingRequirements = nil
		for _, name := range names {
			next.work.MissingRequirements = append(next.work.MissingRequirements, actionmodel.Requirement{Kind: actionmodel.SecretRequirement, Name: name})
		}
		return nil
	}, w.announce)
}

func (w *workSecretWaits) finish() {
	if w == nil {
		return
	}
	changed := false
	_ = w.agent.stateChange(context.WithoutCancel(w.context), func(next *agentState) error {
		if next.work != nil && next.work.CommandID == w.commandID && len(next.work.MissingRequirements) > 0 {
			next.work = next.work.Clone()
			next.work.MissingRequirements = nil
			changed = true
		}
		return nil
	}, w.announcement(&changed))
}

func (w *workSecretWaits) announcement(changed *bool) func(context.Context, AgentSnapshot) error {
	return func(ctx context.Context, snapshot AgentSnapshot) error {
		if *changed && w.announce != nil {
			return w.announce(ctx, snapshot)
		}
		return nil
	}
}
