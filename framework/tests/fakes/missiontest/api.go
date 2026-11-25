package missiontest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/id"
)

const (
	Username           = "mission-fixture"
	Password           = "private-fixture-credential"
	BodySecret         = "private-response-body"
	ForeignOperationID = "00000000-0000-4000-8000-000000000001"
)

var evidence = []byte(`{"finding":9007199254740993}`)

type Snapshot struct {
	Starts       int
	Downloads    int
	Resolutions  int
	Unauthorized int
	Canceled     []string
	Drained      []string
	Work         []string
	Operations   map[string]operations.Operation
	Reads        map[string]int
	Assigned     operations.Task
	Resolved     []actions.NextAction
	Answer       map[string]json.RawMessage
	Completion   map[string]json.RawMessage
}

type API struct {
	mu         sync.Mutex
	mode       string
	redirect   string
	operations map[string]operations.Operation
	reads      map[string]int
	work       map[string]int
	snapshot   Snapshot
}

func NewAPI(mode, redirect string) *API {
	return &API{mode: mode, redirect: redirect, operations: map[string]operations.Operation{
		ForeignOperationID: {ID: ForeignOperationID, Status: operations.Running},
	}, reads: make(map[string]int), work: map[string]int{ForeignOperationID: 0}}
}

func Evidence() []byte { return append([]byte(nil), evidence...) }

func Artifact() artifacts.Artifact {
	digest := sha256.Sum256(evidence)
	return artifacts.Artifact{ID: "00000000-0000-4000-8000-000000000002", Name: "evidence",
		AgentID: "fixture-agent-00000001", ActionName: "report", CreatedAt: time.Now().UTC(),
		Files: []artifacts.ArtifactFile{{ID: "00000000-0000-4000-8000-000000000003",
			Name: "report.json", MediaType: "application/json", Size: int64(len(evidence)), SHA256: hex.EncodeToString(digest[:])}}}
}

func (a *API) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	copy := a.snapshot
	copy.Canceled = append([]string(nil), copy.Canceled...)
	copy.Drained = append([]string(nil), copy.Drained...)
	copy.Operations = make(map[string]operations.Operation, len(a.operations))
	copy.Reads = make(map[string]int, len(a.reads))
	for operationID, operation := range a.operations {
		copy.Operations[operationID] = operation.Clone()
	}
	for operationID, count := range a.reads {
		copy.Reads[operationID] = count
	}
	for operationID := range a.work {
		copy.Work = append(copy.Work, operationID)
	}
	copy.Assigned = copy.Assigned.Clone()
	copy.Resolved = actions.CloneNextActions(copy.Resolved)
	copy.Answer = cloneFields(copy.Answer)
	copy.Completion = cloneFields(copy.Completion)
	return copy
}

func cloneFields(fields map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(fields))
	for key, value := range fields {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	username, password, _ := r.BasicAuth()
	if username != Username || password != Password {
		a.snapshot.Unauthorized++
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	switch parts[2] {
	case "missions":
		var mission missions.Mission
		if json.NewDecoder(r.Body).Decode(&mission) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		write(w, http.StatusCreated, mission)
	case "controllers":
		write(w, http.StatusOK, map[string]any{"items": []any{map[string]any{"id": "default-controller-00000001", "status": "ready", "stale": false}}})
	case "agents":
		a.agents(w)
	case "artifacts":
		a.snapshot.Downloads++
		if a.mode == "redirect" {
			http.Redirect(w, r, a.redirect, http.StatusFound)
			return
		}
		if a.mode == "corrupt" {
			_, _ = w.Write([]byte(strings.Repeat("x", len(evidence))))
			return
		}
		_, _ = w.Write(evidence)
	case "operations":
		a.serveOperation(w, r, parts)
	default:
		http.NotFound(w, r)
	}
}

func (a *API) serveOperation(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 3 && r.Method == http.MethodPost {
		a.start(w, r)
		return
	}
	if len(parts) < 4 {
		http.NotFound(w, r)
		return
	}
	op, found := a.operations[parts[3]]
	if !found {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 4 && r.Method == http.MethodGet {
		a.reads[op.ID]++
		if op.Status.Terminal() && len(op.Tasks) == 1 && op.Tasks[0].Status == operations.TaskRunning {
			if a.mode == "unsettled" {
				op.Tasks[0].Status = operations.TaskInterrupted
			} else if a.reads[op.ID] >= 3 {
				op.Tasks[0].Status = operations.TaskSucceeded
				op.Tasks[0].Result = &actions.ActionOutput{ActionName: "inspect", Output: json.RawMessage(`{"observed":true}`)}
			}
		} else if !op.Status.Terminal() && len(op.Tasks) == 1 && a.reads[op.ID] >= 2 && op.Tasks[0].Status == operations.TaskRunning {
			op.Tasks[0].Status = operations.TaskSucceeded
			op.Tasks[0].Result = &actions.ActionOutput{ActionName: "inspect", Output: json.RawMessage(`{"observed":true}`)}
			op.Tasks[0].Continuation = &operations.Continuation{Version: 3, Status: operations.ContinuationWaiting, DecisionBy: operations.DecisionUser}
			op.Questions = []operations.Question{{ID: id.New(), TaskID: op.Tasks[0].ID, Status: operations.QuestionPending, Text: "Publish the report?", FallbackAt: time.Now().Add(time.Minute)}}
		}
		a.operations[op.ID] = op
		write(w, http.StatusOK, op)
		return
	}
	if len(parts) < 5 {
		http.NotFound(w, r)
		return
	}
	switch parts[4] {
	case "tasks":
		if len(parts) == 5 && r.Method == http.MethodPost {
			a.assign(w, r, op)
			return
		}
		if len(parts) == 8 && parts[6] == "continuation" && parts[7] == "resolve" {
			a.resolve(w, r, op, parts[5])
			return
		}
	case "questions":
		if len(parts) == 7 && parts[6] == "answer" {
			if json.NewDecoder(r.Body).Decode(&a.snapshot.Answer) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var answer string
			_ = json.Unmarshal(a.snapshot.Answer["answer"], &answer)
			op.Questions[0].Status, op.Questions[0].Answer, op.Questions[0].AnsweredBy = operations.QuestionAnswered, answer, operations.AnswerOperator
			op.Tasks[0].Continuation.Status, op.Tasks[0].Continuation.DecisionBy = operations.ContinuationPending, operations.DecisionModel
			op.Tasks[0].Continuation.Version++
			a.operations[op.ID] = op
			write(w, http.StatusOK, op)
			return
		}
	case "complete":
		if json.NewDecoder(r.Body).Decode(&a.snapshot.Completion) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		op.Status, op.Output, op.ReportID, op.CompletedBy = operations.Completed, a.snapshot.Completion["output"], id.New(), operations.CompletionOperator
		a.operations[op.ID] = op
		write(w, http.StatusOK, op)
		return
	case "cancel":
		a.snapshot.Canceled = append(a.snapshot.Canceled, op.ID)
		op.Status = operations.Cancelled
		a.operations[op.ID] = op
		write(w, http.StatusOK, op)
		return
	case "report":
		write(w, http.StatusOK, reports.Report{ID: op.ReportID, OperationID: op.ID, Mission: op.Mission.Name, Output: op.Output, CreatedAt: time.Now().UTC()})
		return
	}
	http.NotFound(w, r)
}

func (a *API) start(w http.ResponseWriter, r *http.Request) {
	a.snapshot.Starts++
	var request struct {
		OperationID string          `json:"operation_id"`
		MissionName string          `json:"mission_name"`
		Input       json.RawMessage `json:"input"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	op := operations.Operation{ID: request.OperationID, Mission: missions.Mission{Name: request.MissionName}, Input: request.Input, Status: operations.Planning}
	a.operations[op.ID] = op
	if a.mode == "uncertain" {
		write(w, http.StatusServiceUnavailable, map[string]string{"error": "operation_write_uncertain", "details": BodySecret})
		return
	}
	write(w, http.StatusCreated, op)
}

func (a *API) assign(w http.ResponseWriter, r *http.Request, op operations.Operation) {
	var request struct {
		ActionName string                   `json:"action_name"`
		Input      json.RawMessage          `json:"input"`
		Selector   *operations.TaskSelector `json:"selector"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	task := operations.Task{ID: id.New(), OperationID: op.ID, ActionName: request.ActionName, Input: request.Input, Selector: request.Selector, Status: operations.TaskRunning}
	op.Status, op.Tasks = operations.Running, append(op.Tasks, task)
	a.operations[op.ID], a.snapshot.Assigned = op, task.Clone()
	a.work[op.ID] = 0
	write(w, http.StatusCreated, task)
}

func (a *API) resolve(w http.ResponseWriter, r *http.Request, op operations.Operation, taskID string) {
	a.snapshot.Resolutions++
	var request struct {
		Version uint64               `json:"version"`
		Actions []actions.NextAction `json:"actions"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || request.Actions == nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	for i := range op.Tasks {
		if op.Tasks[i].ID != taskID {
			continue
		}
		if op.Tasks[i].Continuation == nil || op.Tasks[i].Continuation.Version != request.Version {
			write(w, http.StatusConflict, map[string]string{"error": "continuation_conflict", "details": BodySecret})
			return
		}
		op.Tasks[i].Continuation.Status = operations.ContinuationResolved
		if len(request.Actions) != 0 {
			a.snapshot.Resolved = actions.CloneNextActions(request.Actions)
			for _, next := range request.Actions {
				child := operations.Task{ID: id.New(), OperationID: op.ID, ParentTaskID: taskID, ActionName: next.ActionName, Input: next.Input, Status: operations.TaskSucceeded,
					Result:       &actions.ActionOutput{ActionName: next.ActionName, Output: Evidence(), Artifacts: []artifacts.Artifact{Artifact()}},
					Continuation: &operations.Continuation{Version: 1, Status: operations.ContinuationPending, DecisionBy: operations.DecisionModel}}
				op.Tasks = append(op.Tasks, child)
			}
		}
		a.operations[op.ID] = op
		write(w, http.StatusOK, op)
		return
	}
	http.NotFound(w, r)
}

func (a *API) agents(w http.ResponseWriter) {
	items := []any{map[string]any{"id": "fixture-agent-00000001", "status": "ready", "stale": false,
		"actions": []any{map[string]any{"name": "inspect", "status": "ready"}, map[string]any{"name": "report", "status": "ready"}}}}
	for operationID, observations := range a.work {
		operation := a.operations[operationID]
		if operation.AssessProgress(time.Now()).Settled {
			observations++
			if observations >= 2 {
				delete(a.work, operationID)
				a.snapshot.Drained = append(a.snapshot.Drained, operationID)
				continue
			}
			a.work[operationID] = observations
		}
		taskID := operationID
		if len(operation.Tasks) > 0 {
			taskID = operation.Tasks[0].ID
		}
		items = append(items, map[string]any{"id": "fixture-agent-" + operationID[:8], "status": "working", "stale": false,
			"work": map[string]any{"operation_id": operationID, "task_id": taskID, "action_name": "inspect"}})
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}

func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
