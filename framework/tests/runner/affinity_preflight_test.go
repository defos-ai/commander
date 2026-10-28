package runner_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestAffinityPreflightRequiresCommonCapabilities(t *testing.T) {
	checkout := fixtures.AffinityPreflightAgent("builder-agent-aaaaaaaa", "checkout-template")
	build := fixtures.AffinityPreflightAgent("builder-agent-bbbbbbbb", "build-agent")
	other := fixtures.AffinityPreflightAgent("review-agent-cccccccc", "other-action")
	common := fixtures.AffinityPreflightAgent("builder-agent-dddddddd", "checkout-template", "build-agent")
	missing := actions.Requirement{Kind: actions.SecretRequirement, Name: "BUILD_TOKEN"}
	for _, test := range []struct {
		name   string
		common bool
		change func(*agents.Agent)
		ready  bool
	}{
		{name: "split capabilities cannot satisfy the group"},
		{name: "common agent", common: true, ready: true},
		{name: "busy common agent remains ready", common: true, ready: true, change: func(a *agents.Agent) { a.Status = agents.AgentWorking }},
		{name: "common agent reports each action readiness", common: true, change: func(a *agents.Agent) {
			a.Actions[1].Status = actions.Unavailable
			a.Actions[1].RequiredSecrets = []string{missing.Name}
			a.Actions[1].MissingRequirements = []actions.Requirement{missing}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			op := fixtures.AffinityOperation()
			// The group must be assessed even when it is absent from required_actions.
			op.Mission.RequiredActions = []string{"other-action"}
			inventory := []agents.Agent{checkout, build, other}
			if test.common {
				candidate := common.Clone()
				if test.change != nil {
					test.change(&candidate)
				}
				inventory = append(inventory, candidate)
			}
			preflight, err := op.AssessPreflight(inventory)
			if err != nil {
				t.Fatal(err)
			}
			if preflight.Ready != test.ready || len(preflight.Actions) != 3 {
				t.Fatalf("group coverage = %+v, want ready %v and three actions", preflight, test.ready)
			}
			for _, coverage := range preflight.Actions {
				switch coverage.ActionName {
				case "other-action":
					if !slices.Equal(coverage.ReadyAgentIDs, []string{other.ID}) {
						t.Fatalf("affinity restricted an unrelated action: %+v", coverage)
					}
				case "checkout-template", "build-agent":
					if !test.common {
						if len(coverage.ReadyAgentIDs) != 0 {
							t.Fatalf("split capabilities satisfy affinity: %+v", coverage)
						}
						if len(coverage.UnavailableAgents) == 0 {
							t.Fatalf("split capability wait has no explanation: %+v", coverage)
						}
						for _, candidate := range coverage.UnavailableAgents {
							if !strings.Contains(candidate.Reason, "same-agent") || len(candidate.MissingRequirements) != 0 {
								t.Fatalf("ineligible candidate has misleading readiness: %+v", candidate)
							}
						}
						continue
					}
					if !test.ready && coverage.ActionName == "build-agent" {
						index := slices.IndexFunc(coverage.UnavailableAgents, func(candidate operations.AgentReadiness) bool {
							return candidate.AgentID == common.ID
						})
						if len(coverage.ReadyAgentIDs) != 0 || index < 0 {
							t.Fatalf("missing build requirement was hidden: %+v", coverage)
						}
						candidate := coverage.UnavailableAgents[index]
						if !slices.Equal(candidate.MissingRequirements, []actions.Requirement{missing}) {
							t.Fatalf("wrong grouped candidate requirement: %+v", candidate)
						}
					} else if !slices.Equal(coverage.ReadyAgentIDs, []string{common.ID}) {
						t.Fatalf("group includes an agent without all capabilities: %+v", coverage)
					}
				default:
					t.Fatalf("unexpected action in preflight: %+v", coverage)
				}
			}
		})
	}
}

func TestAffinityPreflightUsesOnlySavedOwner(t *testing.T) {
	owner := fixtures.AffinityPreflightAgent("builder-agent-aaaaaaaa", "checkout-template", "build-agent")
	other := fixtures.AffinityPreflightAgent("builder-agent-bbbbbbbb", "checkout-template", "build-agent", "other-action")
	// This requirement must never be suggested after another agent owns the group.
	other.Actions[1].Status = actions.Unavailable
	other.Actions[1].RequiredSecrets = []string{"OTHER_TOKEN"}
	other.Actions[1].MissingRequirements = []actions.Requirement{{Kind: actions.SecretRequirement, Name: "OTHER_TOKEN"}}
	missing := actions.Requirement{Kind: actions.SecretRequirement, Name: "OWNER_TOKEN"}
	for _, test := range []struct {
		name    string
		change  func(*agents.Agent)
		absent  bool
		ready   bool
		missing []actions.Requirement
	}{
		{name: "owner ready", ready: true},
		{name: "owner needs a secret", missing: []actions.Requirement{missing}, change: func(a *agents.Agent) {
			a.Actions[1].Status = actions.Unavailable
			a.Actions[1].RequiredSecrets = []string{missing.Name}
			a.Actions[1].MissingRequirements = []actions.Requirement{missing}
		}},
		{name: "owner stopped", change: func(a *agents.Agent) { a.Status = agents.AgentStopped }},
		{name: "owner stale with old requirements", change: func(a *agents.Agent) {
			a.Stale = true
			a.Actions[1].MissingRequirements = []actions.Requirement{missing}
		}},
		{name: "owner no longer advertises action", change: func(a *agents.Agent) { a.Actions = a.Actions[:1] }},
		{name: "owner absent", absent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			op := fixtures.AffinityOperation()
			op.Mission.RequiredActions = []string{"other-action"}
			dispatchAffinityTask(t, &op, 0, owner.ID)
			inventory := []agents.Agent{other}
			if !test.absent {
				candidate := owner.Clone()
				if test.change != nil {
					test.change(&candidate)
				}
				inventory = append(inventory, candidate)
			}
			preflight, err := op.AssessPreflight(inventory)
			if err != nil {
				t.Fatal(err)
			}
			if preflight.Ready != test.ready {
				t.Fatalf("bound preflight = %+v, want ready %v", preflight, test.ready)
			}
			for _, coverage := range preflight.Actions {
				if coverage.ActionName == "other-action" {
					if !slices.Equal(coverage.ReadyAgentIDs, []string{other.ID}) {
						t.Fatalf("bound group affected unrelated action: %+v", coverage)
					}
					continue
				}
				for _, readyID := range coverage.ReadyAgentIDs {
					if readyID != owner.ID {
						t.Fatalf("group reassigned away from its owner: %+v", coverage)
					}
				}
				for _, candidate := range coverage.UnavailableAgents {
					if candidate.AgentID != owner.ID || candidate.Reason == "" {
						t.Fatalf("owner wait is not explained or suggests another Agent: %+v", coverage)
					}
				}
				if coverage.ActionName == "build-agent" && !test.ready {
					if len(coverage.ReadyAgentIDs) != 0 || len(coverage.UnavailableAgents) != 1 {
						t.Fatalf("owner failure did not block the grouped action: %+v", coverage)
					}
					if got := coverage.UnavailableAgents[0].MissingRequirements; !slices.Equal(got, test.missing) {
						t.Fatalf("requirements = %v, want only current owner requirements %v", got, test.missing)
					}
				}
			}
		})
	}
}

func TestAffinityPreflightRefreshPreservesVersionAndRejectsConflictingHistory(t *testing.T) {
	owner := fixtures.AffinityPreflightAgent("builder-agent-aaaaaaaa", "checkout-template", "build-agent")
	op := fixtures.AffinityOperation()
	dispatchAffinityTask(t, &op, 0, owner.ID)
	for _, state := range []struct {
		inventory []agents.Agent
		changed   bool
		version   uint64
		ready     bool
	}{
		{inventory: []agents.Agent{owner}, changed: true, version: 1, ready: true},
		{changed: true, version: 2},
		{version: 2},
		{inventory: []agents.Agent{owner}, changed: true, version: 3, ready: true},
		{inventory: []agents.Agent{owner}, version: 3, ready: true},
	} {
		changed, err := op.RefreshPreflight(state.inventory)
		if err != nil || changed != state.changed || op.Preflight == nil || op.Preflight.Version != state.version || op.Preflight.Ready != state.ready {
			t.Fatalf("RefreshPreflight() = %v, %v, %+v; want changed %v, version %d, ready %v", changed, err, op.Preflight, state.changed, state.version, state.ready)
		}
		assertAffinity(t, op, owner.ID)
	}

	dispatchAffinityTask(t, &op, 1, owner.ID)
	op.Tasks[1].AgentID = "builder-agent-bbbbbbbb"
	before := op.Clone()
	if _, err := op.AssessPreflight([]agents.Agent{owner}); !errors.Is(err, operations.ErrOperationConflict) {
		t.Fatalf("assessment accepted conflicting ownership: %v", err)
	}
	changed, err := op.RefreshPreflight([]agents.Agent{owner})
	if changed || !errors.Is(err, operations.ErrOperationConflict) || !reflect.DeepEqual(op.Clone(), before) {
		t.Fatalf("conflicting refresh changed saved operation: changed %v, error %v", changed, err)
	}
}
