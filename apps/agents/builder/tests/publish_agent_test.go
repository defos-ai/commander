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
	"github.com/defos-ai/commander/framework/agent/action"
)

func TestPublishAgentPublishesVerifiedSourceAndRetriesSameCommit(t *testing.T) {
	for _, scenario := range []struct{ name, defaultVisibility, visibility, want string }{
		{name: "explicit public", visibility: "public", want: "public"},
		{name: "configured private", defaultVisibility: "private", want: "private"},
		{name: "explicit public overrides private default", defaultVisibility: "private", visibility: "public", want: "public"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, key := range []string{"GITHUB_TOKEN", "CODEX_API_KEY", "SECRETS_ENCRYPTION_KEY", "API_PASSWORD"} {
				t.Setenv(key, "ambient-secret-must-not-reach-publication")
			}
			f := newPublicationFixture(t, scenario.defaultVisibility, "")
			f.input.Visibility = scenario.visibility
			result, err := f.run(t)
			if err != nil {
				t.Fatal(err)
			}
			var output builderactions.PublishAgentOutput
			if err := json.Unmarshal(result.Output, &output); err != nil {
				t.Fatal(err)
			}
			sha := strings.TrimSpace(fixtureGit(t, "--git-dir="+f.git.Bare, "rev-parse", "refs/heads/main"))
			if output.CommitSHA != sha || output.Branch != "main" || output.Visibility != scenario.want || output.RepositoryURL != "https://github.com/"+publicationRepository || output.CommitURL != output.RepositoryURL+"/commit/"+sha {
				t.Fatalf("publication output does not identify the saved commit: %+v", output)
			}
			if len(result.Artifacts) != 0 || len(result.NextActions) != 0 {
				t.Fatal("publication must only return its repository and commit")
			}
			readme := fixtureGit(t, "--git-dir="+f.git.Bare, "show", sha+":README.md")
			if !strings.Contains(readme, "# Built agent") || strings.Count(readme, "Built with [Commander](https://github.com/defos-ai/commander).") != 1 {
				t.Fatal("publication lost source README or attribution")
			}
			if got := fixtureGit(t, "--git-dir="+f.git.Bare, "show", sha+":cmd/main.go"); got != "package main\nfunc main() {}\n" {
				t.Fatal("published source differs from artifact")
			}
			stored, err := f.files.Get(t.Context(), f.input.SourceArtifactID, f.input.SourceFile)
			if err != nil {
				t.Fatal(err)
			}
			if readBuildArchive(t, stored)["README.md"] != "# Built agent\n" {
				t.Fatal("publication mutated its source artifact")
			}
			retried, err := f.run(t)
			if err != nil || !bytes.Equal(retried.Output, result.Output) {
				t.Fatalf("retry did not resolve the same commit: %s %v", retried.Output, err)
			}
			if f.http.creates != 1 || f.calls(t, "push") != 1 {
				t.Fatal("retry recreated or republished the repository")
			}
			if count := strings.TrimSpace(fixtureGit(t, "--git-dir="+f.git.Bare, "rev-list", "--count", "main")); count != "1" {
				t.Fatalf("retry duplicated history: %s", count)
			}
			checkpoints, _ := json.Marshal(f.checkpoints.record.Checkpoints)
			if bytes.Contains(checkpoints, []byte(publicationToken)) || bytes.Contains(result.Output, []byte(publicationToken)) || bytes.Contains(checkpoints, []byte(f.git.Bare)) {
				t.Fatal("publication exposed token or host paths")
			}
		})
	}
}

func TestPublishAgentReconcilesUncertainPublication(t *testing.T) {
	t.Run("saved intent acknowledgement lost", func(t *testing.T) {
		f := newPublicationFixture(t, "", "")
		f.checkpoints.afterSave = func(name string) error {
			if name == "publication-intent" {
				return errors.New("checkpoint acknowledgement lost")
			}
			return nil
		}
		if _, err := f.run(t); err == nil {
			t.Fatal("unconfirmed intent allowed publication")
		}
		if f.http.requests != 0 || f.calls(t, "push") != 0 {
			t.Fatal("publication contacted GitHub before saving its intent")
		}
		for _, change := range []string{"repository", "source", "visibility"} {
			t.Run(change, func(t *testing.T) {
				original := f.input
				defer func() { f.input = original }()
				switch change {
				case "repository":
					f.input.Repository = "fixture/other-agent"
				case "source":
					f.source(t, []publicationArchiveEntry{{name: "README.md", text: "Different source\n"}})
				case "visibility":
					f.input.Visibility = "private"
				}
				if _, err := f.run(t); err == nil || !strings.Contains(err.Error(), "conflicts") {
					t.Fatalf("retry accepted changed %s: %v", change, err)
				}
				if f.http.requests != 0 {
					t.Fatal("conflicting retry contacted GitHub")
				}
			})
		}
		f.checkpoints.afterSave = nil
		if _, err := f.run(t); err != nil {
			t.Fatal(err)
		}
		if f.http.creates != 1 || f.calls(t, "push") != 1 {
			t.Fatal("saved intent did not safely resume publication")
		}
	})
	t.Run("accepted push acknowledgement lost", func(t *testing.T) {
		f := newPublicationFixture(t, "", "lost-push-ack")
		first, err := f.run(t)
		if err != nil {
			t.Fatalf("confirmed remote commit should resolve lost push acknowledgement: %v", err)
		}
		second, err := f.run(t)
		if err != nil || !bytes.Equal(first.Output, second.Output) || f.calls(t, "push") != 1 {
			t.Fatalf("retry repeated accepted push: %v", err)
		}
	})
	for _, state := range []string{"deleted", "replaced", "visibility changed", "unrelated history"} {
		t.Run(state, func(t *testing.T) {
			f := newPublicationFixture(t, "", "")
			f.checkpoints.afterSave = func(name string) error {
				if name == "publication-repository" {
					return errors.New("repository checkpoint acknowledgement lost")
				}
				return nil
			}
			if _, err := f.run(t); err == nil || f.http.creates != 1 || f.calls(t, "push") != 0 {
				t.Fatalf("repository checkpoint did not guard push: %v", err)
			}
			f.checkpoints.afterSave = nil
			switch state {
			case "deleted":
				f.http.exists = false
			case "replaced":
				f.http.id++
			case "visibility changed":
				f.http.private = true
			case "unrelated history":
				source, _ := templateFixture(t)
				fixtureGit(t, "-C", source, "push", f.git.Bare, "HEAD:refs/heads/main")
			}
			if _, err := f.run(t); err == nil {
				t.Fatal("retry accepted a conflicting remote repository")
			}
			if f.http.creates != 1 || f.calls(t, "push") != 0 {
				t.Fatal("retry recreated or overwrote conflicting remote state")
			}
		})
	}
}

func TestPublishAgentRejectsUnsafeInputAndProtectsManagedSecrets(t *testing.T) {
	for _, scenario := range []string{"missing managed token", "missing visibility", "invalid repository", "altered checksum", "path traversal", "symlink", "credential file", "token content", "token filename", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			f := newPublicationFixture(t, "", "")
			t.Setenv("GITHUB_TOKEN", "ambient-token-is-not-a-managed-secret")
			switch scenario {
			case "missing managed token":
				f.ctx = action.WithSecrets(f.ctx, publicationSecrets(""))
			case "missing visibility":
				f.input.Visibility = ""
			case "invalid repository":
				f.input.Repository = "https://github.com/fixture/built-agent"
			case "altered checksum":
				f.input.SourceFile.SHA256 = strings.Repeat("0", 64)
			case "path traversal":
				f.source(t, []publicationArchiveEntry{{name: "../escape", text: "source"}})
			case "symlink":
				f.source(t, []publicationArchiveEntry{{name: "linked", kind: tar.TypeSymlink}})
			case "credential file":
				f.source(t, []publicationArchiveEntry{{name: ".env", text: "secret=value"}})
			case "token content":
				f.source(t, []publicationArchiveEntry{{name: "README.md", text: publicationToken}})
			case "token filename":
				f.source(t, []publicationArchiveEntry{{name: publicationToken, text: "source"}})
			case "cancelled":
				ctx, cancel := context.WithCancel(f.ctx)
				cancel()
				f.ctx = ctx
			}
			result, err := f.run(t)
			if err == nil {
				t.Fatal("unsafe publication succeeded")
			}
			if len(result.Output) != 0 || strings.Contains(err.Error(), publicationToken) {
				t.Fatal("failed publication leaked output or managed token")
			}
			if f.http.requests != 0 || f.calls(t, "push") != 0 {
				t.Fatal("invalid input reached GitHub")
			}
		})
	}
	t.Run("cancel running Git", func(t *testing.T) {
		f := newPublicationFixture(t, "", "wait")
		ctx, cancel := context.WithCancel(f.ctx)
		f.ctx = ctx
		data, err := json.Marshal(f.input)
		if err != nil {
			t.Fatal(err)
		}
		finished := make(chan error, 1)
		go func() { _, err := f.action.Run(ctx, data); finished <- err }()
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
			if _, err := os.Stat(f.git.Started); err == nil {
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
		if f.calls(t, "push") != 0 {
			t.Fatal("cancelled publication pushed source")
		}
	})
}
