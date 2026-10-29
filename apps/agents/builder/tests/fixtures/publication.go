package fixtures

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/id"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
)

type Publication struct {
	Action      *builderactions.PublishAgent
	Files       artifacts.ArtifactFileStore
	Input       builderactions.PublishAgentInput
	Ctx         context.Context
	Checkpoints *fakes.Checkpoints
	HTTP        *fakes.PublicationHTTP
	Git         fakes.PublicationGit
}

func NewPublication(t *testing.T, visibility, mode string) *Publication {
	t.Helper()
	files, err := fsartifacts.NewArtifactFileFsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	control := t.TempDir()
	git := fakes.PublicationGit{Token: PublicationToken, Repository: PublicationRepository, Bare: filepath.Join(control, "remote.git"), Calls: filepath.Join(control, "calls"), Started: filepath.Join(control, "started"), Mode: mode}
	Git(t, "init", "--bare", "--initial-branch=main", git.Bare)
	scope := GitScope(t, fakes.GitConfig{Publish: &git})
	executor, err := builderactions.NewPublishAgent(files, visibility)
	if err != nil {
		t.Fatal(err)
	}
	status, err := executor.Discover(t.Context(), &environment.Local{}, scope)
	if err != nil || status != action.Ready {
		t.Fatalf("publication discovery: %s %v", status, err)
	}
	if names := executor.Action().RequiredSecrets; len(names) != 1 || names[0] != "GITHUB_TOKEN" {
		t.Fatal("publication must declare managed GITHUB_TOKEN")
	}
	input := builderactions.PublishAgentInput{Repository: PublicationRepository, SourceArtifactID: id.ArtifactID(), Visibility: "public"}
	if visibility != "" {
		input.Visibility = ""
	}
	command, err := messages.NewMessage(messages.RunAction, "builder-agent-12345678", actionmodel.ActionCall{ActionName: builderactions.PublishAgentName, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	journal := fakes.NewCheckpoints(command)
	ctx := action.WithExecution(t.Context(), action.Execution{CommandID: command.ID})
	ctx = action.WithSecrets(action.WithCheckpoints(ctx, journal), fakes.PublicationSecrets(PublicationToken))
	httpFixture := fakes.NewPublicationHTTP(PublicationToken, PublicationRepository)
	previous := http.DefaultTransport
	http.DefaultTransport = httpFixture
	t.Cleanup(func() { http.DefaultTransport = previous })
	f := &Publication{Action: executor, Files: files, Input: input, Ctx: ctx, Checkpoints: journal, HTTP: httpFixture, Git: git}
	f.Source(t, []ArchiveEntry{{Name: "README.md", Text: "# Built agent\n"}, {Name: "cmd/main.go", Text: "package main\nfunc main() {}\n"}})
	return f
}

type ArchiveEntry struct {
	Name, Text string
	Kind       byte
}

func (f *Publication) Source(t *testing.T, entries []ArchiveEntry) {
	t.Helper()
	var data bytes.Buffer
	compressed := gzip.NewWriter(&data)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		Kind := entry.Kind
		if Kind == 0 {
			Kind = tar.TypeReg
		}
		header := &tar.Header{Name: entry.Name, Mode: 0600, Typeflag: Kind, Size: int64(len(entry.Text))}
		if Kind == tar.TypeSymlink {
			header.Linkname = "README.md"
			header.Size = 0
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := io.WriteString(archive, entry.Text); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := f.Files.Put(t.Context(), f.Input.SourceArtifactID, artifacts.FileSource{Name: "source.tar.gz", MediaType: "application/gzip", Data: data.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	f.Input.SourceFile = file
}

func (f *Publication) Run(t *testing.T) (action.Result, error) {
	t.Helper()
	data, err := json.Marshal(f.Input)
	if err != nil {
		t.Fatal(err)
	}
	return f.Action.Run(f.Ctx, data)
}
