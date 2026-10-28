package executions_test

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/executions"
)

func TestExecutionLogRequiresExecutionIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*executions.Log)
	}{
		{"missing log ID", func(log *executions.Log) { log.ID = "" }},
		{"invalid log ID", func(log *executions.Log) { log.ID = "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA" }},
		{"invalid agent", func(log *executions.Log) { log.AgentID = "builder-agent" }},
		{"missing command", func(log *executions.Log) { log.CommandID = "" }},
		{"missing action", func(log *executions.Log) { log.ActionName = " \t" }},
		{"invalid action encoding", func(log *executions.Log) { log.ActionName = "build\xff" }},
		{"incomplete work", func(log *executions.Log) { log.Work.TaskID = "" }},
		{"invalid work", func(log *executions.Log) { log.Work.OperationID = "not-a-uuid" }},
		{"blank step", func(log *executions.Log) { log.Work.StepID = " " }},
		{"invalid step encoding", func(log *executions.Log) { log.Work.StepID = "build\xff" }},
		{"missing timestamp", func(log *executions.Log) { log.CreatedAt = time.Time{} }},
		{"invalid message", func(log *executions.Log) { log.Message = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			log := fixtures.Log()
			test.change(&log)

			err := log.Validate()

			if err == nil {
				t.Fatal("invalid execution log was accepted")
			}
		})
	}
}

func TestExecutionLogAcceptsProgressWithOrWithoutOperation(t *testing.T) {
	for _, withOperation := range []bool{false, true} {
		name := "standalone dispatch"
		if withOperation {
			name = "operation task"
		}
		t.Run(name, func(t *testing.T) {
			log := fixtures.Log()
			log.Message = "  Compiling packages\nTests are next ✓  "
			if !withOperation {
				log.Work = messages.WorkContext{}
			}
			original := log

			err := log.Validate()

			if err != nil {
				t.Fatalf("valid execution log was rejected: %v", err)
			}
			if log != original {
				t.Fatal("validation changed the observation or execution identity")
			}
		})
	}
}
