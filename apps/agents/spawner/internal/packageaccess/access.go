package packageaccess

import (
	"context"
	"errors"
	"fmt"
	"os"

	downloads "github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/packages"
)

// Acquire returns credentials owned by the caller, which must clear them after access.
func Acquire(ctx context.Context, visibility packages.Visibility) (context.Context, []byte, error) {
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	var token []byte
	if visibility == packages.Private {
		token = []byte(os.Getenv("GITHUB_TOKEN"))
		if len(token) == 0 {
			err := action.RunWithProgress(ctx, "Private package access", func() error {
				var secretErr error
				token, secretErr = action.RequestSecret(ctx, "GITHUB_TOKEN")
				return secretErr
			})
			if err != nil {
				clear(token)
				return ctx, nil, fmt.Errorf("private package requires GITHUB_TOKEN: %w", err)
			}
		}
		if len(token) == 0 {
			return ctx, nil, errors.New("GITHUB_TOKEN must not be empty")
		}
	}
	// Public reads must not inherit a token from the caller's context.
	return downloads.WithGitHubToken(ctx, token), token, nil
}
