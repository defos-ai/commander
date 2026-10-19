package api_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
)

func TestAgentsTrackDispatchedWorkUntilCancelledTaskFinishes(t *testing.T) {
	a := connect(t)
	initial := read[models.ListResponse[models.AgentResponse]](a, "/api/v1/agents")
	if !slices.IsSortedFunc(initial.Items, func(left, right models.AgentResponse) int { return strings.Compare(left.ID, right.ID) }) {
		t.Fatalf("agent inventory is not sorted: %+v", initial)
	}
	for _, agent := range initial.Items {
		result := a.request(http.MethodGet, "/api/v1/agents/"+agent.ID, nil)
		detail := decode[models.AgentResponse](t, result, http.StatusOK)
		if detail.ID != agent.ID || detail.Stale || detail.Work != nil || detail.Status != "ready" {
			t.Fatalf("agent is not ready: %+v", detail)
		}
		assertSchema(t, a, "Agent", result)
	}
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	url := serveWebsite(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><head><title>Held review</title></head><body><h1>Review</h1></body></html>")
	}))
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	op := a.start(url)
	task := a.task(op.ID, "check-seo", url)
	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("agent did not reach the controlled website")
	}
	running := a.wait(op.ID, func(op models.OperationResponse) bool { return len(op.Tasks) == 1 && op.Tasks[0].Status == "running" })
	var worker models.AgentResponse
	deadline := time.Now().Add(10 * time.Second)
	for {
		worker = read[models.AgentResponse](a, "/api/v1/agents/"+running.Tasks[0].AgentID)
		if worker.Status == "working" && worker.Work != nil && worker.Work.TaskID == task.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("inventory did not report dispatched task: %+v", worker)
		}
		if err := pause(a.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if worker.Type != "web-agent" || worker.Stale || worker.Work.OperationID != op.ID || worker.Work.CommandID != task.CommandID || worker.Work.ActionName != "check-seo" {
		t.Fatalf("work ownership differs: %+v", worker)
	}
	assertError(t, a.request(http.MethodPost, "/api/v1/testing/cleanup", nil), http.StatusConflict, "test_state_busy")
	assertError(t, a.request(http.MethodPost, operationPath(op.ID)+"/complete", object{"output": object{"url": url, "summary": "Too early", "findings": []object{}}}), http.StatusConflict, "operation_active_tasks")
	cancelled := post[models.OperationResponse](a, operationPath(op.ID)+"/cancel", nil, http.StatusOK)
	if cancelled.Status != "cancelled" || cancelled.Tasks[0].Status != "running" {
		t.Fatalf("cancellation lost dispatched work: %+v", cancelled)
	}
	assertError(t, a.request(http.MethodPost, "/api/v1/testing/cleanup", nil), http.StatusConflict, "test_state_busy")
	once.Do(func() { close(release) })
	finished := a.wait(op.ID, tasksSettled)
	if finished.Status != "cancelled" || finished.Tasks[0].Status != "succeeded" || finished.Tasks[0].Result == nil {
		t.Fatalf("late result lost terminal operation status: %+v", finished)
	}
	assertError(t, a.request(http.MethodGet, "/api/v1/agents/not-valid", nil), http.StatusNotFound, "agent_not_found")
	assertError(t, a.request(http.MethodGet, "/api/v1/agents/unknown-agent-1234abcd", nil), http.StatusNotFound, "agent_not_found")
}
