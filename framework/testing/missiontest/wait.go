package missiontest

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

// WaitFor observes saved work without answering questions or resolving reviews.
// The predicate may recognize expected failures or make explicit decisions.
func (d *Driver) WaitFor(operationID string, ready func(operations.Operation) bool) operations.Operation {
	d.t.Helper()
	path := d.operationPath(operationID)
	if ready == nil {
		d.t.Fatal("missiontest: wait requires a predicate")
	}
	ctx, cancel := context.WithTimeout(d.t.Context(), d.timeout)
	defer cancel()
	var current operations.Operation
	for {
		var snapshot operations.Operation
		if err := d.call(ctx, http.MethodGet, path, nil, http.StatusOK, &snapshot); err != nil {
			d.t.Fatalf("missiontest: observing operation %s: %v; %s", operationID, err, summary(current))
		}
		if snapshot.ID != operationID {
			d.t.Fatal("missiontest: observed response does not identify the requested operation")
		}
		current = snapshot
		if ready(snapshot.Clone()) {
			return current
		}
		if !pause(ctx, d.poll) {
			d.t.Fatalf("missiontest: waiting for operation %s: %v; %s", operationID, ctx.Err(), summary(current))
		}
	}
}

// WaitForTask returns an observed outcome for one exact task. Callers assert
// success or expected failure; interrupted still means the outcome is unknown.
func (d *Driver) WaitForTask(operationID, taskID string) operations.Task {
	d.t.Helper()
	if !id.Valid(taskID) {
		d.t.Fatal("missiontest: task ID must be a canonical UUID")
	}
	var result operations.Task
	d.WaitFor(operationID, func(operation operations.Operation) bool {
		for _, task := range operation.Tasks {
			if task.ID != taskID {
				continue
			}
			switch task.Status {
			case operations.TaskSucceeded, operations.TaskFailed, operations.TaskCancelled, operations.TaskInterrupted:
				result = task
				return true
			}
		}
		return false
	})
	return result
}

func (d *Driver) WaitForContinuation(operationID, taskID string) operations.Task {
	d.t.Helper()
	if !id.Valid(taskID) {
		d.t.Fatal("missiontest: task ID must be a canonical UUID")
	}
	var result operations.Task
	d.WaitFor(operationID, func(operation operations.Operation) bool {
		for _, task := range operation.Tasks {
			if task.ID == taskID && task.Continuation != nil && task.Continuation.Status == operations.ContinuationPending {
				result = task
				return true
			}
		}
		return false
	})
	return result
}

func (d *Driver) WaitForActions(names ...string) {
	d.t.Helper()
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			d.t.Fatal("missiontest: action names must not be blank")
		}
	}
	ctx, cancel := context.WithTimeout(d.t.Context(), d.timeout)
	defer cancel()
	for {
		ready, missing, err := d.readiness(ctx, names)
		if err != nil {
			d.t.Fatalf("missiontest: reading readiness: %v", err)
		}
		if ready && len(missing) == 0 {
			return
		}
		if !pause(ctx, d.poll) {
			d.t.Fatalf("missiontest: readiness wait ended: %v; controller ready=%t; missing actions=%v", ctx.Err(), ready, missing)
		}
	}
}

func (d *Driver) readiness(ctx context.Context, names []string) (bool, []string, error) {
	type host struct {
		Status  string `json:"status"`
		Stale   bool   `json:"stale"`
		Actions []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"actions"`
	}
	var controllers, agents struct {
		Items []host `json:"items"`
	}
	if err := d.call(ctx, http.MethodGet, "/api/v1/controllers", nil, http.StatusOK, &controllers); err != nil {
		return false, nil, err
	}
	if err := d.call(ctx, http.MethodGet, "/api/v1/agents", nil, http.StatusOK, &agents); err != nil {
		return false, nil, err
	}
	controllerReady := slices.ContainsFunc(controllers.Items, func(item host) bool { return item.Status == "ready" && !item.Stale })
	available := make(map[string]bool)
	for _, agent := range agents.Items {
		if agent.Stale || agent.Status != "ready" {
			continue
		}
		for _, action := range agent.Actions {
			if action.Status == "ready" {
				available[action.Name] = true
			}
		}
	}
	var missing []string
	for _, name := range names {
		if !available[name] {
			missing = append(missing, name)
		}
	}
	return controllerReady, missing, nil
}

func pause(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ctx.Err() == nil
	case <-ctx.Done():
		return false
	}
}

func summary(operation operations.Operation) string {
	states := []string{fmt.Sprintf("operation %s=%s", operation.ID, operation.Status)}
	for _, task := range operation.Tasks {
		states = append(states, fmt.Sprintf("task %s action=%s status=%s", task.ID, task.ActionName, task.Status))
		if review := task.Continuation; review != nil && review.Status != operations.ContinuationResolved {
			states = append(states, fmt.Sprintf("review task=%s version=%d status=%s", task.ID, review.Version, review.Status))
		}
	}
	for _, question := range operation.Questions {
		if question.Pending() {
			states = append(states, "pending question="+question.ID)
		}
	}
	return strings.Join(states, "; ")
}
