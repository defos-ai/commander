package playground_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/playground/internal/local"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/framework/domain/operations"
)

const builderFixtureRevision = "0123456789abcdef0123456789abcdef01234567"

func testAllInOneBuilderWorkspace(t *testing.T) {
	temporary := allInOneEnvironment(t)
	installBuilderGitFixture(t)
	external := t.TempDir()
	sentinel := filepath.Join(external, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("existing workspace"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUILDER_WORKSPACE_DIR", external)
	session, stop := startLocalAdminSession(t, local.Options{DisableAdmin: true})
	client, err := bridge.Connect(session.Context, session.MCPURL, session.MCPCredentials)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	call := func(name string, arguments map[string]any, target any) {
		t.Helper()
		encoded, err := json.Marshal(arguments)
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.Call(session.Context, name, encoded)
		if err != nil || result.IsError {
			t.Fatalf("MCP %s: %v (%s)", name, err, result.Text)
		}
		if target != nil {
			if err := json.Unmarshal(result.Data, target); err != nil {
				t.Fatalf("decode %s: %v", name, err)
			}
		}
	}
	var inventory struct {
		Agents []models.AgentResponse `json:"agents"`
	}
	call("list_agents", map[string]any{}, &inventory)
	var builderID string
	for _, agent := range inventory.Agents {
		if agent.Type == "builder-agent" {
			if agent.Status != "ready" || len(agent.Actions) != 1 || agent.Actions[0].Name != "checkout-template" || agent.Actions[0].Status != "ready" {
				t.Fatalf("builder did not discover Git: %+v", agent)
			}
			builderID = agent.ID
		}
	}
	if builderID == "" || len(inventory.Agents) != 3 {
		t.Fatal("all-in-one returned before every agent registered")
	}
	var operation operations.Operation
	call("start_operation", map[string]any{"mission_name": "review-website-no-browser", "input": map[string]any{"url": "https://example.test"}}, &operation)
	var task operations.Task
	call("assign_task", map[string]any{"operation_id": operation.ID, "action_name": "checkout-template", "input": map[string]any{}}, &task)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for task.Status != operations.TaskSucceeded {
		if task.Status == operations.TaskFailed || task.Status == operations.TaskInterrupted {
			t.Fatalf("builder checkout did not succeed: %+v", task)
		}
		select {
		case <-session.Context.Done():
			t.Fatalf("waiting for builder checkout: %+v", task)
		case <-tick.C:
		}
		call("get_task", map[string]any{"operation_id": operation.ID, "task_id": task.ID}, &task)
	}
	var checkout struct {
		WorkspaceID      string `json:"workspace_id"`
		TemplateRevision string `json:"template_revision"`
	}
	if task.AgentID != builderID || task.Result == nil || json.Unmarshal(task.Result.Output, &checkout) != nil || checkout.TemplateRevision != builderFixtureRevision || !strings.HasPrefix(checkout.WorkspaceID, "checkout-") || filepath.Base(checkout.WorkspaceID) != checkout.WorkspaceID {
		t.Fatalf("controller did not save builder checkout evidence: %+v", task)
	}
	workspace := filepath.Join(session.Directory, "builder-workspaces", checkout.WorkspaceID)
	if data, err := os.ReadFile(filepath.Join(workspace, "source", "fixture.txt")); err != nil || string(data) != "local checkout fixture" {
		t.Fatalf("checkout did not use the session workspace: %v", err)
	}
	call("cancel_operation", map[string]any{"operation_id": operation.ID}, nil)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	stop()
	if _, err := os.Stat(session.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("all-in-one retained its builder workspace after closing")
	}
	if entries, err := os.ReadDir(temporary); err != nil || len(entries) != 0 {
		t.Fatalf("session state remains after shutdown: %v (%v)", entries, err)
	}
	if entries, err := os.ReadDir(external); err != nil || len(entries) != 1 || entries[0].Name() != "keep.txt" {
		t.Fatalf("all-in-one modified the configured external builder workspace: %v (%v)", entries, err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "existing workspace" {
		t.Fatal("all-in-one changed existing workspace data")
	}
	assertLocalHostStopped(t, session.APIURL)
	assertLocalHostStopped(t, session.MCPURL)
}

// Use a discovered executable as the fixture boundary, so the real builder host
// runs its checkout lifecycle without a public clone or production test hooks.
func TestMain(m *testing.M) {
	name := filepath.Base(os.Args[0])
	if (name == "git" || name == "git.exe") && len(os.Args) > 1 && os.Args[1] == "-c" {
		if err := runBuilderGitFixture(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runBuilderGitFixture() error {
	args := os.Args[1:]
	for len(args) >= 2 && args[0] == "-c" {
		args = args[2:]
	}
	if len(args) == 0 {
		return errors.New("missing Git fixture command")
	}
	switch args[0] {
	case "clone":
		if len(args) < 3 || args[len(args)-2] != "https://github.com/defos-ai/commander-example-agent.git" {
			return errors.New("unexpected template URL")
		}
		destination := args[len(args)-1]
		if err := os.Mkdir(destination, 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destination, "fixture.txt"), []byte("local checkout fixture"), 0600)
	case "rev-parse":
		fmt.Println(builderFixtureRevision)
		return nil
	default:
		return errors.New("unexpected Git fixture command")
	}
}

func installBuilderGitFixture(t *testing.T) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	executable := filepath.Join(bin, name)
	// Windows locks the running test image; copying permits fixture cleanup.
	if runtime.GOOS == "windows" || os.Link(self, executable) != nil {
		input, err := os.Open(self)
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		output, err := os.OpenFile(executable, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(output, input)
		if err := errors.Join(copyErr, output.Close()); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}
