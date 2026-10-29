package fixtures

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

const SourceApprovalQuestion = "Install agent from github.com/other/worker-agent@stable (repository visibility: public)?"
const PrivateSourceApprovalQuestion = "Install agent from github.com/other/worker-agent@stable (repository visibility: private)?"

func SourceApprovalExecution() action.Execution {
	return action.Execution{CommandID: id.MessageID(), Work: messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()}}
}

func SourceApprovalAnswer(answer string, origin operations.AnswerOrigin) operations.Question {
	now := time.Now().UTC()
	return operations.Question{
		ID: id.QuestionID(), Key: "install-source", Text: SourceApprovalQuestion, Options: []string{"Yes", "No"},
		Status: operations.QuestionAnswered, Answer: answer, AnsweredBy: origin,
		AskedAt: now.Add(-3 * time.Minute), FallbackAt: now.Add(-time.Minute), AnsweredAt: &now,
	}
}

func SourceApprovalResolver(t *testing.T) (*packages.Resolver, *fakes.ApprovalSource) {
	t.Helper()
	source := new(fakes.ApprovalSource)
	resolver := packages.NewResolver(source)
	t.Cleanup(resolver.CloseIdleConnections)
	return resolver, source
}
