package operations

import "time"

func (o *Operation) Cancel() error {
	if o.Status.Terminal() {
		return ErrFinished
	}
	now := time.Now().UTC()
	o.Status = Cancelled
	for i := range o.Tasks {
		if o.Tasks[i].Status == TaskQueued {
			o.Tasks[i].Status = TaskCancelled
			o.Tasks[i].FinishedAt = &now
		}
	}
	o.AppendEvent(OperationCancelled, "", "", "New assignments cancelled; dispatched tasks may still finish")
	o.UpdatedAt = time.Now().UTC()
	return nil
}
