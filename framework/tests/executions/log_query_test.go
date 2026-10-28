package executions_test

import (
	"errors"
	"testing"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/executions"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/executions"
)

func TestExecutionLogQueryAcceptsOptionalFiltersAndPaginationBounds(t *testing.T) {
	log := fixtures.Log()
	for _, test := range []struct {
		name  string
		query executions.LogQuery
	}{
		{"all logs", executions.LogQuery{Limit: 1}},
		{"log cursor", executions.LogQuery{AfterLogID: log.ID, Limit: 1}},
		{"operation", executions.LogQuery{OperationID: log.Work.OperationID, Limit: 1000}},
		{"task without operation", executions.LogQuery{TaskID: log.Work.TaskID, Limit: 1}},
		{"agent", executions.LogQuery{AgentID: log.AgentID, Limit: 1}},
		{"action", executions.LogQuery{ActionName: log.ActionName, Limit: 1}},
		{"UTF-8 action", executions.LogQuery{ActionName: "перевірити", Limit: 1}},
		{"exact action text", executions.LogQuery{ActionName: " build-agent ", Limit: 1}},
		{"all filters", executions.LogQuery{OperationID: log.Work.OperationID, TaskID: log.Work.TaskID,
			AgentID: log.AgentID, ActionName: log.ActionName, Limit: 1000, AfterLogID: log.ID}},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := test.query

			err := test.query.Validate()

			if err != nil {
				t.Fatalf("valid execution log query was rejected: %v", err)
			}
			if test.query != original {
				t.Fatal("query validation changed a filter or continuation")
			}
		})
	}
}

func TestExecutionLogQueryRejectsInvalidFiltersAndLimits(t *testing.T) {
	for _, test := range []struct {
		name  string
		query executions.LogQuery
	}{
		{"invalid operation", executions.LogQuery{OperationID: "not-a-uuid", Limit: 1}},
		{"noncanonical operation", executions.LogQuery{OperationID: "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", Limit: 1}},
		{"invalid task", executions.LogQuery{TaskID: "not-a-uuid", Limit: 1}},
		{"noncanonical task", executions.LogQuery{TaskID: "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", Limit: 1}},
		{"agent type without process", executions.LogQuery{AgentID: "builder-agent", Limit: 1}},
		{"noncanonical agent", executions.LogQuery{AgentID: "builder-agent-AAAAAAAA", Limit: 1}},
		{"blank action", executions.LogQuery{ActionName: " \t\n", Limit: 1}},
		{"invalid action encoding", executions.LogQuery{ActionName: "build\xff", Limit: 1}},
		{"invalid cursor", executions.LogQuery{AfterLogID: "not-a-uuid", Limit: 1}},
		{"noncanonical cursor", executions.LogQuery{AfterLogID: "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", Limit: 1}},
		{"numeric cursor", executions.LogQuery{AfterLogID: "1", Limit: 1}},
		{"blank cursor", executions.LogQuery{AfterLogID: " ", Limit: 1}},
		{"missing limit", executions.LogQuery{}},
		{"negative limit", executions.LogQuery{Limit: -1}},
		{"excessive limit", executions.LogQuery{Limit: 1001}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.query.Validate()

			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("invalid execution log query did not return invalid input: %v", err)
			}
		})
	}
}

func TestExecutionLogQueryMatchesExactFilterIntersections(t *testing.T) {
	log := fixtures.Log()
	matching := executions.LogQuery{OperationID: log.Work.OperationID, TaskID: log.Work.TaskID,
		AgentID: log.AgentID, ActionName: log.ActionName, Limit: 1, AfterLogID: log.CommandID}
	for _, test := range []struct {
		name   string
		change func(*executions.LogQuery)
		want   bool
	}{
		{"all criteria", func(*executions.LogQuery) {}, true},
		{"operation differs", func(query *executions.LogQuery) { query.OperationID = log.ID }, false},
		{"task differs", func(query *executions.LogQuery) { query.TaskID = log.ID }, false},
		{"agent differs", func(query *executions.LogQuery) { query.AgentID = "builder-agent-00000002" }, false},
		{"action differs", func(query *executions.LogQuery) { query.ActionName = "Build-agent" }, false},
		{"action prefix", func(query *executions.LogQuery) { query.ActionName = "build" }, false},
		{"action whitespace", func(query *executions.LogQuery) { query.ActionName = " " + log.ActionName + " " }, false},
		{"optional operation", func(query *executions.LogQuery) { query.OperationID = "" }, true},
		{"optional task", func(query *executions.LogQuery) { query.TaskID = "" }, true},
		{"optional agent", func(query *executions.LogQuery) { query.AgentID = "" }, true},
		{"optional action", func(query *executions.LogQuery) { query.ActionName = "" }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := matching
			test.change(&query)

			matched := query.Matches(log)

			if matched != test.want {
				t.Fatalf("exact filter intersection matched=%v, want %v", matched, test.want)
			}
		})
	}
}

func TestExecutionLogQueryIncludesStandaloneDispatches(t *testing.T) {
	log := fixtures.Log()
	log.Work = messages.WorkContext{}
	for _, test := range []struct {
		name  string
		query executions.LogQuery
		want  bool
	}{
		{"all logs", executions.LogQuery{Limit: 1}, true},
		{"agent and action", executions.LogQuery{AgentID: log.AgentID, ActionName: log.ActionName, Limit: 1}, true},
		{"operation", executions.LogQuery{OperationID: fixtures.Log().Work.OperationID, Limit: 1}, false},
		{"task", executions.LogQuery{TaskID: fixtures.Log().Work.TaskID, Limit: 1}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			matched := test.query.Matches(log)

			if matched != test.want {
				t.Fatalf("standalone dispatch matched=%v, want %v", matched, test.want)
			}
		})
	}
}
