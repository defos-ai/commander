package api_test

import (
	"bytes"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"commander/framework/domain/operations"
)

func TestOperationTasksUseExclusiveAssignmentCursor(t *testing.T) {
	fixture := newFixture(t)
	op := operationRecord(1)
	// UUID and start-time ordering both differ from assignment order.
	op.Tasks = []operations.Task{taskRecord(op.ID, 30), taskRecord(op.ID, 10), taskRecord(op.ID, 20)}
	op.Tasks[0].StartedAt = op.Tasks[0].StartedAt.Add(time.Hour)
	op.Tasks[1].Status, op.Tasks[1].Error = operations.TaskFailed, "Link check failed"
	op = saveOperation(t, operationStore(t, fixture), op)
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID + "/tasks"
	before := operationBytes(t, fixture, op.ID)

	first := decodeResponse[taskPage](t, server.request(t, http.MethodGet, base+"?limit=2", authenticate))
	second := decodeResponse[taskPage](t, server.request(t, http.MethodGet, base+"?limit=2&cursor="+first.NextCursor, authenticate))
	last := decodeResponse[taskPage](t, server.request(t, http.MethodGet, base+"?cursor="+second.NextCursor, authenticate))
	detail := decodeResponse[operations.Task](t, server.request(t, http.MethodGet, base+"/"+op.Tasks[1].ID, authenticate))
	explicitEmpty := decodeResponse[taskPage](t, server.request(t, http.MethodGet, base+"?cursor=&limit=1", authenticate))

	if !reflect.DeepEqual(first.Items, op.Tasks[:2]) || first.NextCursor != op.Tasks[1].ID {
		t.Fatalf("first page lost assignment order or task fields: %+v", first)
	}
	if !reflect.DeepEqual(second.Items, op.Tasks[2:]) || second.NextCursor != op.Tasks[2].ID {
		t.Fatalf("second page did not start after the cursor: %+v", second)
	}
	if last.Items == nil || len(last.Items) != 0 || last.NextCursor != second.NextCursor {
		t.Fatalf("empty task page must retain its cursor: %+v", last)
	}
	if !reflect.DeepEqual(detail, op.Tasks[1]) {
		t.Fatalf("task detail changed the saved record: got %+v, want %+v", detail, op.Tasks[1])
	}
	if !reflect.DeepEqual(explicitEmpty.Items, op.Tasks[:1]) || explicitEmpty.NextCursor != op.Tasks[0].ID {
		t.Fatalf("explicit empty cursor did not start at the first assignment: %+v", explicitEmpty)
	}
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("task queries changed the operation or its revision")
	}
}

func TestOperationPageLimits(t *testing.T) {
	fixture := newFixture(t)
	op := operationRecord(1)
	for i := 1; i <= 1001; i++ {
		op.Tasks = append(op.Tasks, operations.Task{ID: recordID(i), OperationID: op.ID, Status: operations.TaskQueued, ActionName: "review", CommandID: recordID(i)})
		op.Events = append(op.Events, operations.Event{Sequence: uint64(i), OperationID: op.ID, Type: operations.TaskAssigned, TaskID: recordID(i), Status: operations.Running, CreatedAt: op.CreatedAt})
	}
	op = saveOperation(t, operationStore(t, fixture), op)
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID

	for _, suffix := range []string{"/tasks", "/events"} {
		for _, tc := range []struct {
			query string
			count int
		}{{"", 100}, {"?limit=1000", 1000}} {
			t.Run(suffix+tc.query, func(t *testing.T) {
				page := decodeResponse[struct {
					Items      []any  `json:"items"`
					NextCursor string `json:"next_cursor"`
				}](t, server.request(t, http.MethodGet, base+suffix+tc.query, authenticate))

				if len(page.Items) != tc.count {
					t.Fatalf("page has %d records, want %d", len(page.Items), tc.count)
				}
				wantCursor := strconv.Itoa(tc.count)
				if suffix == "/tasks" {
					wantCursor = op.Tasks[tc.count-1].ID
				}
				if page.NextCursor != wantCursor {
					t.Fatalf("page cursor=%q, want %q", page.NextCursor, wantCursor)
				}
			})
		}
	}
}

func TestOperationPaginationRejectsInvalidQueries(t *testing.T) {
	fixture := newFixture(t)
	op := saveOperation(t, operationStore(t, fixture), operationRecord(1))
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID
	before := operationBytes(t, fixture, op.ID)

	for _, suffix := range []string{"/tasks", "/events"} {
		for _, tc := range []struct {
			query string
			code  string
		}{
			{"limit=0", "invalid_limit"}, {"limit=-1", "invalid_limit"}, {"limit=1001", "invalid_limit"},
			{"limit=1.5", "invalid_limit"}, {"limit=ten", "invalid_limit"}, {"limit=", "invalid_limit"}, {"limit=999999999999999999999999", "invalid_limit"},
			{"unknown=1", "invalid_query"}, {"cursor=&cursor=", "invalid_query"}, {"limit=1&limit=2", "invalid_query"},
			{"limit=%zz", "invalid_query"}, {"cursor=%zz", "invalid_query"}, {"limit=1;cursor=2", "invalid_query"},
		} {
			t.Run(suffix+"?"+tc.query, func(t *testing.T) {
				result := server.request(t, http.MethodGet, base+suffix+"?"+tc.query, authenticate)

				assertAPIError(t, result, http.StatusBadRequest, tc.code)
			})
		}
	}
	for _, cursor := range []string{"not-a-uuid", strings.ToUpper(recordID(0xabcdef)), strings.ReplaceAll(recordID(1), "-", ""), "%20" + recordID(1)} {
		assertAPIError(t, server.request(t, http.MethodGet, base+"/tasks?cursor="+cursor, authenticate), http.StatusBadRequest, "invalid_cursor")
	}
	for _, cursor := range []string{"-1", "%2B1", "1.5", "0x10", "one", "18446744073709551616", "%201", "1%20"} {
		assertAPIError(t, server.request(t, http.MethodGet, base+"/events?cursor="+cursor, authenticate), http.StatusBadRequest, "invalid_cursor")
	}

	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("invalid pagination queries changed stored operation state")
	}
}

type taskPage struct {
	Items      []operations.Task `json:"items"`
	NextCursor string            `json:"next_cursor"`
}
