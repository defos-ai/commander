package mongo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	mongoexecutions "github.com/defos-ai/commander/providers/mongo/executions"
	"github.com/defos-ai/commander/providers/mongo/tests/fixtures"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestExecutionMongoStoreRequiresContextAndDatabase(t *testing.T) {
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, cancelled, t.Context()} {
		_, err := mongoexecutions.NewExecutionMongoStore(ctx, nil)

		if err == nil || (ctx == cancelled && !errors.Is(err, context.Canceled)) {
			t.Fatalf("invalid constructor arguments were accepted: %v", err)
		}
	}
}

func TestExecutionMongoStoreLogsPreservesContentAndIdenticalReplays(t *testing.T) {
	database := fixtures.OpenLogDatabase(t)
	catalog := fixtures.OpenExecutionStore(t, database)
	value := fixtures.ExecutionLog()
	value.CreatedAt = value.CreatedAt.In(time.FixedZone("stored", 3*60*60))
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.GetLog(t.Context(), value.ID); !errors.Is(err, executions.ErrUnknownLog) {
		t.Fatalf("absent execution log: %v", err)
	}
	if documents := fixtures.CollectionDocuments(t, database, "execution_logs"); len(documents) != 0 {
		t.Fatal("missing-record read created execution logs")
	}

	if err := catalog.AppendLog(t.Context(), value); err != nil {
		t.Fatal(err)
	}

	reopened := fixtures.OpenExecutionStore(t, database)
	saved, err := reopened.GetLog(t.Context(), value.ID)
	if err != nil || !saved.Equal(value) || saved.Message != value.Message || saved.CreatedAt.Nanosecond() != 123456789 {
		t.Fatalf("execution log lost content or timestamp precision: %+v, %v", saved, err)
	}
	var document bson.Raw
	if err := database.GetByID(t.Context(), "execution_logs", value.ID, &document); err != nil {
		t.Fatal(err)
	}
	if _, data, ok := document.Lookup("data").BinaryOK(); !ok || !bytes.Equal(data, payload) {
		t.Fatal("execution log payload was not preserved as JSON binary")
	}
	before := fixtures.CollectionDocuments(t, database, "execution_logs")
	replay := value
	replay.CreatedAt = replay.CreatedAt.UTC()

	if err := reopened.AppendLog(t.Context(), replay); err != nil {
		t.Fatalf("identical replay with another timezone: %v", err)
	}

	if after := fixtures.CollectionDocuments(t, database, "execution_logs"); !slices.Equal(before, after) {
		t.Fatal("identical replay changed the saved execution log")
	}
	distinct := value
	distinct.ID = id.New()

	if err := catalog.AppendLog(t.Context(), distinct); err != nil {
		t.Fatal(err)
	}

	if documents := fixtures.CollectionDocuments(t, database, "execution_logs"); len(documents) != 2 {
		t.Fatalf("distinct log IDs did not retain separate records: %d", len(documents))
	}
}

func TestExecutionMongoStoreLogsRejectsConflictingContent(t *testing.T) {
	database := fixtures.OpenLogDatabase(t)
	catalog := fixtures.OpenExecutionStore(t, database)
	value := fixtures.ExecutionLog()
	if err := catalog.AppendLog(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	before := fixtures.CollectionDocuments(t, database, "execution_logs")
	for _, test := range []struct {
		name   string
		change func(*executions.Log)
	}{
		{"agent", func(log *executions.Log) { log.AgentID = "builder-agent-89abcdef" }},
		{"command", func(log *executions.Log) { log.CommandID = id.New() }},
		{"action", func(log *executions.Log) { log.ActionName = "other-action" }},
		{"operation", func(log *executions.Log) { log.Work.OperationID = id.New() }},
		{"task", func(log *executions.Log) { log.Work.TaskID = id.New() }},
		{"message", func(log *executions.Log) { log.Message += "changed" }},
		{"timestamp", func(log *executions.Log) { log.CreatedAt = log.CreatedAt.Add(time.Nanosecond) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			conflicting := value
			test.change(&conflicting)

			err := catalog.AppendLog(t.Context(), conflicting)

			if !errors.Is(err, executions.ErrLogConflict) {
				t.Fatalf("conflicting execution log was accepted: %v", err)
			}
		})
	}
	if after := fixtures.CollectionDocuments(t, database, "execution_logs"); !slices.Equal(before, after) {
		t.Fatal("conflicting append changed the saved execution log")
	}
}

func TestExecutionMongoStoreLogsRejectsInvalidAndCancelledRequests(t *testing.T) {
	catalog := &mongoexecutions.ExecutionMongoStore{}
	value := fixtures.ExecutionLog()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	invalid := value
	invalid.Message = ""

	if err := catalog.AppendLog(t.Context(), invalid); err == nil {
		t.Fatal("invalid execution log was accepted")
	}
	if _, err := catalog.GetLog(t.Context(), "not-a-uuid"); err == nil {
		t.Fatal("invalid execution log ID was accepted")
	}
	for _, ctx := range []context.Context{nil, cancelled} {
		if err := catalog.AppendLog(ctx, value); err == nil || (ctx != nil && !errors.Is(err, context.Canceled)) {
			t.Fatalf("invalid append context was accepted: %v", err)
		}
		if _, err := catalog.GetLog(ctx, value.ID); err == nil || (ctx != nil && !errors.Is(err, context.Canceled)) {
			t.Fatalf("invalid read context was accepted: %v", err)
		}
	}
}

func TestExecutionMongoStoreLogsConcurrentAppends(t *testing.T) {
	for _, sameContent := range []bool{true, false} {
		name := "conflicting"
		if sameContent {
			name = "identical"
		}
		t.Run(name, func(t *testing.T) {
			database := fixtures.OpenLogDatabase(t)
			stores := []executions.ExecutionStore{fixtures.OpenExecutionStore(t, database), fixtures.OpenExecutionStore(t, database)}
			value := fixtures.ExecutionLog()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			var workers sync.WaitGroup
			t.Cleanup(func() { cancel(); workers.Wait() })
			start := make(chan struct{})
			type result struct {
				log executions.Log
				err error
			}
			results := make(chan result, 8)
			for i := range 8 {
				candidate := value
				if !sameContent {
					candidate.CreatedAt = candidate.CreatedAt.Add(time.Duration(i) * time.Nanosecond)
				}
				workers.Go(func() {
					<-start
					results <- result{candidate, stores[i%len(stores)].AppendLog(ctx, candidate)}
				})
			}

			close(start)
			workers.Wait()

			successes := 0
			var winner executions.Log
			for range 8 {
				attempt := <-results
				if attempt.err == nil {
					successes++
					winner = attempt.log
				} else if sameContent || !errors.Is(attempt.err, executions.ErrLogConflict) {
					t.Errorf("concurrent append failed unexpectedly: %v", attempt.err)
				}
			}
			if (sameContent && successes != 8) || (!sameContent && successes != 1) {
				t.Fatalf("concurrent append successes=%d", successes)
			}
			saved, err := stores[0].GetLog(ctx, value.ID)
			if err != nil || !saved.Equal(winner) {
				t.Fatalf("concurrent append lost the accepted record: %+v, %v", saved, err)
			}
			if documents := fixtures.CollectionDocuments(t, database, "execution_logs"); len(documents) != 1 {
				t.Fatalf("concurrent append created %d records for one ID", len(documents))
			}
		})
	}
}

func TestExecutionMongoStoreLogsRejectsCorruptDocuments(t *testing.T) {
	for _, test := range []struct {
		name    string
		corrupt func([]byte) []byte
	}{
		{"malformed JSON", func([]byte) []byte { return []byte("{") }},
		{"invalid record", func([]byte) []byte { return []byte(`{}`) }},
		{"wrong ID", func(data []byte) []byte {
			return bytes.Replace(data, []byte(`"id":"10000000-0000-4000-8000-000000000001"`), []byte(`"id":"10000000-0000-4000-8000-000000000002"`), 1)
		}},
		{"unknown field", func(data []byte) []byte { return append([]byte(`{"unexpected":true,`), data[1:]...) }},
		{"trailing JSON", func(data []byte) []byte { return append(data, []byte(`{}`)...) }},
		{"invalid UTF-8", func(data []byte) []byte { return bytes.Replace(data, []byte("Compiling"), []byte{0xff}, 1) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := fixtures.OpenLogDatabase(t)
			catalog := fixtures.OpenExecutionStore(t, database)
			value := fixtures.ExecutionLog()
			if err := catalog.AppendLog(t.Context(), value); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			payload := test.corrupt(data)
			if _, err := database.Update(t.Context(), "execution_logs", bson.M{"_id": value.ID}, bson.M{"$set": bson.M{"data": payload}}); err != nil {
				t.Fatal(err)
			}

			_, readErr := catalog.GetLog(t.Context(), value.ID)
			appendErr := catalog.AppendLog(t.Context(), value)

			if readErr == nil || appendErr == nil {
				t.Fatalf("corrupt document was accepted: read=%v append=%v", readErr, appendErr)
			}
			var saved struct{ Data []byte }
			if err := database.GetByID(t.Context(), "execution_logs", value.ID, &saved); err != nil || !bytes.Equal(saved.Data, payload) {
				t.Fatalf("rejected append changed the corrupt document: %v", err)
			}
		})
	}
}

func TestExecutionMongoStoreLogsRejectsInvalidStoredPositions(t *testing.T) {
	for _, test := range []struct {
		name   string
		update bson.M
	}{
		{"missing", bson.M{"$unset": bson.M{"position": ""}}},
		{"zero", bson.M{"$set": bson.M{"position": int64(0)}}},
		{"negative", bson.M{"$set": bson.M{"position": int64(-1)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := fixtures.OpenLogDatabase(t)
			catalog := fixtures.OpenExecutionStore(t, database)
			value := fixtures.ExecutionLog()
			if err := catalog.AppendLog(t.Context(), value); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Update(t.Context(), "execution_logs", bson.M{"_id": value.ID}, test.update); err != nil {
				t.Fatal(err)
			}
			before := fixtures.CollectionDocuments(t, database, "execution_logs")

			_, readErr := catalog.GetLog(t.Context(), value.ID)
			appendErr := catalog.AppendLog(t.Context(), value)

			if readErr == nil || appendErr == nil {
				t.Fatalf("invalid stored position was accepted: read=%v append=%v", readErr, appendErr)
			}
			if after := fixtures.CollectionDocuments(t, database, "execution_logs"); !slices.Equal(before, after) {
				t.Fatal("rejected access changed the saved execution log")
			}
		})
	}
}
