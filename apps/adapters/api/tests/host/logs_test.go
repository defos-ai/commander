package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
)

func TestLogQueriesReadSavedOperationAndTaskPagesWithoutController(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	op, other := hostfixtures.LogOperation(), hostfixtures.LogOperation()
	if err := config.OperationStore.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	if err := config.OperationStore.Create(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	logs := hostfixtures.OperationLogs(op)
	foreign := hostfixtures.OperationLogs(other)[0]
	for _, log := range append([]executions.Log{foreign}, logs...) {
		if err := config.ExecutionStore.AppendLog(t.Context(), log); err != nil {
			t.Fatal(err)
		}
	}
	handler := hostfixtures.LogHandler(t, config)
	root := "/api/v1/operations/" + op.ID
	descending := slices.Clone(logs)
	slices.Reverse(descending)
	for _, test := range []struct {
		name, taskID, order string
		want                []executions.Log
	}{
		{"operation", "", "", logs},
		{"task", op.Tasks[0].ID, "", []executions.Log{logs[0], logs[2], logs[3]}},
		{"operation descending", "", "desc", descending},
		{"task descending", op.Tasks[0].ID, "desc", []executions.Log{logs[3], logs[2], logs[0]}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var saved []models.LogResponse
			cursor := ""
			for {
				parameters := url.Values{"limit": {"2"}, "cursor": {cursor}}
				if test.taskID != "" {
					parameters.Set("task_id", test.taskID)
				}
				if test.order != "" {
					parameters.Set("order", test.order)
				}

				result := hostfixtures.LogRequest(t.Context(), handler, root+"/logs?"+parameters.Encode(), true)

				page := decodeResponse[models.PageResponse[models.LogResponse]](t, result)
				if page.Items == nil || len(page.Items) > 2 {
					t.Fatalf("invalid log page: %s", result.Body)
				}
				if len(page.Items) == 0 {
					if page.NextCursor != cursor {
						t.Fatal("empty page changed the saved cursor")
					}
					break
				}
				if page.NextCursor != page.Items[len(page.Items)-1].ID {
					t.Fatal("log cursor is not the last returned ID")
				}
				saved = append(saved, page.Items...)
				cursor = page.NextCursor
				if len(saved) > len(test.want) {
					t.Fatal("pagination repeated or leaked another scope's logs")
				}
			}
			assertLogResponses(t, saved, test.want)
		})
	}
	filtered := hostfixtures.LogRequest(t.Context(), handler, root+"/logs?agent_id="+logs[0].AgentID+"&action_name=inspect&limit=1000", true)
	page := decodeResponse[models.PageResponse[models.LogResponse]](t, filtered)
	assertLogResponses(t, page.Items, []executions.Log{logs[0], logs[3]})
	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(filtered.Body), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Items[0]) != 9 || len(raw.Items[1]) != 8 || raw.Items[0]["work"] != nil || raw.Items[1]["step_id"] != nil {
		t.Fatal("execution logs must use flat DTO fields and omit an empty step")
	}
	if calls := config.Operations.(*fakes.FailedStart).Snapshot(); calls.Attempts != 0 {
		t.Fatal("saved log query contacted the controller")
	}
}

func TestLogQueriesResumeAfterAnEmptyFilteredTail(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	op := hostfixtures.LogOperation()
	if err := config.OperationStore.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	logs := hostfixtures.OperationLogs(op)
	if err := config.ExecutionStore.AppendLog(t.Context(), logs[0]); err != nil {
		t.Fatal(err)
	}
	handler := hostfixtures.LogHandler(t, config)
	path := "/api/v1/operations/" + op.ID + "/logs?task_id=" + op.Tasks[0].ID + "&agent_id=" + logs[0].AgentID + "&action_name=inspect&cursor=" + logs[0].ID

	tail := decodeResponse[models.PageResponse[models.LogResponse]](t, hostfixtures.LogRequest(t.Context(), handler, path, true))

	if tail.Items == nil || len(tail.Items) != 0 || tail.NextCursor != logs[0].ID {
		t.Fatalf("empty filtered tail lost the ID cursor: %+v", tail)
	}
	for _, log := range logs[1:] {
		if err := config.ExecutionStore.AppendLog(t.Context(), log); err != nil {
			t.Fatal(err)
		}
	}

	resumed := decodeResponse[models.PageResponse[models.LogResponse]](t, hostfixtures.LogRequest(t.Context(), handler, path, true))

	assertLogResponses(t, resumed.Items, []executions.Log{logs[3]})
	if resumed.NextCursor != logs[3].ID {
		t.Fatal("late log did not advance the ID cursor")
	}
}

func TestLogQueriesDescendingPagesPreserveScopeAndFollowNewerAppends(t *testing.T) {
	for _, test := range []struct {
		name, agent, action string
		task, limit         int
		want                []int
	}{
		{name: "operation", task: -1, limit: 2, want: []int{3, 2}},
		{name: "task", task: 1, limit: 1, want: []int{1}},
		{name: "operation filters", task: -1, agent: "worker-agent-00000001", action: "inspect", limit: 1, want: []int{3}},
		{name: "task filters", task: 0, agent: "worker-agent-00000001", action: "Inspect", limit: 1, want: []int{2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := hostfixtures.LogAPIConfig(t)
			op, other := hostfixtures.LogOperation(), hostfixtures.LogOperation()
			if err := config.OperationStore.Create(t.Context(), op); err != nil {
				t.Fatal(err)
			}
			logs := hostfixtures.OperationLogs(op)
			for _, log := range append(logs, hostfixtures.OperationLogs(other)[0]) {
				if err := config.ExecutionStore.AppendLog(t.Context(), log); err != nil {
					t.Fatal(err)
				}
			}
			handler := hostfixtures.LogHandler(t, config)
			path := "/api/v1/operations/" + op.ID + "/logs"
			parameters := url.Values{"limit": {strconv.Itoa(test.limit)}, "order": {"desc"}}
			if test.task >= 0 {
				parameters.Set("task_id", op.Tasks[test.task].ID)
			}
			if test.agent != "" {
				parameters.Set("agent_id", test.agent)
			}
			if test.action != "" {
				parameters.Set("action_name", test.action)
			}
			want := make([]executions.Log, len(test.want))
			for index, source := range test.want {
				want[index] = logs[source]
			}

			newest := decodeResponse[models.PageResponse[models.LogResponse]](t,
				hostfixtures.LogRequest(t.Context(), handler, path+"?"+parameters.Encode(), true))

			assertLogResponses(t, newest.Items, want)
			if newest.Items == nil || newest.NextCursor != want[len(want)-1].ID {
				t.Fatalf("descending page lost its oldest matching cursor: %+v", newest)
			}
			late := want[0]
			late.ID, late.CommandID = id.New(), id.New()
			late.Message = "Later progress with an earlier source timestamp"
			late.CreatedAt = late.CreatedAt.Add(-24 * time.Hour)
			if err := config.ExecutionStore.AppendLog(t.Context(), late); err != nil {
				t.Fatal(err)
			}
			parameters.Set("cursor", newest.Items[0].ID)
			parameters.Set("order", "asc")

			followed := decodeResponse[models.PageResponse[models.LogResponse]](t,
				hostfixtures.LogRequest(t.Context(), handler, path+"?"+parameters.Encode(), true))

			assertLogResponses(t, followed.Items, []executions.Log{late})
			if followed.NextCursor != late.ID {
				t.Fatal("ascending polling from the newest descending entry did not advance its cursor")
			}
		})
	}
}

func TestLogQueriesEmptyDescendingPageCanObserveTheFirstMatchingLog(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	op := hostfixtures.LogOperation()
	if err := config.OperationStore.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	logs := hostfixtures.OperationLogs(op)
	if err := config.ExecutionStore.AppendLog(t.Context(), logs[0]); err != nil {
		t.Fatal(err)
	}
	handler := hostfixtures.LogHandler(t, config)
	path := "/api/v1/operations/" + op.ID + "/logs"
	filter := "?agent_id=worker-agent-deadbeef"

	newest := decodeResponse[models.PageResponse[models.LogResponse]](t,
		hostfixtures.LogRequest(t.Context(), handler, path+filter+"&order=desc", true))

	if newest.Items == nil || len(newest.Items) != 0 || newest.NextCursor != "" {
		t.Fatalf("empty descending page changed its empty cursor: %+v", newest)
	}
	late := logs[1]
	late.AgentID = "worker-agent-deadbeef"
	if err := config.ExecutionStore.AppendLog(t.Context(), late); err != nil {
		t.Fatal(err)
	}

	followed := decodeResponse[models.PageResponse[models.LogResponse]](t,
		hostfixtures.LogRequest(t.Context(), handler, path+filter+"&order=asc&cursor="+newest.NextCursor, true))

	assertLogResponses(t, followed.Items, []executions.Log{late})
	if followed.NextCursor != late.ID {
		t.Fatal("following an empty descending page lost the first matching log")
	}
}

func TestLogQueriesRejectInvalidInputBeforeReadingLogs(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	op := hostfixtures.LogOperation()
	parents := &fakes.LogOperationReads{}
	config.OperationStore = parents
	store := fakes.NewLogQueries(nil)
	config.ExecutionStore = store
	handler := hostfixtures.LogHandler(t, config)
	path := "/api/v1/operations/" + op.ID + "/logs"
	for _, test := range []struct{ query, code string }{
		{"unexpected=value", "invalid_query"}, {"orderBy=created_at", "invalid_query"},
		{"cursor=a&cursor=b", "invalid_query"}, {"cursor=invalid", "invalid_cursor"},
		{"limit=1&limit=2", "invalid_query"}, {"task_id=a&task_id=b", "invalid_query"},
		{"agent_id=x&agent_id=y", "invalid_query"}, {"action_name=a&action_name=b", "invalid_query"},
		{"order=asc&order=desc", "invalid_query"}, {"order=", "invalid_query"},
		{"order=ASC", "invalid_query"}, {"order=Desc", "invalid_query"}, {"order=ascending", "invalid_query"},
		{"order=%20asc", "invalid_query"}, {"order=null", "invalid_query"},
		{"limit=", "invalid_limit"}, {"limit=-1", "invalid_limit"},
		{"limit=0", "invalid_limit"}, {"limit=1001", "invalid_limit"}, {"limit=one", "invalid_limit"},
		{"task_id=", "invalid_query"}, {"task_id=invalid", "invalid_query"},
		{"task_id=AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", "invalid_query"},
		{"agent_id=", "invalid_query"}, {"agent_id=invalid", "invalid_query"},
		{"action_name=", "invalid_query"}, {"action_name=%20%09", "invalid_query"},
		{"action_name=%FF", "invalid_query"}, {"action_name=%GG", "invalid_query"},
		{"latest=true", "invalid_query"}, {"latest=false", "invalid_query"},
	} {
		t.Run(test.query, func(t *testing.T) {
			result := hostfixtures.LogRequest(t.Context(), handler, path+"?"+test.query, true)

			assertAPIError(t, result, http.StatusBadRequest, test.code)
		})
	}
	if calls, _ := store.Snapshot(); calls.Attempts != 0 {
		t.Fatal("invalid log query reached the execution store")
	}
	assertAPIError(t, hostfixtures.LogRequest(t.Context(), handler, "/api/v1/operations/invalid/logs", true), http.StatusBadRequest, "invalid_operation_id")
	if calls := parents.Snapshot(); calls.Attempts != 0 {
		t.Fatal("invalid log query reached parent operation storage")
	}
}

func TestLogQueriesPreserveMissingParentAndScopedCursorErrors(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	op, other := hostfixtures.LogOperation(), hostfixtures.LogOperation()
	if err := config.OperationStore.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	if err := config.OperationStore.Create(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	logs := hostfixtures.OperationLogs(op)
	foreign := hostfixtures.OperationLogs(other)[0]
	for _, log := range append(logs, foreign) {
		if err := config.ExecutionStore.AppendLog(t.Context(), log); err != nil {
			t.Fatal(err)
		}
	}
	handler := hostfixtures.LogHandler(t, config)
	root := "/api/v1/operations/" + op.ID
	for _, test := range []struct{ path, code string }{
		{"/api/v1/operations/" + id.New() + "/logs", "operation_not_found"},
		{"/api/v1/operations/" + id.New() + "/logs?task_id=" + op.Tasks[0].ID, "operation_not_found"},
		{root + "/logs?task_id=" + id.New(), "task_not_found"},
		{root + "/logs?task_id=" + other.Tasks[0].ID, "task_not_found"},
		{root + "/logs?cursor=" + id.New(), "log_not_found"},
		{root + "/logs?cursor=" + foreign.ID + "&order=desc", "log_not_found"},
		{root + "/logs?task_id=" + op.Tasks[0].ID + "&cursor=" + logs[1].ID, "log_not_found"},
		{root + "/logs?cursor=" + logs[0].ID + "&action_name=Inspect", "log_not_found"},
	} {
		result := hostfixtures.LogRequest(t.Context(), handler, test.path, true)

		assertAPIError(t, result, http.StatusNotFound, test.code)
	}
}

func TestLogQueriesBoundAndSanitizeBackendFailures(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{context.DeadlineExceeded, http.StatusGatewayTimeout, "request_timeout"},
		{errors.New("private backend path"), http.StatusInternalServerError, "internal_error"},
	} {
		t.Run(test.code, func(t *testing.T) {
			config := hostfixtures.LogAPIConfig(t)
			op := hostfixtures.LogOperation()
			if err := config.OperationStore.Create(t.Context(), op); err != nil {
				t.Fatal(err)
			}
			store := fakes.NewLogQueries(test.err)
			config.ExecutionStore = store
			handler := hostfixtures.LogHandler(t, config)
			path := "/api/v1/operations/" + op.ID + "/logs?action_name=" + url.QueryEscape(" inspect ")

			result := hostfixtures.LogRequest(t.Context(), handler, path, true)

			assertAPIError(t, result, test.status, test.code)
			calls, query := store.Snapshot()
			if calls.Attempts != 1 || calls.Remaining <= 0 || calls.Remaining > 10*time.Second || query.Limit != 100 ||
				query.Order != executions.LogOrderAscending || query.ActionName != " inspect " {
				t.Fatalf("log query lost its bounded context or exact filter/default: calls=%+v query=%+v", calls, query)
			}
		})
	}
}

func TestLogQueriesRequireAuthenticationAndAnExecutionStore(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	handler := hostfixtures.LogHandler(t, config)
	path := "/api/v1/operations/" + id.New() + "/logs?order=desc&task_id=" + id.New()

	result := hostfixtures.LogRequest(t.Context(), handler, path, false)

	assertAPIError(t, result, http.StatusUnauthorized, "authentication_required")
	config.ExecutionStore = nil
	if _, err := api.NewHandler(config); err == nil {
		t.Fatal("API accepted a missing execution store")
	}
}

func TestLogQueriesRemovedRoutesHaveNoAliases(t *testing.T) {
	config := hostfixtures.LogAPIConfig(t)
	op := hostfixtures.LogOperation()
	if err := config.OperationStore.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	if err := config.ExecutionStore.AppendLog(t.Context(), hostfixtures.OperationLogs(op)[0]); err != nil {
		t.Fatal(err)
	}
	handler := hostfixtures.LogHandler(t, config)
	root := "/api/v1/operations/" + op.ID
	for _, path := range []string{root + "/logs/latest", root + "/tasks/" + op.Tasks[0].ID + "/logs", root + "/tasks/" + op.Tasks[0].ID + "/logs/latest"} {
		result := hostfixtures.LogRequest(t.Context(), handler, path, true)

		if result.Status != http.StatusNotFound {
			t.Fatalf("removed route %s remains available: %d", path, result.Status)
		}
	}
}

func TestLogQueriesOpenAPIContract(t *testing.T) {
	handler := hostfixtures.LogHandler(t, hostfixtures.LogAPIConfig(t))
	var document struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Parameters  []struct {
				Name     string `json:"name"`
				In       string `json:"in"`
				Required bool   `json:"required"`
				Schema   struct {
					Maximum json.RawMessage `json:"maximum"`
					Default json.RawMessage `json:"default"`
					Pattern string          `json:"pattern"`
					Enum    []string        `json:"enum"`
				} `json:"schema"`
			} `json:"parameters"`
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"schemas"`
		} `json:"components"`
	}

	result := hostfixtures.LogRequest(t.Context(), handler, "/openapi.json", true)

	if result.Status != http.StatusOK {
		t.Fatalf("OpenAPI log contract unavailable: status=%d", result.Status)
	}
	if err := json.Unmarshal([]byte(result.Body), &document); err != nil {
		t.Fatal(err)
	}
	get, ok := document.Paths["/api/v1/operations/{operation_id}/logs"]["get"]
	if !ok || get.OperationID == "" {
		t.Fatal("missing documented operation log route")
	}
	parameters := []string{}
	for _, parameter := range get.Parameters {
		parameters = append(parameters, parameter.Name)
		if parameter.Name == "limit" && (string(parameter.Schema.Default) != "100" || string(parameter.Schema.Maximum) != strconv.Itoa(domain.MaxPageSize)) {
			t.Fatal("documented log page bounds differ from the handler")
		}
		if parameter.Name == "action_name" && parameter.Schema.Pattern != `\S` {
			t.Fatal("documented action filter accepts blank names")
		}
		if parameter.Name == "task_id" && (parameter.In != "query" || parameter.Required || parameter.Schema.Pattern == "") {
			t.Fatal("task filter must be an optional canonical UUID query parameter")
		}
		if parameter.Name == "order" && (!slices.Equal(parameter.Schema.Enum, []string{"asc", "desc"}) || string(parameter.Schema.Default) != `"asc"`) {
			t.Fatal("documented log order differs from the handler")
		}
	}
	slices.Sort(parameters)
	if !slices.Equal(parameters, []string{"action_name", "agent_id", "cursor", "limit", "operation_id", "order", "task_id"}) ||
		get.Responses["400"] == nil || get.Responses["404"] == nil {
		t.Fatalf("log parameters/errors differ from the route: %v", parameters)
	}
	for _, path := range []string{
		"/api/v1/operations/{operation_id}/logs/latest", "/api/v1/operations/{operation_id}/tasks/{task_id}/logs",
		"/api/v1/operations/{operation_id}/tasks/{task_id}/logs/latest",
	} {
		if document.Paths[path] != nil {
			t.Fatalf("removed log route remains documented: %s", path)
		}
	}
	schema := document.Components.Schemas["Log"]
	fields := []string{}
	for name := range schema.Properties {
		fields = append(fields, name)
	}
	slices.Sort(fields)
	if !slices.Equal(fields, []string{"action_name", "agent_id", "command_id", "created_at", "id", "message", "operation_id", "step_id", "task_id"}) || slices.Contains(schema.Required, "step_id") || len(schema.Required) != 8 {
		t.Fatalf("log DTO schema does not match its flat wire fields: %v required=%v", fields, schema.Required)
	}
}

func assertLogResponses(t *testing.T, got []models.LogResponse, want []executions.Log) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d logs, want %d", len(got), len(want))
	}
	for index, log := range want {
		item := got[index]
		if item.ID != log.ID || item.AgentID != log.AgentID || item.CommandID != log.CommandID || item.ActionName != log.ActionName ||
			item.OperationID != log.Work.OperationID || item.TaskID != log.Work.TaskID || item.StepID != log.Work.StepID ||
			item.Message != log.Message || !item.CreatedAt.Equal(log.CreatedAt) {
			t.Fatalf("log %d lost provenance, exact message or timestamp: %+v", index, item)
		}
	}
}
