package operations

import (
	"fmt"

	"github.com/defos-ai/commander/framework/domain"
)

func (operation Operation) ValidateStep(stepID string) error {
	if stepID == "" {
		return nil
	}
	if len(operation.Mission.Steps) == 0 {
		return fmt.Errorf("%w: unknown mission step %q: mission %q declares no steps; omit step_id and use action_name when assigning a task", domain.ErrInvalidInput, stepID, operation.Mission.Name)
	}
	for _, step := range operation.Mission.Steps {
		if step.ID == stepID {
			return nil
		}
	}
	return fmt.Errorf("%w: unknown mission step %q for mission %q; use a step ID declared in mission.steps", domain.ErrInvalidInput, stepID, operation.Mission.Name)
}

// RefreshStatus derives the operation status without reopening a terminal operation.
func (operation *Operation) RefreshStatus() {
	if operation.Status.Terminal() {
		return
	}
	operation.Status = Planning
	for _, task := range operation.Tasks {
		if task.Status == TaskRunning {
			operation.Status = Running
			return
		}
	}
	if operation.Preflight != nil && !operation.Preflight.Ready {
		operation.Status = Waiting
		return
	}
	for _, question := range operation.Questions {
		if question.Pending() {
			operation.Status = Waiting
			return
		}
	}
	for _, task := range operation.Tasks {
		if task.Status == TaskQueued {
			operation.Status = Running
			return
		}
	}
}
