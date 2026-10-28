package builder_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
)

func TestCheckoutIgnoresAmbientGitConfiguration(t *testing.T) {
	source, revision := fixtures.Template(t)
	parent := t.TempDir()
	fixtures.Git(t, "init", "--quiet", parent)
	fixtures.Git(t, "-C", parent, "config", "url.https://unexpected.invalid/.insteadOf", source)
	root := filepath.Join(parent, "workspaces")
	global := filepath.Join(t.TempDir(), "gitconfig")
	fixtures.Git(t, "config", "--file", global, "url.https://unexpected.invalid/.insteadOf", source)
	fixtures.Git(t, "config", "--file", global, "credential.helper", "!exit 97")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.https://unexpected.invalid/.insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", source)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "not-a-git-repository"))
	t.Setenv("GIT_WORK_TREE", filepath.Join(t.TempDir(), "not-the-workspace"))
	t.Setenv("GIT_ALLOW_PROTOCOL", "ssh")
	t.Setenv("GIT_ASKPASS", filepath.Join(t.TempDir(), "should-not-execute"))
	output := runCheckout(t, fixtures.LocalCheckout(t, root, source))
	if output.TemplateRevision != revision {
		t.Fatalf("ambient Git settings changed checkout: %+v", output)
	}
}

func TestCheckoutDoesNotRunHostTemplateHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture hook is a POSIX shell script")
	}
	source, _ := fixtures.Template(t)
	template := t.TempDir()
	hooks := filepath.Join(template, "hooks")
	if err := os.Mkdir(hooks, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "hook-executed")
	hook := "#!/bin/sh\nprintf executed > " + fixtures.ShellQuote(marker) + "\n"
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte(hook), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_TEMPLATE_DIR", template)
	root := t.TempDir()
	output := runCheckout(t, fixtures.LocalCheckout(t, root, source))
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("checkout executed an inherited hook: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, output.WorkspaceID, "source", ".git", "hooks", "post-checkout")); !os.IsNotExist(err) {
		t.Fatalf("checkout copied the inherited hook into its workspace: %v", err)
	}
}

func TestCheckoutCancellationRemovesPartialWorkspace(t *testing.T) {
	root := t.TempDir()
	started := filepath.Join(t.TempDir(), "started")
	executor := fixtures.ConfiguredCheckout(t, root, fakes.GitConfig{Mode: "wait", Started: started})
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan error, 1)
	stopped := false
	defer func() {
		cancel()
		if stopped {
			return
		}
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("checkout did not settle during test cleanup")
		}
	}()
	go func() {
		_, err := executor.Run(ctx, json.RawMessage(`{}`))
		finished <- err
	}()
	// Cancel only once the process has started, so this exercises cleanup of a
	// partially created checkout rather than the pre-execution context check.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case err := <-finished:
			stopped = true
			t.Fatalf("checkout exited before its process started: %v", err)
		case <-deadline.C:
			t.Fatal("checkout process did not start")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case err := <-finished:
		stopped = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled checkout returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled checkout did not stop")
	}
	assertEmptyWorkspaceRoot(t, root)
}

func TestCheckoutRechecksRemovedGit(t *testing.T) {
	root := t.TempDir()
	executor := fixtures.LocalCheckout(t, root, "")
	executable, err := (&environment.Local{}).FindExecutable(t.Context(), "git", executor.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Run(t.Context(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("checkout succeeded after the Git executable was removed")
	}
	if executor.Action().Status != action.Unavailable {
		t.Fatal("removed Git executable did not invalidate readiness")
	}
	assertEmptyWorkspaceRoot(t, root)
}

func TestGitSubprocessProtectsCredentialsAndDiagnostics(t *testing.T) {
	source, revision := fixtures.Template(t)
	t.Setenv("GITHUB_TOKEN", "fixture-github-token")
	t.Setenv("SECRETS_ENCRYPTION_KEY", "fixture-encryption-key")
	for _, test := range []struct {
		name    string
		mode    string
		wantErr string
	}{
		{
			name: "host secrets are not inherited",
			mode: "check-secrets",
		},
		{
			name:    "raw diagnostics are not returned",
			mode:    "fail",
			wantErr: "Git exited with status 7",
		},
		{
			name:    "excess output is bounded",
			mode:    "overflow",
			wantErr: "Git output exceeded its limit",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			executor := fixtures.ConfiguredCheckout(t, root, fakes.GitConfig{Source: source, Mode: test.mode})
			if test.wantErr == "" {
				if output := runCheckout(t, executor); output.TemplateRevision != revision {
					t.Fatal("checkout with isolated credentials returned the wrong revision")
				}
				return
			}

			result, err := executor.Run(t.Context(), json.RawMessage(`{}`))

			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("checkout error = %v, want %q", err, test.wantErr)
			}
			if len(result.Output) != 0 || strings.Contains(err.Error(), "untrusted-sensitive-diagnostic") {
				t.Fatal("subprocess error exposed raw diagnostic contents")
			}
			assertEmptyWorkspaceRoot(t, root)
		})
	}
}
