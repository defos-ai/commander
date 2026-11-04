package fakes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	OperationID    = "20000000-0000-4000-8000-000000000001"
	TaskID         = "30000000-0000-4000-8000-000000000001"
	CommandID      = "40000000-0000-4000-8000-000000000001"
	RequestID      = "10000000-0000-4000-8000-000000000001"
	OtherRequestID = "10000000-0000-4000-8000-000000000002"
)

type SecretRequest struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Name        string    `json:"name"`
	AgentID     string    `json:"agent_id"`
	AgentType   string    `json:"agent_type"`
	OperationID string    `json:"operation_id"`
	TaskID      string    `json:"task_id"`
	CommandID   string    `json:"command_id"`
	RequestedAt time.Time `json:"requested_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Status      string    `json:"status"`
}

type SecretSubmission struct{ RequestID, Value string }

type TransientSecrets struct {
	mu               sync.Mutex
	requests         []SecretRequest
	submissions      []SecretSubmission
	managedWrites    int
	managedValue     string
	modelBodies      []string
	mcpArguments     []string
	postStatus       int
	postStarted      chan struct{}
	postCanceled     chan struct{}
	detailReads      int
	discoveryStarted chan struct{}
	discoveryRelease <-chan struct{}
	finishedWait     bool
	receiptPending   bool
	updatedAt        time.Time
}

func NewTransientSecrets(expiry time.Time) *TransientSecrets {
	request := SecretRequest{ID: RequestID, Type: "transient", Name: "CODE", AgentID: "browser-agent-00000001", AgentType: "browser-agent", OperationID: OperationID, TaskID: TaskID, CommandID: CommandID, RequestedAt: time.Now().UTC().Add(-time.Second), ExpiresAt: expiry, Status: "pending"}
	other := request
	other.ID = OtherRequestID
	other.OperationID = "20000000-0000-4000-8000-000000000002"
	other.TaskID = "30000000-0000-4000-8000-000000000002"
	other.CommandID = "40000000-0000-4000-8000-000000000002"
	return &TransientSecrets{requests: []SecretRequest{other, request}, postStatus: http.StatusAccepted}
}

func NewReusableSecrets(expiry time.Time) *TransientSecrets {
	fake := NewTransientSecrets(expiry)
	fake.requests[1].Type, fake.requests[1].Status = "reusable", "needs_value"
	return fake
}

func (f *TransientSecrets) ManagedValue() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.managedValue
}

func (f *TransientSecrets) PostStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.postStatus = status
}

func (f *TransientSecrets) HoldPost() (<-chan struct{}, <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.postStarted, f.postCanceled = make(chan struct{}), make(chan struct{})
	return f.postStarted, f.postCanceled
}

func (f *TransientSecrets) HoldDiscovery(release <-chan struct{}) <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discoveryStarted, f.discoveryRelease = make(chan struct{}), release
	return f.discoveryStarted
}

func (f *TransientSecrets) DetailReads() int { f.mu.Lock(); defer f.mu.Unlock(); return f.detailReads }

func (f *TransientSecrets) Status(status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests[1].Status = status
	if status == "fulfilled" {
		f.finishedWait = true
		f.receiptPending = true
		f.updatedAt = time.Now().UTC()
	}
}

func (f *TransientSecrets) Snapshot() ([]SecretSubmission, int, []string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SecretSubmission(nil), f.submissions...), f.managedWrites, append([]string(nil), f.modelBodies...), append([]string(nil), f.mcpArguments...)
}

func (f *TransientSecrets) API(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	if username, password, ok := r.BasicAuth(); !ok || username != "operator" || password != "fixture-api-password" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/api/v1/secrets") {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/secrets/COMMANDER_LLM_API_KEY" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "50000000-0000-4000-8000-000000000001", "name": "COMMANDER_LLM_API_KEY", "version": 1, "status": "available"})
			return
		}
		if r.Method != http.MethodGet {
			f.managedWrites++
			if f.requests[1].Type == "reusable" && r.Method == http.MethodPost && r.URL.Path == "/api/v1/secrets" {
				var input struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				}
				_ = json.NewDecoder(r.Body).Decode(&input)
				f.managedValue = input.Value
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "50000000-0000-4000-8000-000000000002", "name": input.Name, "version": 1, "status": "available"})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"secret_not_found"}`)
		return
	}
	if r.URL.Path == "/api/v1/secret-requests" {
		if r.URL.Query().Get("type") == "transient" && f.discoveryStarted != nil {
			started, release := f.discoveryStarted, f.discoveryRelease
			f.discoveryStarted = nil
			close(started)
			f.mu.Unlock()
			select {
			case <-release:
			case <-r.Context().Done():
			}
			f.mu.Lock()
		}
		items := make([]SecretRequest, 0)
		for _, request := range f.requests {
			if (request.Status == "pending" || request.Status == "needs_value") && (r.URL.Query().Get("type") == "" || r.URL.Query().Get("type") == request.Type) {
				items = append(items, request)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		return
	}
	for _, request := range f.requests {
		path := "/api/v1/secret-requests/" + request.ID
		if r.URL.Path == path {
			f.detailReads++
			if request.ID == RequestID && f.receiptPending {
				request.Status = "pending"
				f.receiptPending = false
			}
			_ = json.NewEncoder(w).Encode(request)
			return
		}
		if r.URL.Path != path+"/values" || r.Method != http.MethodPost {
			continue
		}
		var input struct {
			Value string `json:"value"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		f.submissions = append(f.submissions, SecretSubmission{RequestID: request.ID, Value: input.Value})
		if f.postStarted != nil {
			started, canceled := f.postStarted, f.postCanceled
			f.postStarted = nil
			close(started)
			f.mu.Unlock()
			<-r.Context().Done()
			close(canceled)
			f.mu.Lock()
			return
		}
		w.WriteHeader(f.postStatus)
		if f.postStatus == http.StatusAccepted {
			_, _ = io.WriteString(w, `{"status":"awaiting_receipt"}`)
		} else {
			_, _ = io.WriteString(w, `{"error":"secret_delivery_uncertain"}`)
		}
		return
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `{"error":"secret_request_not_found"}`)
}

func (f *TransientSecrets) Model(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.modelBodies = append(f.modelBodies, string(body))
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Fixture reply\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
}

func (f *TransientSecrets) MCP() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	for _, name := range []string{"get_operation", "operation_events"} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.mcpArguments = append(f.mcpArguments, string(request.Params.Arguments))
			if request.Params.Name == "operation_events" {
				return &mcp.CallToolResult{StructuredContent: map[string]any{"next_cursor": "fixture"}}, nil
			}
			secret := f.requests[1]
			deadline := secret.ExpiresAt.Add(time.Minute)
			task := map[string]any{"id": TaskID, "command_id": CommandID, "agent_id": secret.AgentID, "agent_type": secret.AgentType, "action_name": "browse", "status": "running", "deadline_at": deadline, "missing_requirements": []any{map[string]any{"kind": "secret", "name": "CODE"}}}
			updatedAt := secret.RequestedAt
			if f.finishedWait {
				delete(task, "agent_type")
				delete(task, "missing_requirements")
				updatedAt = f.updatedAt
			}
			return &mcp.CallToolResult{StructuredContent: map[string]any{
				"id": OperationID, "status": "running", "updated_at": updatedAt,
				"progress": map[string]any{"total_tasks": 1, "task_counts": map[string]int{"running": 1}},
				"tasks":    []any{task},
			}}, nil
		})
	}
	return server
}
