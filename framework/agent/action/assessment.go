package action

import (
	"context"
	"github.com/defos-ai/commander/framework/domain/assessments"
)

type assessmentContextKey struct{}

// WithAssessment supplies the controller's immutable operation scope to actions
// that resolve destinations from referenced artifacts or other structured input.
func WithAssessment(ctx context.Context, scope *assessments.Snapshot) context.Context {
	return context.WithValue(ctx, assessmentContextKey{}, scope.Clone())
}

func AssessmentFromContext(ctx context.Context) *assessments.Snapshot {
	scope, _ := ctx.Value(assessmentContextKey{}).(*assessments.Snapshot)
	return scope.Clone()
}
