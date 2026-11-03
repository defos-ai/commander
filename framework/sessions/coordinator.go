package sessions

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	model "github.com/defos-ai/commander/framework/domain/sessions"
)

type Coordinator interface {
	Normalize(context.Context, operations.Operation, []actions.NextAction, []agents.Agent) ([]actions.NextAction, error)
	CheckAdmission(context.Context, operations.Task) error
	ScopeRecords(context.Context) ([]model.Scope, error)
	Prepare(context.Context, Assignment) (PreparedDispatch, error)
	Observe(context.Context, Evidence) error
}
