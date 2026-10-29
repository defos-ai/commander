package operations

import (
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/id"
)

// AssessTaskRequirements projects a live wait for this exact saved execution.
// It does not change task state or extend the execution deadline. Callers must
// suppress this projection when the parent operation is terminal.
func AssessTaskRequirements(task Task, record agents.Agent, now time.Time) []actions.Requirement {
	if task.Status != TaskRunning || task.DeadlineAt == nil || !now.Before(*task.DeadlineAt) ||
		!id.Valid(task.OperationID) || !id.Valid(task.ID) || !id.Valid(task.CommandID) ||
		!id.ValidAgent(task.AgentID) || !id.ValidType(string(record.Type)) || strings.TrimSpace(task.ActionName) == "" {
		return nil
	}
	runtimeType, validID := id.RuntimeType(record.ID, id.Agent)
	if !validID || runtimeType != strings.TrimSuffix(string(record.Type), "-agent") || record.Stale || record.ID != task.AgentID {
		return nil
	}
	record = record.Snapshot(now)
	work := record.Work
	if record.Stale || record.Status != agents.AgentWorking || work == nil ||
		work.OperationID != task.OperationID || work.TaskID != task.ID ||
		work.CommandID != task.CommandID || work.ActionName != task.ActionName {
		return nil
	}
	var missing []actions.Requirement
	seen := make(map[string]bool)
	for _, requirement := range work.MissingRequirements {
		if requirement.Kind == actions.SecretRequirement && requirement.Validate() == nil && !seen[requirement.Name] {
			seen[requirement.Name] = true
			missing = append(missing, requirement)
		}
	}
	return missing
}
