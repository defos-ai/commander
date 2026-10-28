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

func TestUnapprovedSpawnSourcesStopBeforeCredentialsOrProgress(t *testing.T) {
	for _, access := range []string{"no secret capability", "managed secret", "environment token"} {
		t.Run(access, func(t *testing.T) {
			token := ""
			if access == "environment token" {
				token = "private-environment-token"
			}
			t.Setenv("GITHUB_TOKEN", token)
			for _, visibility := range []string{"public", "private"} {
				for _, source := range []struct {
					name, reference string
					needsApproval   bool
				}{
					{"third-party owner", "github.com/acme/worker-agent@v1.2.3", true},
					{"owner prefix", "github.com/defos-ai-attacker/worker-agent@stable", true},
					{"owner suffix", "github.com/attacker-defos-ai/worker-agent", true},
					{"official repository name", "github.com/acme/defos-ai@v1.2.3", true},
					{"official repository prefix", "github.com/acme/defos-ai-worker@v1.2.3", true},
					{"official module path", "https://github.com/acme/worker-agent/defos-ai/worker@v1.2.3", true},
					{"spoofed host", "https://github.com.attacker.example/defos-ai/worker-agent", false},
					{"non-GitHub host", "https://attacker.example/defos-ai/worker-agent", false},
					{"credential host", "https://github.com@attacker.example/defos-ai/worker-agent", false},
					{"traversal module", "github.com/acme/worker-agent/../defos-ai@v1.2.3", false},
					{"GitHub tree URL", "https://github.com/acme/worker-agent/tree/defos-ai", false},
				} {
					t.Run(visibility+"/"+source.name, func(t *testing.T) {
						logs := fakes.NewProgressLogs(nil)
						ctx := action.WithExecution(action.WithLogs(t.Context(), logs), action.Execution{CommandID: id.MessageID()})
						requests := 0
						if access == "managed secret" {
							ctx = action.WithSecrets(ctx, fakes.SecretRequester(func(context.Context, string) ([]byte, error) {
								requests++
								return nil, errors.New("unapproved source requested a private token")
							}))
						}
						launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{Result: fixtures.SpawnResult()})
						executor := fixtures.SpawnAction(t, launcher)
						input, err := json.Marshal(map[string]string{"package": source.reference, "visibility": visibility})
						if err != nil {
							t.Fatal(err)
						}

						result, err := executor.Run(ctx, input)

						if err == nil || source.needsApproval && !errors.Is(err, packages.ErrSourceApprovalRequired) {
							t.Fatalf("source did not stop at its policy or validation boundary: %v", err)
						}
						if requests != 0 || len(logs.Messages()) != 0 || len(launcher.Calls()) != 0 || len(result.Output) != 0 {
							t.Fatalf("unapproved source acquired credentials, reported progress or launched: requests=%d logs=%v launches=%v", requests, logs.Messages(), launcher.Calls())
						}
						if token != "" && strings.Contains(err.Error(), token) {
							t.Fatal("source rejection exposed an environment credential")
						}
					})
				}
			}
		})
	}
}
