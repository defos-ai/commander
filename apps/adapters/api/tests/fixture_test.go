package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
)

type object = map[string]any

const absentID = "00000000-0000-4000-8000-000000ffffff"

type api struct {
	t        *testing.T
	ctx      context.Context
	url      string
	username string
	password string
	client   *http.Client
}

type response struct {
	status int
	header http.Header
	body   string
}

func connect(t *testing.T) *api {
	t.Helper()
	a := &api{t: t, ctx: t.Context(), url: strings.TrimRight(environment("COMMANDER_TEST_API_URL", "http://127.0.0.1:14274"), "/"),
		username: environment("COMMANDER_TEST_API_USERNAME", "api-fixture-user"), password: environment("COMMANDER_TEST_API_PASSWORD", "api-fixture-password"),
		client: &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}}}
	t.Cleanup(a.client.CloseIdleConnections)
	ctx, cancel := context.WithTimeout(a.ctx, 90*time.Second)
	defer cancel()
	confirmedTestEndpoint := false
	for {
		result, err := a.send(ctx, http.MethodPost, "/api/v1/testing/cleanup", nil)
		if err != nil {
			t.Fatalf("connect to test API at %s: %v; start the Compose test stack with ENV=tests", a.url, err)
		}
		if result.status != http.StatusNoContent && (result.status != http.StatusConflict || errorCode(result) != "test_state_busy") {
			t.Fatalf("test cleanup requires ENV=tests: status=%d body=%s", result.status, result.body)
		}
		if !confirmedTestEndpoint {
			confirmedTestEndpoint = true
			// Establish the test-only endpoint before cancelling any saved work.
			// Teardown also survives a failed test or cancellation of t.Context.
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				if err := a.settle(ctx); err != nil {
					t.Errorf("settle shared API state after test: %v", err)
				}
			})
		}
		if result.status == http.StatusNoContent {
			break
		}
		if err := a.settle(ctx); err != nil {
			t.Fatal(err)
		}
		if err := pause(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for {
		catalog := read[models.ListResponse[models.AgentResponse]](a, "/api/v1/agents")
		ready := true
		types := map[string]int{}
		for _, agent := range catalog.Items {
			types[agent.Type]++
			expected := expectedAgentActions(agent.Type)
			ready = ready && len(expected) > 0 && agent.Status == "ready" && !agent.Stale && agent.Work == nil && len(agent.Actions) == len(expected)
			for _, action := range agent.Actions {
				ready = ready && action.Status == "ready"
			}
		}
		ready = ready && types["web-agent"] == 2 && types["browser-agent"] == 1 && types["builder-agent"] == 1 && types["spawner-agent"] == 1 && types["fixture-agent"] >= 1
		actions := read[models.ListResponse[models.ActionResponse]](a, "/api/v1/actions")
		missions := read[models.ListResponse[models.MissionResponse]](a, "/api/v1/missions")
		if ready && slices.Equal(actionNames(actions.Items), expectedCatalogActions(catalog.Items)) && slices.Equal(missionNames(missions.Items), expectedMissions) {
			break
		}
		if err := pause(ctx); err != nil {
			t.Fatalf("catalog did not recover after cleanup: agents=%+v actions=%d missions=%d: %v", catalog.Items, len(actions.Items), len(missions.Items), err)
		}
	}
	if operations := read[models.ListResponse[models.OperationResponse]](a, "/api/v1/operations"); len(operations.Items) != 0 {
		t.Fatal("cleanup retained operations from a previous test")
	}
	return a
}

func expectedAgentActions(agentType string) []string {
	switch agentType {
	case "web-agent":
		return []string{"check-links", "check-seo", "detect-technologies", "discover-pages", "discover-pages-no-browser"}
	case "browser-agent":
		return []string{"capture-page", "check-accessibility"}
	case "spawner-agent":
		return []string{"spawn-agent"}
	case "builder-agent":
		return []string{"checkout-template"}
	case "fixture-agent":
		return []string{"fixture-echo"}
	default:
		return nil
	}
}

var expectedMissions = []string{"fixture-use-agent", "install-agent", "review-website-no-browser", "review-website-with-browser"}

func expectedCatalogActions(agents []models.AgentResponse) []string {
	var names []string
	for _, agent := range agents {
		names = append(names, expectedAgentActions(agent.Type)...)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func actionNames(actions []models.ActionResponse) []string {
	var names []string
	for _, action := range actions {
		names = append(names, action.Name)
	}
	return names
}

func missionNames(missions []models.MissionResponse) []string {
	var names []string
	for _, mission := range missions {
		names = append(names, mission.Name)
	}
	return names
}

func expectedExecutorType(action string) string {
	if action == "capture-page" || action == "check-accessibility" {
		return "browser-agent"
	}
	return "web-agent"
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (a *api) send(ctx context.Context, method, path string, body any, configure ...func(*http.Request)) (response, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return response{}, err
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, a.url+path, bytes.NewReader(data))
	if err != nil {
		return response{}, err
	}
	request.SetBasicAuth(a.username, a.password)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, change := range configure {
		change(request)
	}
	result, err := a.client.Do(request)
	if err != nil {
		return response{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer result.Body.Close()
	data, err = io.ReadAll(result.Body)
	return response{status: result.StatusCode, header: result.Header, body: string(data)}, err
}

func (a *api) request(method, path string, body any, configure ...func(*http.Request)) response {
	a.t.Helper()
	result, err := a.send(a.ctx, method, path, body, configure...)
	if err != nil {
		a.t.Fatal(err)
	}
	return result
}

func read[T any](a *api, path string) T {
	a.t.Helper()
	return decode[T](a.t, a.request(http.MethodGet, path, nil), http.StatusOK)
}

func post[T any](a *api, path string, body any, status int) T {
	a.t.Helper()
	return decode[T](a.t, a.request(http.MethodPost, path, body), status)
}

func decode[T any](t *testing.T, result response, status int) T {
	t.Helper()
	if result.status != status {
		t.Fatalf("status=%d, want %d: %s", result.status, status, result.body)
	}
	var value T
	if err := json.Unmarshal([]byte(result.body), &value); err != nil {
		t.Fatalf("decode response: %v: %s", err, result.body)
	}
	return value
}

func assertError(t *testing.T, result response, status int, code string) {
	t.Helper()
	var payload map[string]string
	if result.status != status || json.Unmarshal([]byte(result.body), &payload) != nil || len(payload) != 1 || payload["error"] != code {
		t.Fatalf("status=%d body=%s, want %d with error %q", result.status, result.body, status, code)
	}
}

func errorCode(result response) string {
	var payload struct{ Error string }
	_ = json.Unmarshal([]byte(result.body), &payload)
	return payload.Error
}

func (a *api) start(url string) models.OperationResponse {
	a.t.Helper()
	return post[models.OperationResponse](a, "/api/v1/operations", object{"mission_name": "review-website-with-browser", "input": object{"url": url, "max_pages": 2}}, http.StatusCreated)
}

func operationPath(id string) string { return "/api/v1/operations/" + id }

func (a *api) task(id, action, url string) models.TaskResponse {
	a.t.Helper()
	return post[models.TaskResponse](a, operationPath(id)+"/tasks", object{"action_name": action, "input": object{"url": url}}, http.StatusCreated)
}

func (a *api) wait(id string, done func(models.OperationResponse) bool) models.OperationResponse {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(a.ctx, 90*time.Second)
	defer cancel()
	for {
		last := read[models.OperationResponse](a, operationPath(id))
		if done(last) {
			return last
		}
		if err := pause(ctx); err != nil {
			data, _ := json.Marshal(last)
			a.t.Fatalf("operation did not reach expected state: %s: %v", data, err)
		}
	}
}

func tasksSettled(op models.OperationResponse) bool {
	for _, task := range op.Tasks {
		if task.Status == "queued" || task.Status == "running" || task.Status == "interrupted" {
			return false
		}
	}
	return true
}

func modelDecision(op models.OperationResponse) *models.TaskResponse {
	for _, task := range op.Tasks {
		if c := task.Continuation; c != nil && c.Status == "pending" && c.DecisionBy == "model" {
			return &task
		}
	}
	return nil
}

func (a *api) resolve(id string, task models.TaskResponse, actions []object) models.OperationResponse {
	a.t.Helper()
	return post[models.OperationResponse](a, operationPath(id)+"/tasks/"+task.ID+"/continuation/resolve", object{"version": task.Continuation.Version, "actions": actions}, http.StatusOK)
}

func (a *api) complete(id, url, summary string) models.OperationResponse {
	a.t.Helper()
	return post[models.OperationResponse](a, operationPath(id)+"/complete", object{"output": object{"url": url, "summary": summary, "findings": []object{}}}, http.StatusOK)
}

func (a *api) settle(ctx context.Context) error {
	for {
		result, err := a.send(ctx, http.MethodGet, "/api/v1/operations", nil)
		if err != nil {
			return err
		}
		var operations models.ListResponse[models.OperationResponse]
		if result.status != http.StatusOK || json.Unmarshal([]byte(result.body), &operations) != nil {
			return fmt.Errorf("list operations: status=%d body=%s", result.status, result.body)
		}
		busy := false
		for _, op := range operations.Items {
			terminal := op.Status == "completed" || op.Status == "cancelled" || op.Status == "failed"
			if !terminal {
				// Questions are test data: finish their lifecycle before cancellation,
				// which prevents a terminal operation retaining a long pending timer.
				for _, question := range op.Questions {
					if question.Status == "pending" {
						answer, err := a.send(ctx, http.MethodPost, operationPath(op.ID)+"/questions/"+question.ID+"/answer", object{"answer": "End this test review"})
						if err != nil {
							return err
						}
						if answer.status != http.StatusOK && answer.status != http.StatusConflict {
							return fmt.Errorf("settle question: status=%d body=%s", answer.status, answer.body)
						}
					}
				}
				cancelled, err := a.send(ctx, http.MethodPost, operationPath(op.ID)+"/cancel", nil)
				if err != nil {
					return err
				}
				if cancelled.status != http.StatusOK && !(cancelled.status == http.StatusConflict && errorCode(cancelled) == "operation_finished") {
					return fmt.Errorf("cancel leftover operation: status=%d body=%s", cancelled.status, cancelled.body)
				}
				busy = true
			}
			busy = busy || !tasksSettled(op)
			for _, question := range op.Questions {
				busy = busy || question.Status == "pending"
			}
		}
		inventory, err := a.send(ctx, http.MethodGet, "/api/v1/agents", nil)
		if err != nil {
			return err
		}
		var agents models.ListResponse[models.AgentResponse]
		if inventory.status != http.StatusOK || json.Unmarshal([]byte(inventory.body), &agents) != nil {
			return fmt.Errorf("read agents during settlement: status=%d body=%s", inventory.status, inventory.body)
		}
		for _, agent := range agents.Items {
			busy = busy || agent.Work != nil || agent.Status == "working"
		}
		if !busy {
			return nil
		}
		if err := pause(ctx); err != nil {
			return fmt.Errorf("work still active: operations=%s agents=%s: %w", result.body, inventory.body, err)
		}
	}
}

func pause(ctx context.Context) error {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Only the website fixture runs on the host; all Commander work runs in Compose.
func website(t *testing.T) string {
	t.Helper()
	return serveWebsite(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Powered-By", "Express")
		fmt.Fprint(w, `<!doctype html><html lang="en"><head><title>Commander test site</title><meta name="generator" content="WordPress 6.8"></head><body><h1>Release review</h1><a href="/about">About</a><a href="/missing">Broken link</a><img src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw=="></body></html>`)
	}))
}

func serveWebsite(t *testing.T, handler http.Handler) string {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return "http://" + net.JoinHostPort(environment("COMMANDER_TEST_WEBSITE_HOST", "host.docker.internal"), fmt.Sprint(listener.Addr().(*net.TCPAddr).Port))
}
