package packages_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
)

func TestSpawnAgentScopesEnvironmentTokenToPrivatePackages(t *testing.T) {
	const token = "environment-github-token"
	t.Setenv("GITHUB_TOKEN", token)
	launchFailure := errors.New("launch failed")
	for _, tc := range []struct {
		name, visibility string
		private          bool
		failure          error
	}{
		{name: "omitted visibility"},
		{name: "public", visibility: `,"visibility":"public"`},
		{name: "private", visibility: `,"visibility":"private"`, private: true},
		{name: "private launch failure", visibility: `,"visibility":"private"`, private: true, failure: launchFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inherited := []byte("inherited-token")
			ctx := packages.WithGitHubToken(t.Context(), inherited)
			ctx = action.WithExecution(ctx, action.Execution{CommandID: id.MessageID()})
			requests := 0
			ctx = action.WithSecrets(ctx, spawnSecretRequester(func(context.Context, string) ([]byte, error) {
				requests++
				return nil, errors.New("unexpected managed secret request")
			}))
			var received []byte
			calls := 0
			executor := newSpawnAction(t, secretActionLauncher(func(ctx context.Context, _, _ string) (runtimes.Result, error) {
				calls++
				received = packages.GitHubToken(ctx)
				if tc.private && string(received) != token || !tc.private && len(received) != 0 {
					t.Error("launch received the wrong credential scope")
				}
				return secretSpawnResult(), tc.failure
			}))
			result, err := executor.Run(ctx, json.RawMessage(`{"package":"`+actionPackage+`"`+tc.visibility+`}`))
			if !errors.Is(err, tc.failure) || calls != 1 || requests != 0 {
				t.Fatalf("unexpected launch outcome: calls=%d requests=%d error=%v", calls, requests, err)
			}
			if !bytes.Equal(received, make([]byte, len(received))) {
				t.Fatal("private launch retained its temporary token")
			}
			if string(inherited) != "inherited-token" || os.Getenv("GITHUB_TOKEN") != token {
				t.Fatal("spawn modified caller-owned context or environment credentials")
			}
			if bytes.Contains(result.Output, []byte(token)) {
				t.Fatal("spawn output exposed its token")
			}
		})
	}
}

func TestSpawnAgentWaitsForManagedSecretInTheSameExecution(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	const privateURL = "https://github.com/defos-ai/commander-example-agent"
	for _, cancelWaiting := range []bool{false, true} {
		name := "delivered"
		if cancelWaiting {
			name = "canceled"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			work := messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID(), StepID: "install"}
			commandID := id.MessageID()
			ctx = action.WithExecution(ctx, action.Execution{CommandID: commandID, Work: work})
			requested := make(chan string, 1)
			deliver := make(chan struct{})
			provided := []byte("managed-github-token")
			ctx = action.WithSecrets(ctx, spawnSecretRequester(func(ctx context.Context, name string) ([]byte, error) {
				requested <- name
				select {
				case <-deliver:
					return provided, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}))
			launched := make(chan spawnInvocation, 1)
			var received []byte
			executor := newSpawnAction(t, secretActionLauncher(func(ctx context.Context, requestID, reference string) (runtimes.Result, error) {
				received = packages.GitHubToken(ctx)
				if string(received) != "managed-github-token" {
					t.Error("launch did not receive the delivered token")
				}
				launched <- spawnInvocation{requestID: requestID, reference: reference}
				return secretSpawnResult(), nil
			}))
			outcome := make(chan error, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				result, err := executor.Run(ctx, json.RawMessage(`{"package":"`+privateURL+`","visibility":"private"}`))
				if bytes.Contains(result.Output, []byte("managed-github-token")) {
					t.Error("spawn output exposed the managed token")
				}
				outcome <- err
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Error("secret-waiting action did not stop")
				}
			})
			select {
			case secret := <-requested:
				if secret != "GITHUB_TOKEN" {
					t.Fatal("spawn requested the wrong secret name")
				}
			case <-ctx.Done():
				t.Fatal("spawn never requested the managed secret")
			}
			select {
			case <-launched:
				t.Fatal("spawn launched before the secret was supplied")
			default:
			}
			if cancelWaiting {
				cancel()
			} else {
				close(deliver)
			}
			var err error
			select {
			case err = <-outcome:
			case <-time.After(3 * time.Second):
				t.Fatal("spawn did not finish after secret delivery or cancellation")
			}
			if cancelWaiting {
				if !errors.Is(err, context.Canceled) || len(launched) != 0 {
					t.Fatalf("canceled secret wait launched an agent or lost cancellation: %v", err)
				}
				return
			}
			if err != nil || len(launched) != 1 || len(requested) != 0 {
				t.Fatalf("secret delivery did not finish exactly one launch: %v", err)
			}
			if call := <-launched; call != (spawnInvocation{requestID: "command:" + commandID, reference: privateURL}) {
				t.Fatalf("secret wait changed execution identity: %+v", call)
			}
			if !bytes.Equal(received, make([]byte, len(received))) || !bytes.Equal(provided, make([]byte, len(provided))) {
				t.Fatal("completed launch retained temporary managed-secret bytes")
			}
		})
	}
}

func TestSpawnAgentRejectsUnavailableOrEmptyManagedSecret(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	for _, name := range []string{"no provider", "empty secret", "provider failure"} {
		t.Run(name, func(t *testing.T) {
			ctx := action.WithExecution(t.Context(), action.Execution{CommandID: id.MessageID()})
			provided := []byte("secret-error-sentinel")
			if name != "no provider" {
				ctx = action.WithSecrets(ctx, spawnSecretRequester(func(context.Context, string) ([]byte, error) {
					if name == "empty secret" {
						return nil, nil
					}
					return provided, errors.New("provider failed: secret-error-sentinel")
				}))
			}
			launcher := &actionLauncher{}
			result, err := newSpawnAction(t, launcher).Run(ctx, json.RawMessage(`{"package":"`+actionPackage+`","visibility":"private"}`))
			if err == nil || len(launcher.calls) != 0 {
				t.Fatal("missing or empty managed secret reached the launcher")
			}
			if strings.Contains(err.Error(), "secret-error-sentinel") || bytes.Contains(result.Output, []byte("secret-error-sentinel")) {
				t.Fatal("failed secret request exposed secret bytes")
			}
			if name == "provider failure" && !bytes.Equal(provided, make([]byte, len(provided))) {
				t.Fatal("failed secret request retained returned bytes")
			}
		})
	}
}

type secretActionLauncher func(context.Context, string, string) (runtimes.Result, error)

func (f secretActionLauncher) Spawn(ctx context.Context, requestID, reference string) (runtimes.Result, error) {
	return f(ctx, requestID, reference)
}

type spawnSecretRequester func(context.Context, string) ([]byte, error)

func (f spawnSecretRequester) Secret(string) ([]byte, error) { return nil, action.ErrSecretUnavailable }
func (f spawnSecretRequester) RequestSecret(ctx context.Context, name string) ([]byte, error) {
	return f(ctx, name)
}

func secretSpawnResult() runtimes.Result {
	return runtimes.Result{AgentID: "example-agent-12345678", AgentType: "example-agent", Package: actionPackage,
		Runtime: "native", PID: 42, Actions: []actionmodel.Action{}}
}
