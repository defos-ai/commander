package operations

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
