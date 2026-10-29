package builder_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/action"
)

func TestPublishAgentPublishesVerifiedSourceAndRetriesSameCommit(t *testing.T) {
	for _, scenario := range []struct{ name, defaultVisibility, visibility, want string }{
		{name: "explicit public", visibility: "public", want: "public"},
		{name: "configured private", defaultVisibility: "private", want: "private"},
		{name: "explicit public overrides private default", defaultVisibility: "private", visibility: "public", want: "public"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, key := range []string{"GITHUB_TOKEN", "OPENAI_API_KEY", "CODEX_API_KEY", "SECRETS_ENCRYPTION_KEY", "API_PASSWORD"} {
				t.Setenv(key, "ambient-secret-must-not-reach-publication")
			}
			f := fixtures.NewPublication(t, scenario.defaultVisibility, "")
			f.Input.Visibility = scenario.visibility

			result, err := f.Run(t)

			if err != nil {
				t.Fatal(err)
			}
			var output builderactions.PublishAgentOutput
			if err := json.Unmarshal(result.Output, &output); err != nil {
				t.Fatal(err)
			}
			sha := strings.TrimSpace(fixtures.Git(t, "--git-dir="+f.Git.Bare, "rev-parse", "refs/heads/main"))
			if output.CommitSHA != sha || output.Branch != "main" || output.Visibility != scenario.want || output.RepositoryURL != "https://github.com/"+fixtures.PublicationRepository || output.CommitURL != output.RepositoryURL+"/commit/"+sha {
				t.Fatalf("publication output does not identify the saved commit: %+v", output)
			}
			if len(result.Artifacts) != 0 || len(result.NextActions) != 0 {
				t.Fatal("publication must only return its repository and commit")
			}
			readme := fixtures.Git(t, "--git-dir="+f.Git.Bare, "show", sha+":README.md")
			if !strings.Contains(readme, "# Built agent") || strings.Count(readme, "Built with [Commander](https://github.com/defos-ai/commander).") != 1 {
				t.Fatal("publication lost source README or attribution")
			}
			if got := fixtures.Git(t, "--git-dir="+f.Git.Bare, "show", sha+":cmd/main.go"); got != "package main\nfunc main() {}\n" {
				t.Fatal("published source differs from artifact")
			}
			stored, err := f.Files.Get(t.Context(), f.Input.SourceArtifactID, f.Input.SourceFile)
			if err != nil {
				t.Fatal(err)
			}
			if readBuildArchive(t, stored)["README.md"] != "# Built agent\n" {
				t.Fatal("publication mutated its source artifact")
			}

			retried, err := f.Run(t)

			if err != nil || !bytes.Equal(retried.Output, result.Output) {
				t.Fatalf("retry did not resolve the same commit: %s %v", retried.Output, err)
			}
			if f.HTTP.CreateCount() != 1 || publicationCalls(t, f, "push") != 1 {
				t.Fatal("retry recreated or republished the repository")
			}
			if count := strings.TrimSpace(fixtures.Git(t, "--git-dir="+f.Git.Bare, "rev-list", "--count", "main")); count != "1" {
				t.Fatalf("retry duplicated history: %s", count)
			}
			checkpoints, _ := json.Marshal(f.Checkpoints.Snapshot())
			if bytes.Contains(checkpoints, []byte(fixtures.PublicationToken)) || bytes.Contains(result.Output, []byte(fixtures.PublicationToken)) || bytes.Contains(checkpoints, []byte(f.Git.Bare)) {
				t.Fatal("publication exposed token or host paths")
			}
		})
	}
}

func TestPublishAgentReconcilesUncertainPublication(t *testing.T) {
	t.Run("saved intent acknowledgement lost", func(t *testing.T) {
		f := fixtures.NewPublication(t, "", "")
		f.Checkpoints.SetAfterSave(func(name string) error {
			if name == "publication-intent" {
				return errors.New("checkpoint acknowledgement lost")
			}
			return nil
		})
		if _, err := f.Run(t); err == nil {
			t.Fatal("unconfirmed intent allowed publication")
		}
		if f.HTTP.RequestCount() != 0 || publicationCalls(t, f, "push") != 0 {
			t.Fatal("publication contacted GitHub before saving its intent")
		}
		for _, change := range []string{"repository", "source", "visibility"} {
			t.Run(change, func(t *testing.T) {
				original := f.Input
				defer func() { f.Input = original }()
				switch change {
				case "repository":
					f.Input.Repository = "fixture/other-agent"
				case "source":
					f.Source(t, []fixtures.ArchiveEntry{{Name: "README.md", Text: "Different source\n"}})
				case "visibility":
					f.Input.Visibility = "private"
				}
				if _, err := f.Run(t); err == nil || !strings.Contains(err.Error(), "conflicts") {
					t.Fatalf("retry accepted changed %s: %v", change, err)
				}
				if f.HTTP.RequestCount() != 0 {
					t.Fatal("conflicting retry contacted GitHub")
				}
			})
		}
		f.Checkpoints.SetAfterSave(nil)
		if _, err := f.Run(t); err != nil {
			t.Fatal(err)
		}
		if f.HTTP.CreateCount() != 1 || publicationCalls(t, f, "push") != 1 {
			t.Fatal("saved intent did not safely resume publication")
		}
	})
	t.Run("accepted push acknowledgement lost", func(t *testing.T) {
		f := fixtures.NewPublication(t, "", "lost-push-ack")

		first, err := f.Run(t)

		if err != nil {
			t.Fatalf("confirmed remote commit should resolve lost push acknowledgement: %v", err)
		}

		second, err := f.Run(t)

		if err != nil || !bytes.Equal(first.Output, second.Output) || publicationCalls(t, f, "push") != 1 {
			t.Fatalf("retry repeated accepted push: %v", err)
		}
	})
	for _, state := range []string{"deleted", "replaced", "visibility changed", "unrelated history"} {
		t.Run(state, func(t *testing.T) {
			f := fixtures.NewPublication(t, "", "")
			f.Checkpoints.SetAfterSave(func(name string) error {
				if name == "publication-repository" {
					return errors.New("repository checkpoint acknowledgement lost")
				}
				return nil
			})
			if _, err := f.Run(t); err == nil || f.HTTP.CreateCount() != 1 || publicationCalls(t, f, "push") != 0 {
				t.Fatalf("repository checkpoint did not guard push: %v", err)
			}
			f.Checkpoints.SetAfterSave(nil)
			switch state {
			case "deleted":
				f.HTTP.Change(func(state *fakes.PublicationState) {
					state.Exists = false
				})
			case "replaced":
				f.HTTP.Change(func(state *fakes.PublicationState) { state.ID++ })
			case "visibility changed":
				f.HTTP.Change(func(state *fakes.PublicationState) {
					state.Private = true
				})
			case "unrelated history":
				source, _ := fixtures.Template(t)
				fixtures.Git(t, "-C", source, "push", f.Git.Bare, "HEAD:refs/heads/main")
			}
			if _, err := f.Run(t); err == nil {
				t.Fatal("retry accepted a conflicting remote repository")
			}
			if f.HTTP.CreateCount() != 1 || publicationCalls(t, f, "push") != 0 {
				t.Fatal("retry recreated or overwrote conflicting remote state")
			}
		})
	}
}

func TestPublishAgentRejectsUnsafeInputAndProtectsManagedSecrets(t *testing.T) {
	for _, scenario := range []string{"missing managed token", "missing visibility", "invalid repository", "altered checksum", "path traversal", "symlink", "credential file", "token content", "token filename", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			f := fixtures.NewPublication(t, "", "")
			t.Setenv("GITHUB_TOKEN", "ambient-token-is-not-a-managed-secret")
			switch scenario {
			case "missing managed token":
				f.Ctx = action.WithSecrets(f.Ctx, fakes.PublicationSecrets(""))
			case "missing visibility":
				f.Input.Visibility = ""
			case "invalid repository":
				f.Input.Repository = "https://github.com/fixture/built-agent"
			case "altered checksum":
				f.Input.SourceFile.SHA256 = strings.Repeat("0", 64)
			case "path traversal":
				f.Source(t, []fixtures.ArchiveEntry{{Name: "../escape", Text: "source"}})
			case "symlink":
				f.Source(t, []fixtures.ArchiveEntry{{Name: "linked", Kind: tar.TypeSymlink}})
			case "credential file":
				f.Source(t, []fixtures.ArchiveEntry{{Name: ".env", Text: "secret=value"}})
			case "token content":
				f.Source(t, []fixtures.ArchiveEntry{{Name: "README.md", Text: fixtures.PublicationToken}})
			case "token filename":
				f.Source(t, []fixtures.ArchiveEntry{{Name: fixtures.PublicationToken, Text: "source"}})
			case "cancelled":
				ctx, cancel := context.WithCancel(f.Ctx)
				cancel()
				f.Ctx = ctx
			}

			result, err := f.Run(t)

			if err == nil {
				t.Fatal("unsafe publication succeeded")
			}
			if len(result.Output) != 0 || strings.Contains(err.Error(), fixtures.PublicationToken) {
				t.Fatal("failed publication leaked output or managed token")
			}
			if f.HTTP.RequestCount() != 0 || publicationCalls(t, f, "push") != 0 {
				t.Fatal("invalid input reached GitHub")
			}
		})
	}
	t.Run("cancel running Git", func(t *testing.T) {
		f := fixtures.NewPublication(t, "", "wait")
		ctx, cancel := context.WithCancel(f.Ctx)
		f.Ctx = ctx
		data, err := json.Marshal(f.Input)
		if err != nil {
			t.Fatal(err)
		}
		finished := make(chan error, 1)
		go func() { _, err := f.Action.Run(ctx, data); finished <- err }()
		stopped := false
		t.Cleanup(func() {
			cancel()
			if !stopped {
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Error("publication did not stop during cleanup")
				}
			}
		})
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			if _, err := os.Stat(f.Git.Started); err == nil {
				break
			}
			select {
			case err := <-finished:
				stopped = true
				t.Fatalf("publication stopped before cancellation: %v", err)
			case <-deadline.C:
				t.Fatal("publication Git did not start")
			case <-tick.C:
			}
		}
		cancel()
		select {
		case err := <-finished:
			stopped = true
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("publication lost cancellation: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("publication child did not stop")
		}
		if publicationCalls(t, f, "push") != 0 {
			t.Fatal("cancelled publication pushed source")
		}
	})
}

func publicationCalls(t *testing.T, f *fixtures.Publication, command string) int {
	t.Helper()
	data, err := os.ReadFile(f.Git.Calls)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var call fakes.PublicationGitCall
		if err := json.Unmarshal(line, &call); err != nil {
			t.Fatal(err)
		}
		if call.Command == command {
			n++
		}
		if _, err := os.Stat(call.Home); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("publication retained temporary Git home")
		}
	}
	return n
}
