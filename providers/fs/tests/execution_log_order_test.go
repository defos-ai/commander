package providers_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestExecutionLogsPageInBothDirectionsAndFollowNewAppends(t *testing.T) {
	root := t.TempDir()
	store := fixtures.CheckpointStore(t, root)
	logs := make([]executions.Log, 9)
	for index := range logs {
		logs[index] = fixtures.ExecutionLog(90 - index*10)
		logs[index].CreatedAt = logs[index].CreatedAt.Add(-time.Duration(index) * time.Hour)
	}
	logs[1].AgentID = "builder-agent-87654321"
	logs[3].Work.OperationID, logs[3].Work.TaskID = fixtures.ExecutionLog(903).ID, fixtures.ExecutionLog(904).ID
	logs[4].ActionName = "publish-agent"
	logs[5].Work.TaskID = fixtures.ExecutionLog(905).ID
	logs[8].Work.OperationID, logs[8].Work.TaskID = fixtures.ExecutionLog(908).ID, fixtures.ExecutionLog(909).ID
	for _, log := range logs {
		if err := store.AppendLog(t.Context(), log); err != nil {
			t.Fatal(err)
		}
	}
	query := executions.LogQuery{OperationID: logs[0].Work.OperationID, TaskID: logs[0].Work.TaskID,
		AgentID: logs[0].AgentID, ActionName: logs[0].ActionName, Order: executions.LogOrderDescending, Limit: 2}
	reopened := fixtures.CheckpointStore(t, root)
	for _, test := range []struct {
		order executions.LogOrder
		want  []executions.Log
	}{
		{executions.LogOrderAscending, []executions.Log{logs[0], logs[2], logs[6], logs[7]}},
		{executions.LogOrderDescending, []executions.Log{logs[7], logs[6], logs[2], logs[0]}},
	} {
		t.Run(string(test.order), func(t *testing.T) {
			criteria := query
			criteria.Order = test.order
			for _, expected := range [][]executions.Log{test.want[:2], test.want[2:], nil} {
				page, err := reopened.ListLogs(t.Context(), criteria)

				assertExecutionLogs(t, page, err, expected...)
				if len(page) > 0 {
					criteria.AfterLogID = page[len(page)-1].ID
				}
			}
		})
	}

	page, err := reopened.ListLogs(t.Context(), query)

	assertExecutionLogs(t, page, err, logs[7], logs[6])
	query.Order, query.AfterLogID = executions.LogOrderAscending, page[0].ID

	tail, err := reopened.ListLogs(t.Context(), query)

	assertExecutionLogs(t, tail, err)
	late := fixtures.ExecutionLog(1)
	late.CreatedAt = logs[7].CreatedAt.Add(-24 * time.Hour)
	if err := store.AppendLog(t.Context(), late); err != nil {
		t.Fatal(err)
	}

	continued, err := reopened.ListLogs(t.Context(), query)

	assertExecutionLogs(t, continued, err, late)
	query.Order, query.AfterLogID = executions.LogOrderDescending, ""

	newest, err := reopened.ListLogs(t.Context(), query)

	assertExecutionLogs(t, newest, err, late, logs[7])
}

func TestDescendingExecutionLogsHandleEmptySmallAndExactPagesWithoutReadingOlderHistory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	store := fixtures.CheckpointStore(t, root)

	empty, err := store.ListLogs(t.Context(), executions.LogQuery{Order: executions.LogOrderDescending, Limit: 10})

	assertExecutionLogs(t, empty, err)
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reading descending logs created state: %v", err)
	}
	logs := []executions.Log{fixtures.ExecutionLog(1), fixtures.ExecutionLog(2), fixtures.ExecutionLog(3)}
	logs[1].Work = messages.WorkContext{}
	for _, log := range logs {
		if err := store.AppendLog(t.Context(), log); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name  string
		limit int
		want  []executions.Log
	}{
		{"one", 1, logs[2:]},
		{"exact", 3, []executions.Log{logs[2], logs[1], logs[0]}},
		{"fewer than limit", 10, []executions.Log{logs[2], logs[1], logs[0]}},
	} {
		t.Run(test.name, func(t *testing.T) {
			page, err := store.ListLogs(t.Context(), executions.LogQuery{Order: executions.LogOrderDescending, Limit: test.limit})

			assertExecutionLogs(t, page, err, test.want...)
		})
	}

	missing, err := store.ListLogs(t.Context(), executions.LogQuery{Order: executions.LogOrderDescending, ActionName: "missing-action", Limit: 10})

	assertExecutionLogs(t, missing, err)
	fixtures.WriteExecutionLogState(t, root, logs[0].ID, []byte(`corrupted older history`))

	newest, err := store.ListLogs(t.Context(), executions.LogQuery{Order: executions.LogOrderDescending, Limit: 2})

	assertExecutionLogs(t, newest, err, logs[2], logs[1])

	_, err = store.ListLogs(t.Context(), executions.LogQuery{Order: executions.LogOrderDescending, AfterLogID: logs[1].ID, Limit: 2})

	if err == nil || errors.Is(err, executions.ErrUnknownLog) {
		t.Fatalf("descending pagination hid damaged selected history: %v", err)
	}
}
