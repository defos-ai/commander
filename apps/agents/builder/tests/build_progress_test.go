package builder_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/action"
)

func buildProgressStages() []string {
	return []string{
		"Codex generation", "Go tests", "Go build", "Release structure validation",
		"Registered action validation", "Source archive preparation",
	}
}

func TestBuildProgressReportsStagesWithoutRepeatingWork(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"available", nil}, {"publication unavailable", errors.New("private-log-backend-diagnostic")},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, workspaceID := fixtures.BuildWorkspace(t)
			executor, config, _ := fixtures.BuildExecutor(t, root, workspaceID, "")
			logs := fakes.NewProgressLogs(test.err)
			ctx := action.WithLogs(fixtures.BuildContext(t), logs)

			result, err := executor.Run(ctx, fixtures.BuildInput(t, workspaceID))

			if err != nil || len(result.Artifacts) != 1 || len(result.NextActions) != 0 {
				t.Fatalf("progress publication changed the successful build outcome: %v", err)
			}
			assertBuildProgressMessages(t, logs.Messages(), buildProgressStages(), false, root, config.Source)
			calls, err := os.ReadFile(config.Calls)
			if err != nil || strings.Count(string(calls), "\n") != 7 || strings.Count(string(calls), "codex exec ") != 1 {
				t.Fatalf("progress publication repeated build subprocesses: %q, %v", calls, err)
			}
		})
	}
}

func TestBuildProgressStopsAtTheFailedStageWithoutLeakingDiagnostics(t *testing.T) {
	for _, test := range []struct {
		mode   string
		stages int
		calls  int
	}{
		{"fail", 1, 1}, {"test-fail", 2, 2}, {"build-fail", 3, 3},
		{"template-identity", 4, 3}, {"contracts-description", 5, 7}, {"leak", 6, 7},
	} {
		t.Run(test.mode, func(t *testing.T) {
			root, workspaceID := fixtures.BuildWorkspace(t)
			executor, config, _ := fixtures.BuildExecutor(t, root, workspaceID, test.mode)
			logs := fakes.NewProgressLogs(errors.New("private-log-backend-diagnostic"))
			ctx := action.WithLogs(fixtures.BuildContext(t), logs)

			result, err := executor.Run(ctx, fixtures.BuildInput(t, workspaceID))

			if err == nil || len(result.Artifacts) != 0 || len(result.Output) != 0 {
				t.Fatal("progress publication hid a failed build stage")
			}
			if strings.Contains(err.Error(), "private-log-backend-diagnostic") {
				t.Fatal("log publication failure replaced the build error")
			}
			assertBuildProgressMessages(t, logs.Messages(), buildProgressStages()[:test.stages], true, root, config.Source)
			calls, readErr := os.ReadFile(config.Calls)
			if readErr != nil || strings.Count(string(calls), "codex exec ") != 1 || strings.Count(string(calls), "\n") != test.calls {
				t.Fatalf("failed progress stage repeated or continued subprocesses: %q, %v", calls, readErr)
			}
		})
	}
}

func TestBuildProgressIsVisibleWhileCodexIsRunning(t *testing.T) {
	root, workspaceID := fixtures.BuildWorkspace(t)
	executor, config, _ := fixtures.BuildExecutor(t, root, workspaceID, "wait")
	logs := fakes.NewProgressLogs(nil)
	ctx, cancel := context.WithCancel(action.WithLogs(fixtures.BuildContext(t), logs))
	input := fixtures.BuildInput(t, workspaceID)
	finished := make(chan error, 1)
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Error("progress-reporting build did not stop during cleanup")
			}
		}
	})
	go func() {
		_, err := executor.Run(ctx, input)
		finished <- err
	}()

	select {
	case message := <-logs.Published():
		if message != "Codex generation started" {
			t.Fatalf("first progress message: %q", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("running build did not publish progress")
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(config.Started); err == nil {
			break
		}
		select {
		case err := <-finished:
			joined = true
			t.Fatalf("build returned before its waiting subprocess started: %v", err)
		case <-deadline.C:
			t.Fatal("Codex fixture did not start")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case err := <-finished:
		joined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("progress changed build cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled build did not join its progress worker")
	}

	if messages := logs.Messages(); !slices.Equal(messages, []string{"Codex generation started"}) {
		t.Fatalf("cancelled build reported later stages: %v", messages)
	}
	calls, err := os.ReadFile(config.Calls)
	if err != nil || strings.Count(string(calls), "\n") != 1 {
		t.Fatal("progress observation repeated or continued the cancelled build")
	}
}

func TestTemplateCheckoutProgressPreservesOutcomesAndCleanup(t *testing.T) {
	source, _ := fixtures.Template(t)
	for _, mode := range []string{"", "fail"} {
		name := "success"
		if mode != "" {
			name = mode
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			executor := fixtures.ConfiguredCheckout(t, root, fakes.GitConfig{Source: source, Mode: mode})
			logs := fakes.NewProgressLogs(errors.New("private-log-backend-diagnostic"))

			result, err := executor.Run(action.WithLogs(t.Context(), logs), json.RawMessage(`{}`))

			if (err != nil) != (mode != "") || len(result.Artifacts) != 0 || len(result.NextActions) != 0 {
				t.Fatalf("progress publication changed checkout outcome: %v", err)
			}
			assertBuildProgressMessages(t, logs.Messages(), []string{"Template checkout"}, mode != "", root, source)
			if mode != "" {
				assertEmptyWorkspaceRoot(t, root)
			}
		})
	}
}

func assertBuildProgressMessages(t *testing.T, messages, stages []string, failed bool, privateValues ...string) {
	t.Helper()
	want := make([]string, 0, len(stages)*2)
	for index, stage := range stages {
		outcome := " completed"
		if failed && index == len(stages)-1 {
			outcome = " ended with error"
		}
		want = append(want, stage+" started", stage+outcome)
	}
	if !slices.Equal(messages, want) {
		t.Fatalf("unexpected stage progress: got %v, want %v", messages, want)
	}
	privateValues = append(privateValues, fixtures.BuildSecret, fixtures.BuildInstructions, "untrusted-sensitive", "private-log-backend-diagnostic", "uploaded", "published")
	for _, message := range messages {
		for _, value := range privateValues {
			if strings.Contains(message, value) {
				t.Fatalf("progress message disclosed private data or overstated artifact availability: %q", message)
			}
		}
	}
}
