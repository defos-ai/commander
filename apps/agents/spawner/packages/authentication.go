package packages

import "context"

type githubTokenKey struct{}

// WithGitHubToken attaches credentials for GitHub downloads and GHCR image pulls.
// The caller owns the bytes and must keep them intact until spawning finishes,
// then clear them.
func WithGitHubToken(ctx context.Context, token []byte) context.Context {
	return context.WithValue(ctx, githubTokenKey{}, token)
}

// GitHubToken returns the caller-owned token without retaining a separate copy.
// It must never be persisted or forwarded to a spawned agent.
func GitHubToken(ctx context.Context) []byte {
	token, _ := ctx.Value(githubTokenKey{}).([]byte)
	return token
}
