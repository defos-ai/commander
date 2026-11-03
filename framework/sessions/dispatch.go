package sessions

import (
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/operations"
	model "github.com/defos-ai/commander/framework/domain/sessions"
)

type Assignment struct {
	OperationID, TaskID, Candidate string
	Timeout                        time.Duration
}
type PreparedDispatch struct {
	Operation operations.Operation
	Task      operations.Task
	Command   messages.Message
	Binding   *model.Binding
}
