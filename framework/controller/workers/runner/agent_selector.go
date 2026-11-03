package runner

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
)

// AgentSelector chooses from candidates satisfying affinity, readiness and
// capacity without dispatching work.
// Calls are serial within a runner and must return promptly and honor ctx.
// An empty ID defers selection; errors leave work queued for retry.
type AgentSelector interface {
	Name() string
	Select(ctx context.Context, selection AgentSelection) (agentID string, err error)
}

type AgentSelection struct {
	Task       operations.Task
	Candidates []AgentCandidate
	// PreviousAgentID changes only after a dispatch is durably reserved.
	PreviousAgentID string
}

// Candidate snapshots belong to this selection call. Capacity and RunningTasks
// describe work owned by this runner, not shared capacity across controllers.
type AgentCandidate struct {
	Agent        agents.Agent
	RunningTasks int
	Capacity     int
}

type RoundRobinSelector struct{}

func (RoundRobinSelector) Name() string { return "round-robin" }

func (RoundRobinSelector) Select(ctx context.Context, selection AgentSelection) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var first, next string
	for _, candidate := range selection.Candidates {
		id := candidate.Agent.ID
		if first == "" || id < first {
			first = id
		}
		if id > selection.PreviousAgentID && (next == "" || id < next) {
			next = id
		}
	}
	if next != "" {
		return next, nil
	}
	return first, nil
}
