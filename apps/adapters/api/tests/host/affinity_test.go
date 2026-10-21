package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestOperationQueriesExposeSavedAffinityWithoutAssigningQueuedTasks(t *testing.T) {
	fixture := newFixture(t)
	store, err := operationfs.NewOperationFsStore(filepath.Join(fixture.root, "database"))
	if err != nil {
		t.Fatal(err)
	}
	server := startAPI(t, append(fixture.localArgs(), "--swagger=true"))
	schema := affinityOperationSchema(t, server)
	const owner = "builder-agent-12345678"
	group := []string{"checkout-template", "build-agent"}
	for _, scenario := range []string{"no rule", "unbound", "bound owner absent", "terminal", "conflicting history"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			op := operations.Operation{ID: id.OperationID(), Status: operations.Planning, Input: json.RawMessage(`{}`),
				Mission: missions.Mission{Name: "build", Objective: "Build an agent",
					InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
					Rules: missions.RuleSet{missions.SameAgent: {ForActions: slices.Clone(group)}},
				}, CreatedAt: now, UpdatedAt: now}
			for _, name := range group {
				op.Tasks = append(op.Tasks, operations.NewTask(op.ID, "", "", actions.NextAction{ActionName: name, Input: json.RawMessage(`{}`)}))
			}
			if scenario == "no rule" {
				op.Mission.Rules = nil
			}
			bound := scenario != "no rule" && scenario != "unbound"
			if bound {
				if err := op.DispatchTask(operations.TaskDispatch{
					TaskID: op.Tasks[0].ID, AgentID: owner, StartedAt: now, DeadlineAt: now.Add(time.Minute),
				}); err != nil {
					t.Fatal(err)
				}
				// Retained dispatch history binds even an interrupted execution
				// whose agent is no longer in the inventory.
				op.Tasks[0].Status = operations.TaskInterrupted
				op.Tasks[0].FinishedAt = &now
				op.Preflight, err = op.AssessPreflight(nil)
				if err != nil {
					t.Fatal(err)
				}
				op.RefreshStatus()
			}
			if scenario == "terminal" {
				if err := op.Cancel(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "conflicting history" {
				op.Tasks[1].Status = operations.TaskInterrupted
				op.Tasks[1].AgentID = "builder-agent-87654321"
				op.Tasks[1].DeadlineAt = op.Tasks[0].DeadlineAt
			}
			if err := store.Create(t.Context(), op); err != nil {
				t.Fatal(err)
			}
			before, err := store.Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			result := server.request(t, http.MethodGet, "/api/v1/operations/"+op.ID, authenticate)
			detail := decodeResponse[models.OperationResponse](t, result)
			var body any
			if err := json.Unmarshal([]byte(result.body), &body); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(body); err != nil {
				t.Fatalf("operation does not match its published schema: %v", err)
			}
			if detail.Progress.TotalTasks != 2 || detail.Progress.Settled || detail.Progress.TaskCounts == nil {
				t.Fatalf("operation lost unsettled progress: %+v", detail.Progress)
			}
			if scenario == "terminal" && (detail.Progress.FinishedTasks != 1 || detail.Progress.TaskCounts["interrupted"] != 1 || detail.Progress.TaskCounts["cancelled"] != 1) {
				t.Fatalf("cancelled operation counted interrupted work as finished: %+v", detail.Progress)
			}
			object := body.(map[string]any)
			delete(object, "progress")
			if err := schema.Validate(object); err == nil {
				t.Fatal("operation schema permits a missing progress object")
			}
			affinity := detail.Affinity
			switch {
			case scenario == "no rule":
				if affinity != nil {
					t.Fatal("operation without same-agent acquired affinity")
				}
			case scenario == "conflicting history":
				if affinity == nil || affinity.Error == "" || affinity.AgentID != "" {
					t.Fatalf("inconsistent history appears unbound or valid: %+v", affinity)
				}
			default:
				if affinity == nil || !slices.Equal(affinity.ForActions, group) || affinity.Error != "" ||
					bound && affinity.AgentID != owner || !bound && affinity.AgentID != "" {
					t.Fatalf("saved affinity was lost: %+v", affinity)
				}
				if detail.Tasks[1].AgentID != "" {
					t.Fatal("query assigned the pending task to the affinity owner")
				}
			}
			if scenario == "bound owner absent" && (detail.Preflight == nil || detail.Preflight.Ready ||
				len(detail.Preflight.Actions[1].UnavailableAgents) != 1 || detail.Preflight.Actions[1].UnavailableAgents[0].AgentID != owner) {
				t.Fatal("saved readiness lost the absent owner blocker")
			}
			list := decodeResponse[models.ListResponse[models.OperationResponse]](t, server.request(t, http.MethodGet, "/api/v1/operations", authenticate))
			index := slices.IndexFunc(list.Items, func(item models.OperationResponse) bool { return item.ID == op.ID })
			if index < 0 || !reflect.DeepEqual(list.Items[index].Affinity, affinity) || !reflect.DeepEqual(list.Items[index].Progress, detail.Progress) {
				t.Fatal("list and detail disagree about saved affinity or progress")
			}
			after, err := store.Get(t.Context(), op.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("queries changed saved operation state")
			}
			if scenario == "terminal" {
				if _, err := store.FinishTask(t.Context(), op.ID, operations.TaskResult{
					TaskID: op.Tasks[0].ID, AgentID: owner, Status: operations.TaskFailed,
					Error: "late failure", FinishedAt: time.Now().UTC(),
				}); err != nil {
					t.Fatal(err)
				}
				late := decodeResponse[models.OperationResponse](t, server.request(t, http.MethodGet, "/api/v1/operations/"+op.ID, authenticate))
				if !late.Progress.Settled || late.Progress.FinishedTasks != 2 || late.Progress.PendingDecisions != 0 || late.Status != "cancelled" {
					t.Fatalf("late known outcome did not settle cancelled work: %+v", late.Progress)
				}
			}
		})
	}
}

func affinityOperationSchema(t *testing.T, server *runningAPI) *jsonschema.Resolved {
	t.Helper()
	document := decodeResponse[struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}](t, server.request(t, http.MethodGet, "/openapi.json", authenticate))
	data, err := json.Marshal(map[string]any{"$defs": document.Components.Schemas, "$ref": "#/$defs/Operation"})
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(bytes.ReplaceAll(data, []byte("#/components/schemas/"), []byte("#/$defs/")), &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
