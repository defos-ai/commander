package fixtures

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
)

type Object = map[string]any

const AbsentID = "00000000-0000-4000-8000-000000ffffff"
const FixtureText = "Commander keeps execution results."

type Client struct {
	t        *testing.T
	Context  context.Context
	url      string
	Username string
	password string
	Client   *http.Client
}

type Response struct {
	Status int
	Header http.Header
	Body   string
}

func Connect(t *testing.T) *Client {
	t.Helper()
	a := &Client{t: t, Context: t.Context(), url: strings.TrimRight(Environment("COMMANDER_TEST_API_URL", "http://127.0.0.1:14274"), "/"),
		Username: Environment("COMMANDER_TEST_API_USERNAME", "api-fixture-user"), password: Environment("COMMANDER_TEST_API_PASSWORD", "api-fixture-password"),
		Client: &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}}}
	t.Cleanup(a.Client.CloseIdleConnections)
	ctx, cancel := context.WithTimeout(a.Context, 90*time.Second)
	defer cancel()
	confirmedTestEndpoint := false
	for {
		result, err := a.Send(ctx, http.MethodPost, "/api/v1/testing/cleanup", nil)
		if err != nil {
			t.Fatalf("connect to test API at %s: %v; start the Compose test stack with ENV=tests", a.url, err)
		}
		if result.Status != http.StatusNoContent && (result.Status != http.StatusConflict || errorCode(result) != "test_state_busy") {
			t.Fatalf("test cleanup requires ENV=tests: status=%d body=%s", result.Status, result.Body)
		}
		if !confirmedTestEndpoint {
			confirmedTestEndpoint = true
			// Establish the test-only endpoint before cancelling any saved work.
			// Teardown also survives a failed test or cancellation of t.Context.
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				if err := a.Settle(ctx); err != nil {
					t.Errorf("settle shared API state after test: %v", err)
				}
			})
		}
		if result.Status == http.StatusNoContent {
			break
		}
		if err := a.Settle(ctx); err != nil {
			t.Fatal(err)
		}
		if err := Pause(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for {
		catalog := Read[models.ListResponse[models.AgentResponse]](a, "/api/v1/agents")
		ready := true
		types := map[string]int{}
		for _, agent := range catalog.Items {
			types[agent.Type]++
			expected := ExpectedAgentActions(agent.Type)
			ready = ready && len(expected) > 0 && agent.Status == "ready" && !agent.Stale && agent.Work == nil && len(agent.Actions) == len(expected)
			for _, action := range agent.Actions {
				ready = ready && (action.Name == "build-agent" || action.Name == "publish-agent" || action.Name == "release-agent" || action.Status == "ready")
			}
		}
		ready = ready && types["example-agent"] == 2 && types["builder-agent"] == 1 && types["spawner-agent"] == 1
		actions := Read[models.ListResponse[models.ActionResponse]](a, "/api/v1/actions")
		missions := Read[models.ListResponse[models.MissionResponse]](a, "/api/v1/missions")
		if ready && slices.Equal(ActionNames(actions.Items), ExpectedCatalogActions(catalog.Items)) && HasExpectedMissions(missions.Items) {
			break
		}
		if err := Pause(ctx); err != nil {
			t.Fatalf("catalog did not recover after cleanup: agents=%+v actions=%d missions=%d: %v", catalog.Items, len(actions.Items), len(missions.Items), err)
		}
	}
	if operations := Read[models.ListResponse[models.OperationResponse]](a, "/api/v1/operations"); len(operations.Items) != 0 {
		t.Fatal("cleanup retained operations from a previous test")
	}
	return a
}

func ExpectedAgentActions(agentType string) []string {
	switch agentType {
	case "example-agent":
		return []string{"analyze-text", "write-summary"}
	case "spawner-agent":
		return []string{"spawn-agent"}
	case "builder-agent":
		return []string{"build-agent", "checkout-template", "publish-agent", "release-agent"}
	case "fixture-agent":
		return []string{"fixture-echo"}
	default:
		return nil
	}
}

func ExpectedMissions() []string {
	return []string{"analyze-text", "build-agent", "fixture-affinity", "fixture-use-agent", "install-agent"}
}

func HasExpectedMissions(missions []models.MissionResponse) bool {
	names := MissionNames(missions)
	for _, name := range ExpectedMissions() {
		if !slices.Contains(names, name) {
			return false
		}
	}
	return true
}

func ExpectedCatalogActions(agents []models.AgentResponse) []string {
	var names []string
	for _, agent := range agents {
		names = append(names, ExpectedAgentActions(agent.Type)...)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func ActionNames(actions []models.ActionResponse) []string {
	var names []string
	for _, action := range actions {
		names = append(names, action.Name)
	}
	return names
}

func MissionNames(missions []models.MissionResponse) []string {
	var names []string
	for _, mission := range missions {
		names = append(names, mission.Name)
	}
	return names
}

func Environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (a *Client) Send(ctx context.Context, method, path string, body any, configure ...func(*http.Request)) (Response, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return Response{}, err
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, a.url+path, bytes.NewReader(data))
	if err != nil {
		return Response{}, err
	}
	request.SetBasicAuth(a.Username, a.password)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, change := range configure {
		change(request)
	}
	result, err := a.Client.Do(request)
	if err != nil {
		return Response{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer result.Body.Close()
	data, err = io.ReadAll(result.Body)
	return Response{Status: result.StatusCode, Header: result.Header, Body: string(data)}, err
}

func (a *Client) Request(method, path string, body any, configure ...func(*http.Request)) Response {
	a.t.Helper()
	result, err := a.Send(a.Context, method, path, body, configure...)
	if err != nil {
		a.t.Fatal(err)
	}
	return result
}

func Read[T any](a *Client, path string) T {
	a.t.Helper()
	return Decode[T](a.t, a.Request(http.MethodGet, path, nil), http.StatusOK)
}

func Post[T any](a *Client, path string, body any, status int) T {
	a.t.Helper()
	return Decode[T](a.t, a.Request(http.MethodPost, path, body), status)
}

func Decode[T any](t *testing.T, result Response, status int) T {
	t.Helper()
	if result.Status != status {
		t.Fatalf("status=%d, want %d: %s", result.Status, status, result.Body)
	}
	var value T
	if err := json.Unmarshal([]byte(result.Body), &value); err != nil {
		t.Fatalf("decode response: %v: %s", err, result.Body)
	}
	if _, ok := any(value).(models.OperationResponse); ok {
		var payload struct {
			Progress map[string]json.RawMessage `json:"progress"`
		}
		if err := json.Unmarshal([]byte(result.Body), &payload); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"task_counts", "total_tasks", "finished_tasks", "pending_decisions", "pending_questions", "settled"} {
			if raw := payload.Progress[field]; len(raw) == 0 || string(raw) == "null" {
				t.Fatalf("operation response omitted progress.%s", field)
			}
		}
	}
	return value
}

func AssertError(t *testing.T, result Response, status int, code string) {
	t.Helper()
	var payload map[string]string
	if result.Status != status || json.Unmarshal([]byte(result.Body), &payload) != nil || len(payload) != 1 || payload["error"] != code {
		t.Fatalf("status=%d body=%s, want %d with error %q", result.Status, result.Body, status, code)
	}
}

func errorCode(result Response) string {
	var payload struct{ Error string }
	_ = json.Unmarshal([]byte(result.Body), &payload)
	return payload.Error
}

func (a *Client) Start(text string) models.OperationResponse {
	a.t.Helper()
	return Post[models.OperationResponse](a, "/api/v1/operations", Object{"mission_name": "analyze-text", "input": Object{"text": text}}, http.StatusCreated)
}

func OperationPath(id string) string { return "/api/v1/operations/" + id }

func (a *Client) Task(id, action string, input Object) models.TaskResponse {
	a.t.Helper()
	return Post[models.TaskResponse](a, OperationPath(id)+"/tasks", Object{"action_name": action, "input": input}, http.StatusCreated)
}

func SummaryInput() Object {
	return Object{"character_count": 12, "word_count": 2, "line_count": 1}
}

func (a *Client) Wait(id string, done func(models.OperationResponse) bool) models.OperationResponse {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(a.Context, 90*time.Second)
	defer cancel()
	for {
		last := Read[models.OperationResponse](a, OperationPath(id))
		if done(last) {
			return last
		}
		if err := Pause(ctx); err != nil {
			data, _ := json.Marshal(last)
			a.t.Fatalf("operation did not reach expected state: %s: %v", data, err)
		}
	}
}

func TasksSettled(op models.OperationResponse) bool {
	for _, task := range op.Tasks {
		if task.Status == "queued" || task.Status == "running" || task.Status == "interrupted" {
			return false
		}
	}
	return true
}

func ModelDecision(op models.OperationResponse) *models.TaskResponse {
	for _, task := range op.Tasks {
		if c := task.Continuation; c != nil && c.Status == "pending" && c.DecisionBy == "model" {
			return &task
		}
	}
	return nil
}

func (a *Client) Resolve(id string, task models.TaskResponse, actions []Object) models.OperationResponse {
	a.t.Helper()
	return Post[models.OperationResponse](a, OperationPath(id)+"/tasks/"+task.ID+"/continuation/resolve", Object{"version": task.Continuation.Version, "actions": actions}, http.StatusOK)
}

func (a *Client) Complete(id, summary string) models.OperationResponse {
	a.t.Helper()
	return Post[models.OperationResponse](a, OperationPath(id)+"/complete", Object{"output": Object{"summary": summary}}, http.StatusOK)
}

func (a *Client) Settle(ctx context.Context) error {
	for {
		result, err := a.Send(ctx, http.MethodGet, "/api/v1/operations", nil)
		if err != nil {
			return err
		}
		var operations models.ListResponse[models.OperationResponse]
		if result.Status != http.StatusOK || json.Unmarshal([]byte(result.Body), &operations) != nil {
			return fmt.Errorf("list operations: status=%d body=%s", result.Status, result.Body)
		}
		busy := false
		for _, op := range operations.Items {
			terminal := op.Status == "completed" || op.Status == "cancelled" || op.Status == "failed"
			if !terminal {
				// Questions are test data: finish their lifecycle before cancellation,
				// which prevents a terminal operation retaining a long pending timer.
				for _, question := range op.Questions {
					if question.Status == "pending" {
						answer, err := a.Send(ctx, http.MethodPost, OperationPath(op.ID)+"/questions/"+question.ID+"/answer", Object{"answer": "End this test review"})
						if err != nil {
							return err
						}
						if answer.Status != http.StatusOK && answer.Status != http.StatusConflict {
							return fmt.Errorf("settle question: status=%d body=%s", answer.Status, answer.Body)
						}
					}
				}
				cancelled, err := a.Send(ctx, http.MethodPost, OperationPath(op.ID)+"/cancel", nil)
				if err != nil {
					return err
				}
				if cancelled.Status != http.StatusOK && !(cancelled.Status == http.StatusConflict && errorCode(cancelled) == "operation_finished") {
					return fmt.Errorf("cancel leftover operation: status=%d body=%s", cancelled.Status, cancelled.Body)
				}
				busy = true
			}
			busy = busy || !TasksSettled(op)
			for _, question := range op.Questions {
				busy = busy || question.Status == "pending"
			}
		}
		inventory, err := a.Send(ctx, http.MethodGet, "/api/v1/agents", nil)
		if err != nil {
			return err
		}
		var agents models.ListResponse[models.AgentResponse]
		if inventory.Status != http.StatusOK || json.Unmarshal([]byte(inventory.Body), &agents) != nil {
			return fmt.Errorf("read agents during settlement: status=%d body=%s", inventory.Status, inventory.Body)
		}
		for _, agent := range agents.Items {
			busy = busy || agent.Work != nil || agent.Status == "working"
		}
		if !busy {
			return nil
		}
		if err := Pause(ctx); err != nil {
			return fmt.Errorf("work still active: operations=%s agents=%s: %w", result.Body, inventory.Body, err)
		}
	}
}

func Pause(ctx context.Context) error {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
