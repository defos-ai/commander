package packages_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/id"
)

func TestPrivateAccessProgressPreservesSecretScopeAndFailureRedaction(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	for _, failure := range []bool{false, true} {
		name := "delivered"
		if failure {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			const token = "private-progress-token"
			provided := []byte(token)
			logs := fakes.NewProgressLogs(errors.New("private progress backend diagnostic"))
			ctx := action.WithExecution(action.WithLogs(t.Context(), logs), action.Execution{CommandID: id.MessageID()})
			requests, launches := 0, 0
			ctx = action.WithSecrets(ctx, fakes.SecretRequester(func(_ context.Context, key string) ([]byte, error) {
				requests++
				if key != "GITHUB_TOKEN" {
					t.Errorf("progress changed the requested secret: %s", key)
				}
				if failure {
					return provided, errors.New("untrusted secret provider diagnostic: " + token)
				}
				return provided, nil
			}))
			var received []byte
			executor := fixtures.SpawnAction(t, fakes.LauncherFunc(func(ctx context.Context, _, _ string) (runtimes.Result, error) {
				launches++
				received = packages.GitHubToken(ctx)
				if string(received) != token {
					t.Error("progress changed the private launch credential scope")
				}
				return fixtures.SpawnResult(), nil
			}))

			result, err := executor.Run(ctx, json.RawMessage(`{"package":"`+fixtures.ActionPackage+`","visibility":"private"}`))

			ending, wantLaunches := "Private package access completed", 1
			if failure {
				ending, wantLaunches = "Private package access ended with error", 0
			}
			if (err != nil) != failure || requests != 1 || launches != wantLaunches ||
				!reflect.DeepEqual(logs.Messages(), []string{"Private package access started", ending}) {
				t.Fatalf("progress changed secret acquisition or launch effects: requests=%d launches=%d error=%v messages=%v", requests, launches, err, logs.Messages())
			}
			if !bytes.Equal(provided, make([]byte, len(provided))) || !bytes.Equal(received, make([]byte, len(received))) {
				t.Fatal("private access progress retained temporary credential bytes")
			}
			if bytes.Contains(result.Output, []byte(token)) || err != nil && strings.Contains(err.Error(), token) {
				t.Fatal("private access progress exposed a secret in the result or failure")
			}
		})
	}
}
