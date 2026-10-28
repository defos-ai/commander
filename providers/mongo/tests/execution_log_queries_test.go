package mongo_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/executions"
	mongoexecutions "github.com/defos-ai/commander/providers/mongo/executions"
	"github.com/defos-ai/commander/providers/mongo/tests/fixtures"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestExecutionMongoStoreLogQueriesValidateBeforeDatabaseAccess(t *testing.T) {
	catalog := &mongoexecutions.ExecutionMongoStore{}
	for _, query := range []executions.LogQuery{
		{}, {Limit: executions.MaxLogPageSize + 1}, {Limit: 1, OperationID: "invalid"},
		{Limit: 1, TaskID: "invalid"}, {Limit: 1, AgentID: "invalid"}, {Limit: 1, ActionName: " "},
		{Limit: 1, AfterLogID: "invalid"},
	} {
		_, err := catalog.ListLogs(t.Context(), query)

		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("invalid query reached the database: %+v, %v", query, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, cancelled} {
		_, err := catalog.ListLogs(ctx, executions.LogQuery{Limit: 1})

		if err == nil || (ctx != nil && !errors.Is(err, context.Canceled)) {
			t.Fatalf("invalid query context reached the database: %v", err)
		}
	}
}

func TestExecutionMongoStoreLogQueriesFollowIngestionOrderAndResumeAtTail(t *testing.T) {
	database := fixtures.OpenLogDatabase(t)
	catalog := fixtures.OpenExecutionStore(t, database)
	logs := fixtures.NumberedExecutionLogs(5)
	for index := range 2 {
		logs[index].ID, logs[3-index].ID = logs[3-index].ID, logs[index].ID
	}
	for _, log := range logs[:4] {
		if err := catalog.AppendLog(t.Context(), log); err != nil {
			t.Fatal(err)
		}
	}
	before := fixtures.CollectionDocuments(t, database, "execution_log_counters")
	saved := fixtures.CollectionDocuments(t, database, "execution_logs")
	if err := catalog.AppendLog(t.Context(), logs[0]); err != nil {
		t.Fatal(err)
	}

	first, err := catalog.ListLogs(t.Context(), executions.LogQuery{Limit: 2})

	assertLogs(t, first, err, logs[:2])
	if after := fixtures.CollectionDocuments(t, database, "execution_log_counters"); !slices.Equal(before, after) {
		t.Fatal("identical replay advanced the ingestion counter")
	}
	second, err := catalog.ListLogs(t.Context(), executions.LogQuery{Limit: 2, AfterLogID: first[len(first)-1].ID})
	assertLogs(t, second, err, logs[2:4])
	after := second[len(second)-1].ID
	tail, err := catalog.ListLogs(t.Context(), executions.LogQuery{Limit: 2, AfterLogID: after})
	assertLogs(t, tail, err, nil)
	if after := fixtures.CollectionDocuments(t, database, "execution_logs"); !slices.Equal(saved, after) {
		t.Fatal("pagination changed saved logs")
	}
	if after := fixtures.CollectionDocuments(t, database, "execution_log_counters"); !slices.Equal(before, after) {
		t.Fatal("pagination changed the ingestion counter")
	}

	if err := catalog.AppendLog(t.Context(), logs[4]); err != nil {
		t.Fatal(err)
	}
	resumed, err := catalog.ListLogs(t.Context(), executions.LogQuery{Limit: 2, AfterLogID: after})

	assertLogs(t, resumed, err, logs[4:])
}

func TestExecutionMongoStoreLogQueriesIntersectExactFilters(t *testing.T) {
	database := fixtures.OpenLogDatabase(t)
	catalog := fixtures.OpenExecutionStore(t, database)
	logs := fixtures.NumberedExecutionLogs(6)
	logs[1].Work.TaskID = "40000000-0000-4000-8000-000000000002"
	logs[2].Work.OperationID = "30000000-0000-4000-8000-000000000002"
	logs[2].AgentID = "builder-agent-89abcdef"
	logs[3].ActionName = "Build-Agent"
	logs[4].Work = messages.WorkContext{}
	logs[5].ActionName = ` {"$ne":null} `
	for _, log := range logs {
		if err := catalog.AppendLog(t.Context(), log); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name  string
		query executions.LogQuery
		want  []executions.Log
	}{
		{"unfiltered includes unscoped", executions.LogQuery{}, logs},
		{"operation", executions.LogQuery{OperationID: logs[0].Work.OperationID}, []executions.Log{logs[0], logs[1], logs[3], logs[5]}},
		{"task", executions.LogQuery{TaskID: logs[0].Work.TaskID}, []executions.Log{logs[0], logs[2], logs[3], logs[5]}},
		{"agent", executions.LogQuery{AgentID: logs[2].AgentID}, []executions.Log{logs[2]}},
		{"action case sensitive", executions.LogQuery{ActionName: "Build-Agent"}, []executions.Log{logs[3]}},
		{"action literal", executions.LogQuery{ActionName: logs[5].ActionName}, []executions.Log{logs[5]}},
		{"all filters", executions.LogQuery{OperationID: logs[0].Work.OperationID, TaskID: logs[0].Work.TaskID,
			AgentID: logs[0].AgentID, ActionName: logs[0].ActionName}, []executions.Log{logs[0]}},
		{"negative intersection", executions.LogQuery{OperationID: logs[0].Work.OperationID, AgentID: logs[2].AgentID}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := test.query
			query.Limit = 10

			page, err := catalog.ListLogs(t.Context(), query)

			assertLogs(t, page, err, test.want)
		})
	}
	query := executions.LogQuery{AgentID: logs[2].AgentID, Limit: 1}

	page, err := catalog.ListLogs(t.Context(), query)

	assertLogs(t, page, err, []executions.Log{logs[2]})
	query.AfterLogID = page[len(page)-1].ID
	tail, err := catalog.ListLogs(t.Context(), query)
	assertLogs(t, tail, err, nil)
	late := fixtures.NumberedExecutionLogs(7)[6]
	late.AgentID = query.AgentID
	if err := catalog.AppendLog(t.Context(), late); err != nil {
		t.Fatal(err)
	}

	resumed, err := catalog.ListLogs(t.Context(), query)

	assertLogs(t, resumed, err, []executions.Log{late})
}

func TestExecutionMongoStoreLogQueriesRejectUnknownOrOutOfScopeCursor(t *testing.T) {
	database := fixtures.OpenLogDatabase(t)
	catalog := fixtures.OpenExecutionStore(t, database)
	logs := fixtures.NumberedExecutionLogs(2)

	_, err := catalog.ListLogs(t.Context(), executions.LogQuery{Limit: 1, AfterLogID: logs[0].ID})

	if !errors.Is(err, executions.ErrUnknownLog) {
		t.Fatalf("unknown cursor in empty store was accepted: %v", err)
	}
	if err := catalog.AppendLog(t.Context(), logs[0]); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		query executions.LogQuery
	}{
		{"unknown", executions.LogQuery{AfterLogID: logs[1].ID}},
		{"operation", executions.LogQuery{AfterLogID: logs[0].ID, OperationID: "30000000-0000-4000-8000-000000000002"}},
		{"task", executions.LogQuery{AfterLogID: logs[0].ID, TaskID: "40000000-0000-4000-8000-000000000002"}},
		{"agent", executions.LogQuery{AfterLogID: logs[0].ID, AgentID: "builder-agent-89abcdef"}},
		{"action", executions.LogQuery{AfterLogID: logs[0].ID, ActionName: "Build-Agent"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := test.query
			query.Limit = 1

			_, err := catalog.ListLogs(t.Context(), query)

			if !errors.Is(err, executions.ErrUnknownLog) {
				t.Fatalf("invalid scoped cursor was accepted: %+v, %v", query, err)
			}
		})
	}
}

func TestExecutionMongoStoreLogQueriesValidateStoredCursorBeforeReturningAnEmptyTail(t *testing.T) {
	for _, test := range []struct {
		name    string
		changes bson.M
	}{
		{"uncommitted", bson.M{"position": int64(2)}},
		{"metadata mismatch", bson.M{"agent_id": "builder-agent-89abcdef"}},
		{"malformed payload", bson.M{"data": []byte("{")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := fixtures.OpenLogDatabase(t)
			catalog := fixtures.OpenExecutionStore(t, database)
			log := fixtures.ExecutionLog()
			if err := catalog.AppendLog(t.Context(), log); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Update(t.Context(), "execution_logs", bson.M{"_id": log.ID},
				bson.M{"$set": test.changes}); err != nil {
				t.Fatal(err)
			}

			_, err := catalog.ListLogs(t.Context(), executions.LogQuery{Limit: 1, AfterLogID: log.ID})

			if err == nil || (test.name == "uncommitted" && !errors.Is(err, executions.ErrUnknownLog)) {
				t.Fatalf("invalid stored cursor was accepted: %v", err)
			}
		})
	}
}

func TestExecutionMongoStoreLogQueriesFilterAndBoundDocumentsBeforeDecode(t *testing.T) {
	database := fixtures.OpenLogDatabase(t)
	catalog := fixtures.OpenExecutionStore(t, database)
	logs := fixtures.NumberedExecutionLogs(3)
	logs[2].AgentID = "builder-agent-89abcdef"
	for _, log := range logs {
		if err := catalog.AppendLog(t.Context(), log); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Update(t.Context(), "execution_logs", bson.M{"_id": logs[2].ID},
		bson.M{"$set": bson.M{"data": []byte("{")}}); err != nil {
		t.Fatal(err)
	}

	bounded, boundedErr := catalog.ListLogs(t.Context(), executions.LogQuery{Limit: 1})
	filtered, filteredErr := catalog.ListLogs(t.Context(), executions.LogQuery{Limit: 10, AgentID: logs[0].AgentID})
	_, corruptErr := catalog.ListLogs(t.Context(), executions.LogQuery{Limit: 1, AfterLogID: logs[1].ID})

	assertLogs(t, bounded, boundedErr, logs[:1])
	assertLogs(t, filtered, filteredErr, logs[:2])
	if corruptErr == nil {
		t.Fatal("page containing malformed stored log was accepted")
	}
}

func TestExecutionMongoStoreLogQueriesDoNotSkipConcurrentAppends(t *testing.T) {
	database := fixtures.OpenLogDatabase(t)
	stores := []executions.ExecutionStore{fixtures.OpenExecutionStore(t, database), fixtures.OpenExecutionStore(t, database)}
	logs := fixtures.NumberedExecutionLogs(24)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	var workers sync.WaitGroup
	t.Cleanup(func() { cancel(); workers.Wait() })
	start := make(chan struct{})
	results := make(chan error, len(logs))
	for index, log := range logs {
		workers.Go(func() {
			<-start
			results <- stores[index%len(stores)].AppendLog(ctx, log)
		})
	}

	close(start)
	seen := make(map[string]executions.Log)
	var after string
	completed := 0
	for completed < len(logs) || len(seen) < len(logs) {
		select {
		case err := <-results:
			completed++
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatalf("pagination did not observe all appends: completed=%d seen=%d cursor=%s: %v", completed, len(seen), after, ctx.Err())
		default:
		}
		page, err := stores[0].ListLogs(ctx, executions.LogQuery{Limit: 3, AfterLogID: after})
		if err != nil {
			t.Fatal(err)
		}
		if page == nil || len(page) > 3 {
			t.Fatalf("invalid concurrent page: %+v", page)
		}
		for _, log := range page {
			if _, duplicate := seen[log.ID]; duplicate {
				t.Fatalf("log %s appeared on multiple pages", log.ID)
			}
			seen[log.ID] = log
		}
		if len(page) > 0 {
			after = page[len(page)-1].ID
		}
	}
	workers.Wait()

	for _, log := range logs {
		if !seen[log.ID].Equal(log) {
			t.Fatalf("committed log was skipped or changed: %s", log.ID)
		}
	}
	tail, err := stores[0].ListLogs(ctx, executions.LogQuery{Limit: 3, AfterLogID: after})
	assertLogs(t, tail, err, nil)
}

func TestExecutionMongoStoreLogsRejectInconsistentIndexedMetadata(t *testing.T) {
	for _, field := range []string{"agent_id", "action_name", "operation_id", "task_id", "step_id"} {
		t.Run(field, func(t *testing.T) {
			database := fixtures.OpenLogDatabase(t)
			catalog := fixtures.OpenExecutionStore(t, database)
			log := fixtures.ExecutionLog()
			if err := catalog.AppendLog(t.Context(), log); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Update(t.Context(), "execution_logs", bson.M{"_id": log.ID}, bson.M{"$set": bson.M{field: "different"}}); err != nil {
				t.Fatal(err)
			}
			before := fixtures.CollectionDocuments(t, database, "execution_logs")

			_, readErr := catalog.GetLog(t.Context(), log.ID)
			_, queryErr := catalog.ListLogs(t.Context(), executions.LogQuery{Limit: 1})

			if readErr == nil || queryErr == nil {
				t.Fatalf("inconsistent metadata was accepted: read=%v query=%v", readErr, queryErr)
			}
			if after := fixtures.CollectionDocuments(t, database, "execution_logs"); !slices.Equal(before, after) {
				t.Fatal("rejected reads mutated the log document")
			}
		})
	}
}

func TestExecutionMongoStoreLogsRejectUnpositionedExistingData(t *testing.T) {
	database := fixtures.OpenLogDatabase(t)
	log := fixtures.ExecutionLog()
	data, err := json.Marshal(log)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Insert(t.Context(), "execution_logs", bson.M{"_id": log.ID, "data": data}); err != nil {
		t.Fatal(err)
	}
	before := fixtures.CollectionDocuments(t, database, "execution_logs")

	_, err = mongoexecutions.NewExecutionMongoStore(t.Context(), database)

	if err == nil {
		t.Fatal("unpositioned existing logs were silently accepted")
	}
	if after := fixtures.CollectionDocuments(t, database, "execution_logs"); !slices.Equal(before, after) {
		t.Fatal("failed initialization changed existing logs")
	}
}

func TestExecutionMongoStoreLogsRejectInconsistentCounter(t *testing.T) {
	for _, test := range []struct {
		name     string
		position any
	}{
		{"null", nil}, {"negative", int64(-1)}, {"behind", int64(0)}, {"ahead", int64(2)},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := fixtures.OpenLogDatabase(t)
			catalog := fixtures.OpenExecutionStore(t, database)
			if err := catalog.AppendLog(t.Context(), fixtures.ExecutionLog()); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Update(t.Context(), "execution_log_counters", bson.M{"_id": "global"},
				bson.M{"$set": bson.M{"position": test.position}}); err != nil {
				t.Fatal(err)
			}

			_, err := mongoexecutions.NewExecutionMongoStore(t.Context(), database)

			if err == nil {
				t.Fatal("counter inconsistent with saved logs was accepted")
			}
		})
	}
}

func TestExecutionMongoStoreLogsRejectPositionOverflowWithoutChangingData(t *testing.T) {
	database := fixtures.OpenLogDatabase(t)
	catalog := fixtures.OpenExecutionStore(t, database)
	logs := fixtures.NumberedExecutionLogs(2)
	if err := catalog.AppendLog(t.Context(), logs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Update(t.Context(), "execution_log_counters", bson.M{"_id": "global"},
		bson.M{"$set": bson.M{"position": int64(math.MaxInt64)}}); err != nil {
		t.Fatal(err)
	}
	before := fixtures.CollectionDocuments(t, database, "execution_logs")

	replayErr := catalog.AppendLog(t.Context(), logs[0])
	appendErr := catalog.AppendLog(t.Context(), logs[1])

	if replayErr != nil || appendErr == nil {
		t.Fatalf("exhausted counter: replay=%v append=%v", replayErr, appendErr)
	}
	if after := fixtures.CollectionDocuments(t, database, "execution_logs"); !slices.Equal(before, after) {
		t.Fatal("overflow append changed existing logs")
	}
}

func assertLogs(t *testing.T, logs []executions.Log, err error, want []executions.Log) {
	t.Helper()
	if err != nil || logs == nil || len(logs) != len(want) {
		t.Fatalf("logs: got count=%d err=%v, want count=%d", len(logs), err, len(want))
	}
	for index, log := range want {
		if !logs[index].Equal(log) {
			t.Fatalf("log at index %d differs: got %+v, want %+v", index, logs[index], log)
		}
	}
}
