package providers_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestExecutionLogQueriesFollowAppendOrderAcrossReopenAndLateWrites(t *testing.T) {
	root := t.TempDir()
	store := fixtures.CheckpointStore(t, root)
	logs := []executions.Log{fixtures.ExecutionLog(30), fixtures.ExecutionLog(20), fixtures.ExecutionLog(10)}
	for index := range logs {
		logs[index].CreatedAt = logs[index].CreatedAt.Add(-time.Duration(index) * time.Hour)
		if err := store.AppendLog(t.Context(), logs[index]); err != nil {
			t.Fatal(err)
		}
	}
	replay := logs[0]
	replay.CreatedAt = replay.CreatedAt.UTC()
	if err := store.AppendLog(t.Context(), replay); err != nil {
		t.Fatal(err)
	}

	page, err := store.ListLogs(t.Context(), executions.LogQuery{Limit: 1})

	assertExecutionLogs(t, page, err, logs[0])
	reopened := fixtures.CheckpointStore(t, root)

	page, err = reopened.ListLogs(t.Context(), executions.LogQuery{Limit: 1, AfterLogID: page[len(page)-1].ID})

	assertExecutionLogs(t, page, err, logs[1])

	page, err = reopened.ListLogs(t.Context(), executions.LogQuery{Limit: 5, AfterLogID: page[len(page)-1].ID})

	assertExecutionLogs(t, page, err, logs[2])

	page, err = reopened.ListLogs(t.Context(), executions.LogQuery{Limit: 1, AfterLogID: logs[2].ID})

	assertExecutionLogs(t, page, err)
	late := fixtures.ExecutionLog(1)
	late.CreatedAt = logs[2].CreatedAt.Add(-time.Hour)

	if err := reopened.AppendLog(t.Context(), late); err != nil {
		t.Fatal(err)
	}
	page, err = reopened.ListLogs(t.Context(), executions.LogQuery{Limit: 5, AfterLogID: logs[2].ID})

	assertExecutionLogs(t, page, err, late)
	conflicting := logs[0]
	conflicting.Message += " changed"
	if err := reopened.AppendLog(t.Context(), conflicting); !errors.Is(err, executions.ErrLogConflict) {
		t.Fatalf("conflicting replay: %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := reopened.AppendLog(canceled, fixtures.ExecutionLog(100)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled append: %v", err)
	}

	page, err = reopened.ListLogs(t.Context(), executions.LogQuery{Limit: 5, AfterLogID: late.ID})

	assertExecutionLogs(t, page, err)
}

func TestExecutionLogQueriesIntersectExactFiltersIncludingStandaloneWork(t *testing.T) {
	store := fixtures.CheckpointStore(t, t.TempDir())
	logs := make([]executions.Log, 6)
	for index := range logs {
		logs[index] = fixtures.ExecutionLog(index + 1)
	}
	logs[1].Work.TaskID = fixtures.ExecutionLog(100).ID
	logs[2].Work.OperationID = fixtures.ExecutionLog(101).ID
	logs[3].AgentID = "builder-agent-87654321"
	logs[4].ActionName = "publish-agent"
	logs[5].Work = messages.WorkContext{}
	for _, log := range logs {
		if err := store.AppendLog(t.Context(), log); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name  string
		query executions.LogQuery
		want  []executions.Log
	}{
		{"all", executions.LogQuery{}, logs},
		{"operation", executions.LogQuery{OperationID: logs[0].Work.OperationID}, []executions.Log{logs[0], logs[1], logs[3], logs[4]}},
		{"task without operation", executions.LogQuery{TaskID: logs[0].Work.TaskID}, []executions.Log{logs[0], logs[2], logs[3], logs[4]}},
		{"agent", executions.LogQuery{AgentID: logs[0].AgentID}, []executions.Log{logs[0], logs[1], logs[2], logs[4], logs[5]}},
		{"action", executions.LogQuery{ActionName: logs[0].ActionName}, []executions.Log{logs[0], logs[1], logs[2], logs[3], logs[5]}},
		{"all filters", executions.LogQuery{OperationID: logs[0].Work.OperationID, TaskID: logs[0].Work.TaskID,
			AgentID: logs[0].AgentID, ActionName: logs[0].ActionName}, []executions.Log{logs[0]}},
		{"case differs", executions.LogQuery{ActionName: "Build-agent"}, nil},
		{"prefix differs", executions.LogQuery{ActionName: "build"}, nil},
		{"whitespace differs", executions.LogQuery{ActionName: " build-agent "}, nil},
		{"unknown operation", executions.LogQuery{OperationID: fixtures.ExecutionLog(102).ID}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := test.query
			query.Limit = 1000

			page, err := store.ListLogs(t.Context(), query)

			assertExecutionLogs(t, page, err, test.want...)
		})
	}
}

func TestExecutionLogQueriesFindMatchesBeyondSparseHistory(t *testing.T) {
	store := fixtures.CheckpointStore(t, t.TempDir())
	var matching executions.Log
	for index := 1; index <= 1002; index++ {
		log := fixtures.ExecutionLog(index)
		if index == 1001 {
			log.ActionName = "publish-agent"
			matching = log
		}
		if err := store.AppendLog(t.Context(), log); err != nil {
			t.Fatal(err)
		}
	}
	query := executions.LogQuery{ActionName: matching.ActionName, Limit: 10}

	first, err := store.ListLogs(t.Context(), query)

	assertExecutionLogs(t, first, err, matching)
	query.AfterLogID = matching.ID

	second, err := store.ListLogs(t.Context(), query)

	assertExecutionLogs(t, second, err)
	late := fixtures.ExecutionLog(1003)
	late.ActionName = matching.ActionName
	late.CreatedAt = matching.CreatedAt.Add(-time.Hour)
	if err := store.AppendLog(t.Context(), late); err != nil {
		t.Fatal(err)
	}

	tail, err := store.ListLogs(t.Context(), query)

	assertExecutionLogs(t, tail, err, late)
}

func TestExecutionLogQueriesRejectUnknownOrFilterMismatchedCursors(t *testing.T) {
	store := fixtures.CheckpointStore(t, t.TempDir())
	log := fixtures.ExecutionLog(1)
	unscoped := fixtures.ExecutionLog(2)
	unscoped.Work = messages.WorkContext{}
	for _, value := range []executions.Log{log, unscoped} {
		if err := store.AppendLog(t.Context(), value); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name  string
		query executions.LogQuery
	}{
		{"unknown ID", executions.LogQuery{AfterLogID: fixtures.ExecutionLog(3).ID}},
		{"operation mismatch", executions.LogQuery{AfterLogID: log.ID, OperationID: fixtures.ExecutionLog(100).ID}},
		{"task mismatch", executions.LogQuery{AfterLogID: log.ID, TaskID: fixtures.ExecutionLog(100).ID}},
		{"agent mismatch", executions.LogQuery{AfterLogID: log.ID, AgentID: "builder-agent-87654321"}},
		{"action mismatch", executions.LogQuery{AfterLogID: log.ID, ActionName: "Build-agent"}},
		{"standalone cursor for operation", executions.LogQuery{AfterLogID: unscoped.ID, OperationID: log.Work.OperationID}},
	} {
		for _, order := range []executions.LogOrder{executions.LogOrderAscending, executions.LogOrderDescending} {
			t.Run(string(order)+"/"+test.name, func(t *testing.T) {
				query := test.query
				query.Limit, query.Order = 1, order

				_, err := store.ListLogs(t.Context(), query)

				if !errors.Is(err, executions.ErrUnknownLog) {
					t.Fatalf("unknown or filter-mismatched cursor was accepted: %v", err)
				}
			})
		}
	}
	query := executions.LogQuery{AfterLogID: log.ID, OperationID: log.Work.OperationID, TaskID: log.Work.TaskID,
		AgentID: log.AgentID, ActionName: log.ActionName, Limit: 1}

	page, err := store.ListLogs(t.Context(), query)

	assertExecutionLogs(t, page, err)
}

func TestExecutionLogQueriesPageWhileIndependentStoresAppend(t *testing.T) {
	root := t.TempDir()
	stores := []executions.ExecutionStore{fixtures.CheckpointStore(t, root), fixtures.CheckpointStore(t, root)}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	var workers sync.WaitGroup
	t.Cleanup(func() { cancel(); workers.Wait() })
	const count = 40
	start := make(chan struct{})
	progress := make(chan struct{}, count)
	failures := make(chan error, len(stores))
	done := make(chan struct{})
	for worker := range stores {
		workers.Go(func() {
			<-start
			for index := worker + 1; index <= count; index += len(stores) {
				log := fixtures.ExecutionLog(index)
				log.CreatedAt = log.CreatedAt.Add(-time.Duration(index) * time.Hour)
				if err := stores[worker].AppendLog(ctx, log); err != nil {
					failures <- err
					return
				}
				progress <- struct{}{}
			}
		})
	}
	go func() { workers.Wait(); close(failures); close(done) }()
	seen := make(map[string]bool, count)
	observed := make([]executions.Log, 0, count)
	query := executions.LogQuery{Limit: 3}
	writersDone := false

	close(start)
	for {
		page, err := stores[0].ListLogs(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		if page == nil || len(page) > query.Limit {
			t.Fatalf("invalid concurrent page: %+v", page)
		}
		for _, log := range page {
			if seen[log.ID] {
				t.Fatalf("concurrent pagination repeated %s", log.ID)
			}
			seen[log.ID] = true
			observed = append(observed, log)
		}
		if len(page) > 0 {
			query.AfterLogID = page[len(page)-1].ID
		} else {
			if writersDone {
				break
			}
			select {
			case <-done:
				writersDone = true
			case <-progress:
			case <-ctx.Done():
				t.Fatal("concurrent append and pagination did not finish")
			}
		}
	}
	for err := range failures {
		t.Errorf("concurrent append: %v", err)
	}
	if len(seen) != count {
		t.Fatalf("concurrent pagination skipped logs: records=%d", len(seen))
	}
	for index := 1; index <= count; index++ {
		if !seen[fixtures.ExecutionLog(index).ID] {
			t.Fatalf("concurrent pagination missed log %d", index)
		}
	}
	all, err := stores[0].ListLogs(ctx, executions.LogQuery{Limit: 1000})
	assertExecutionLogs(t, all, err, observed...)
}

func TestExecutionLogQueriesRejectInvalidInputWithoutCreatingState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	store := fixtures.CheckpointStore(t, root)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, canceled} {
		for _, order := range []executions.LogOrder{executions.LogOrderAscending, executions.LogOrderDescending} {
			if _, err := store.ListLogs(ctx, executions.LogQuery{Order: order, Limit: 1}); err == nil || ctx != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("invalid query context was accepted: %v", err)
			}
		}
	}
	for _, query := range []executions.LogQuery{{}, {Limit: 1001}, {OperationID: "../outside", Limit: 1}, {AfterLogID: "../outside", Limit: 1},
		{Order: "newest", AfterLogID: fixtures.ExecutionLog(1).ID, Limit: 1}} {
		if _, err := store.ListLogs(t.Context(), query); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("invalid query did not return invalid input: %v", err)
		}
	}

	page, err := store.ListLogs(t.Context(), executions.LogQuery{Limit: 1})

	assertExecutionLogs(t, page, err)
	if _, err := store.ListLogs(t.Context(), executions.LogQuery{Limit: 1, AfterLogID: fixtures.ExecutionLog(1).ID}); !errors.Is(err, executions.ErrUnknownLog) {
		t.Fatalf("missing cursor was accepted: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty or invalid query created state: %v", err)
	}
}

func assertExecutionLogs(t *testing.T, page []executions.Log, err error, expected ...executions.Log) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if page == nil || len(page) != len(expected) {
		t.Fatalf("log page items=%d, want items=%d", len(page), len(expected))
	}
	for index, log := range page {
		if !log.Equal(expected[index]) {
			t.Fatalf("log at position %d changed: %+v, want %+v", index, log, expected[index])
		}
	}
}
