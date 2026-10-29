package packages

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/defos-ai/commander/framework/agent/action"
	agentpackages "github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/id"
)

var (
	ErrSourceApprovalRequired = errors.New("agent source requires installation approval")
	ErrSourceApprovalDenied   = errors.New("agent source installation was not approved")
)

type sourceApprovalKey struct{}
type sourceApproval struct{ selector, commandID, operationID string }

func AuthorizeSource(ctx context.Context, selector string, visibility agentpackages.Visibility) (context.Context, error) {
	reference, err := agentpackages.ParseSelector(selector)
	if err != nil {
		return ctx, err
	}
	if strings.EqualFold(reference.Owner, "defos-ai") {
		return ctx, nil
	}
	execution, ok := action.ExecutionFromContext(ctx)
	if !ok || !id.Valid(execution.CommandID) || !id.Valid(execution.Work.OperationID) || execution.Work.Validate() != nil {
		return ctx, ErrSourceApprovalRequired
	}
	text := fmt.Sprintf("Install agent from %s (repository visibility: %s)?", reference.String(), visibility)
	err = action.RunWithProgress(ctx, "Installation approval", func() error {
		question, err := action.Question(ctx, "install-source", text, "Yes", "No")
		if err != nil {
			return errors.Join(ErrSourceApprovalRequired, err)
		}
		if !strings.EqualFold(strings.TrimSpace(question.Answer), "Yes") {
			return ErrSourceApprovalDenied
		}
		return nil
	})
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, sourceApprovalKey{}, sourceApproval{
		selector: reference.String(), commandID: execution.CommandID, operationID: execution.Work.OperationID,
	}), nil
}

func RequireTrustedSource(ctx context.Context, selector string) error {
	reference, err := agentpackages.ParseSelector(selector)
	if err != nil {
		return err
	}
	if !strings.EqualFold(reference.Owner, "defos-ai") {
		approval, _ := ctx.Value(sourceApprovalKey{}).(sourceApproval)
		execution, ok := action.ExecutionFromContext(ctx)
		if !ok || approval.commandID == "" || approval.commandID != execution.CommandID || approval.operationID != execution.Work.OperationID || approval.selector != reference.String() {
			return ErrSourceApprovalRequired
		}
	}
	return nil
}
