package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fixtures"
	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

func TestMain(m *testing.M) {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if name == "git" {
		syscall.Exit(fakes.RunGit())
	}
	if name == "codex" || name == "go" {
		syscall.Exit(fakes.RunBuildTool())
	}
	os.Exit(m.Run())
}

func TestE2EBuilderWorkspaceRoutesAcrossOperationsAndRetainsSource(t *testing.T) {
	s := fixtures.NewWorkspaces(t)
	owner := s.Builder(t, "00000001", "", "fail")
	first := s.Start(t)
	created, checkout := s.Checkout(t, first, first)
	s.Builder(t, "00000002", "", "fail")
	second := s.Start(t)
	input := fixtures.WorkspaceBuildInput(checkout)

	build := s.Assign(t, second, builderactions.BuildAgentName, input)
	failed := s.Finished(t, second, build.ID)
	reuse := s.Assign(t, second, builderactions.CheckoutTemplateName, builderactions.CheckoutTemplateInput{SessionScope: checkout.SessionScope, SessionID: checkout.SessionID})
	reused := s.Finished(t, second, reuse.ID)

	if failed.AgentID != owner.ID || failed.Status != operations.TaskFailed || !strings.Contains(failed.Error, "exit status 9") {
		t.Fatalf("build was not attempted on the workspace owner: %+v", failed)
	}
	if reused.AgentID != owner.ID || reused.Status != operations.TaskSucceeded || reused.Result == nil {
		t.Fatalf("checkout reuse changed the workspace owner: %+v", reused)
	}
	var retained builderactions.CheckoutTemplateOutput
	if err := json.Unmarshal(reused.Result.Output, &retained); err != nil {
		t.Fatal(err)
	}
	if retained != checkout {
		t.Fatalf("explicit reuse replaced the checkout: %+v != %+v", retained, checkout)
	}
	source := filepath.Join(owner.Directory, checkout.WorkspaceID, "source")
	if _, err := os.Stat(filepath.Join(source, "generated.txt")); err != nil {
		t.Fatalf("failed build lost its partial source: %v", err)
	}
	closed := s.CloseSession(t, created.Result.Session.Reference)
	if closed.State != "closed" || closed.ReleaseReason != "closed" {
		t.Fatalf("workspace close was not confirmed: %+v", closed)
	}
	if _, err := os.Stat(filepath.Join(source, "README.md")); err != nil {
		t.Fatalf("close deleted successful checkout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(source, "generated.txt")); err != nil {
		t.Fatalf("close deleted generated source: %v", err)
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Controller.Operations().Assign(s.Context, commands.AssignTaskCommand{OperationID: second, ActionName: builderactions.BuildAgentName, Input: data})
	if !errors.Is(err, sessions.ErrSessionClosed) {
		t.Fatalf("closed workspace accepted more work: %v", err)
	}
}

func TestE2EBuilderSuccessfulBuildPublishesItsSessionSourceArtifact(t *testing.T) {
	s := fixtures.NewSuccessfulBuild(t)
	owner := s.Builder(t, "00000001", "", "success")
	operation := s.Start(t)
	created, checkout := s.Checkout(t, operation, operation)

	task := s.Assign(t, operation, builderactions.BuildAgentName, fixtures.WorkspaceBuildInput(checkout))
	finished := s.Finished(t, operation, task.ID)

	if finished.Status != operations.TaskSucceeded || finished.AgentID != owner.ID || finished.Result == nil {
		t.Fatalf("successful build did not retain its owner/output: %+v", finished)
	}
	if created.SessionIntent == nil || created.SessionIntent.Mode != sessions.Open || finished.SessionIntent == nil ||
		finished.SessionIntent.Mode != sessions.Use || finished.SessionIntent.ID != created.ID || finished.SessionIntent.ScopeID != created.SessionIntent.ScopeID {
		t.Fatalf("build did not use the saved checkout binding: checkout=%+v build=%+v", created.SessionIntent, finished.SessionIntent)
	}
	var output builderactions.BuildAgentOutput
	if err := json.Unmarshal(finished.Result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.SessionScope != checkout.SessionScope || output.SessionID != checkout.SessionID || output.WorkspaceID != checkout.WorkspaceID ||
		output.AgentType != "workspace-demo-agent" || output.ModulePath != "example.invalid/workspace-demo-agent" || output.Binary != "commander-workspace-demo-agent" || output.SourceFiles == 0 {
		t.Fatalf("built package changed session or generated identity: %+v", output)
	}
	if len(finished.Result.Artifacts) != 1 || len(finished.Result.Artifacts[0].Files) != 1 {
		t.Fatalf("accepted source artifact missing: %+v", finished.Result.Artifacts)
	}
	artifact := finished.Result.Artifacts[0]
	file := artifact.Files[0]
	if file.Name != "source.tar.gz" {
		t.Fatalf("unexpected source attachment: %+v", file)
	}
	data := s.ArtifactFile(t, artifact, file)
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	files := make(map[string][]byte)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(archive)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = contents
		if strings.HasPrefix(header.Name, ".build-") || strings.HasPrefix(header.Name, ".git/") || bytes.Contains(contents, []byte("workspace-model-key")) {
			t.Fatalf("source archive included temporary or sensitive data: %q", header.Name)
		}
	}
	if len(files) != output.SourceFiles || !bytes.Contains(files["agent.go"], []byte(`"workspace-demo-agent"`)) || len(files["cmd/main.go"]) == 0 || len(files[".github/scripts/release.go"]) == 0 {
		t.Fatalf("source archive did not retain the verified agent: count=%d output=%+v", len(files), output)
	}
}

func TestE2EBuilderCloseCancelsActiveWorkspaceWork(t *testing.T) {
	for _, stage := range []string{"checkout", "build"} {
		t.Run(stage, func(t *testing.T) {
			s := fixtures.NewWorkspaces(t)
			checkoutMode, buildMode := "wait", "fail"
			if stage == "build" {
				checkoutMode, buildMode = "", "wait"
			}
			owner := s.Builder(t, "00000001", checkoutMode, buildMode)
			operation := s.Start(t)
			var task, creation operations.Task
			var output builderactions.CheckoutTemplateOutput
			if stage == "checkout" {
				task = s.Assign(t, operation, builderactions.CheckoutTemplateName, builderactions.CheckoutTemplateInput{SessionScope: operation})
				creation = task
			} else {
				creation, output = s.Checkout(t, operation, operation)
				task = s.Assign(t, operation, builderactions.BuildAgentName, fixtures.WorkspaceBuildInput(output))
			}
			s.AwaitStarted(t, owner)
			observations := owner.Provider.Snapshot()
			if len(observations) != 1 || !observations[0].Busy {
				t.Fatalf("workspace work was not observed: %+v", observations)
			}

			closed := s.CloseSession(t, observations[0].Reference)
			finished := s.Finished(t, operation, task.ID)

			if closed.State != "closed" || closed.Busy || finished.Status != operations.TaskFailed {
				t.Fatalf("close did not stop workspace work: session=%+v task=%+v", closed, finished)
			}
			claim := sessions.CreationClaim{Reference: closed.Reference,
				OperationID: operation, TaskID: creation.ID, CommandID: creation.ID}
			if err := owner.Provider.Prepare(s.Context, claim); !errors.Is(err, sessions.ErrSessionClosed) {
				t.Fatalf("closed creation could start again: %v", err)
			}
			paths, err := filepath.Glob(filepath.Join(owner.Directory, "checkout-*"))
			if err != nil {
				t.Fatal(err)
			}
			if stage == "checkout" && len(paths) != 0 {
				t.Fatalf("incomplete checkout was retained: %v", paths)
			}
			if stage == "build" {
				if _, err := os.Stat(filepath.Join(owner.Directory, output.WorkspaceID, "source", "generated.txt")); err != nil {
					t.Fatalf("interrupted build source was deleted: %v", err)
				}
			}
		})
	}
}

func TestE2EBuilderRetainsAnExactCloseBeforePreparation(t *testing.T) {
	s := fixtures.NewWorkspaces(t)
	owner := s.Builder(t, "00000001", "", "fail")
	taskID := id.New()
	claim := sessions.CreationClaim{Reference: sessions.Reference{ID: id.New(), ScopeID: id.New(), OwnerAgentID: owner.ID},
		OperationID: id.New(), TaskID: taskID, CommandID: taskID}

	closed, err := owner.Provider.Control(s.Context, sessions.Control{Reference: claim.Reference, Method: "close",
		OperationID: claim.OperationID, TaskID: claim.TaskID, CommandID: claim.CommandID})
	prepared := owner.Provider.Prepare(s.Context, claim)
	foreign := claim
	foreign.OwnerAgentID = "builder-agent-00000002"
	_, foreignErr := owner.Provider.Control(s.Context, sessions.Control{Reference: foreign.Reference, Method: "close",
		OperationID: foreign.OperationID, TaskID: foreign.TaskID, CommandID: foreign.CommandID})

	if err != nil || closed.State != "closed" || !errors.Is(prepared, sessions.ErrSessionClosed) || !errors.Is(foreignErr, sessions.ErrConflict) {
		t.Fatalf("close tombstone lost its exact claim: closed=%+v close=%v prepare=%v foreign=%v", closed, err, prepared, foreignErr)
	}
	paths, err := filepath.Glob(filepath.Join(owner.Directory, "checkout-*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("preparation opened a resource: %v %v", paths, err)
	}
}
