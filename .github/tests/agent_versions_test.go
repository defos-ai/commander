package release_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fixtures "github.com/defos-ai/commander/.github/tests/fixtures"
)

func TestCIEmbeddedAgentBuildVersions(t *testing.T) {
	const builderVersion, spawnerVersion = "v2.3.4", "v4.5.6"
	binary := fixtures.BuildAgentVersions(t, builderVersion, spawnerVersion)
	for _, test := range []struct {
		name, builderOverride, spawnerOverride, wantBuilder, wantSpawner string
	}{
		{name: "package stamps", wantBuilder: builderVersion, wantSpawner: spawnerVersion},
		{name: "caller overrides", builderOverride: "builder-custom", spawnerOverride: "spawner-custom",
			wantBuilder: "builder-custom", wantSpawner: "spawner-custom"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, t.TempDir(), test.builderOverride, test.spawnerOverride)

			output, err := command.CombinedOutput()

			if err != nil {
				t.Fatalf("run embedded agents: %v: %s", err, output)
			}
			var snapshots []struct{ Type, Version, Status string }
			if err := json.Unmarshal(output, &snapshots); err != nil {
				t.Fatal(err)
			}
			if len(snapshots) != 2 {
				t.Fatalf("expected both embedded agent snapshots, got %s", output)
			}
			want := map[string]string{"builder-agent": test.wantBuilder, "spawner-agent": test.wantSpawner}
			for _, snapshot := range snapshots {
				version, exists := want[snapshot.Type]
				if !exists || snapshot.Version != version || snapshot.Status != "ready" {
					t.Errorf("discovered agent = %+v, want its own version %q and ready status", snapshot, version)
				}
				delete(want, snapshot.Type)
			}
			if len(want) != 0 {
				t.Errorf("missing embedded agents: %v", want)
			}
		})
	}
}

func TestCIAgentImageBuildVersions(t *testing.T) {
	for _, image := range []struct{ path, target, agent string }{
		{"apps/agents/builder/docker/Dockerfile", "./apps/agents/builder/cmd", "builder"},
		{"apps/agents/spawner/docker/Dockerfile", "./apps/agents/spawner/cmd", "spawner"},
		{"apps/playground/docker/Dockerfile", "./apps/playground/cmd/commander", "spawner"},
		{"apps/playground/docker/Dockerfile.aio", "./apps/playground/cmd/commander", "spawner"},
	} {
		t.Run(image.path, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("../..", image.path))
			if err != nil {
				t.Fatal(err)
			}
			var command string
			inBuildStage, versionArgument := false, false
			for _, line := range strings.Split(strings.ReplaceAll(string(data), "\\\n", " "), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "FROM ") {
					inBuildStage = strings.HasPrefix(line, "FROM golang:")
					versionArgument = false
				}
				if strings.HasPrefix(line, "ARG VERSION=") {
					t.Fatal("local image builds must leave VERSION empty unless supplied by the caller")
				}
				if !inBuildStage {
					continue
				}
				if line == "ARG VERSION" {
					versionArgument = true
				}
				if offset := strings.Index(line, "go build "); strings.HasPrefix(line, "RUN ") && offset >= 0 {
					if !versionArgument {
						t.Fatal("Go build stage must declare ARG VERSION without a default before compiling")
					}
					if command != "" {
						t.Fatal("expected one agent-hosting Go build command")
					}
					command = line[offset:]
				}
			}
			if command == "" {
				t.Fatal("missing agent-hosting Go build command")
			}
			for _, version := range []string{"", "v2.3.4"} {
				f := fixtures.NewAgentImageBuild(t, command, version)

				output, err := f.Run(t)

				if err != nil {
					t.Fatalf("image build command failed: %v: %s", err, output)
				}
				arguments := strings.Split(strings.TrimSuffix(f.Read(t, "build-arguments"), "\n"), "\n")
				if arguments[len(arguments)-1] != image.target {
					t.Fatalf("image builds %q, want %q", arguments[len(arguments)-1], image.target)
				}
				flags := recordedLinkerFlags(t, arguments)
				want := "-X github.com/defos-ai/commander/apps/agents/" + image.agent + ".Version=" + version
				if flags != want {
					t.Errorf("VERSION=%q produced linker flags %q, want %q", version, flags, want)
				}
			}
		})
	}
}

func TestCIAgentVersionBuildInputs(t *testing.T) {
	var native struct {
		Jobs map[string]struct{ Env map[string]string }
	}
	readCIYAML(t, "..", "workflows/build-binaries.yml", &native)
	if native.Jobs["build"].Env["VERSION"] != "${{ inputs.version }}" {
		t.Error("native builds must receive the release version as VERSION")
	}
	for _, name := range []string{"push-agents.yml", "push-playground.yml", "push-aio.yml"} {
		t.Run(name, func(t *testing.T) {
			var workflow struct {
				Jobs map[string]struct {
					Steps []struct {
						Uses string
						With map[string]string
					}
				}
			}
			readCIYAML(t, "..", "workflows/"+name, &workflow)
			builds := 0
			for _, job := range workflow.Jobs {
				for _, step := range job.Steps {
					if !strings.HasPrefix(step.Uses, "docker/build-push-action@") {
						continue
					}
					builds++
					var versions []string
					for _, argument := range strings.Split(step.With["build-args"], "\n") {
						if strings.HasPrefix(strings.TrimSpace(argument), "VERSION=") {
							versions = append(versions, strings.TrimSpace(argument))
						}
					}
					if len(versions) != 1 || versions[0] != "VERSION=${{ inputs.version }}" {
						t.Errorf("%s must pass the release version as its VERSION build argument, got %q", step.With["file"], step.With["build-args"])
					}
				}
			}
			want := 1
			if name == "push-agents.yml" {
				want = 2
			}
			if builds != want {
				t.Errorf("version wiring checked %d image builds, want %d", builds, want)
			}
		})
	}
}

func recordedLinkerFlags(t *testing.T, arguments []string) string {
	t.Helper()
	var flags []string
	for index, argument := range arguments {
		if value, exists := strings.CutPrefix(argument, "-ldflags="); exists {
			flags = append(flags, value)
		} else if argument == "-ldflags" && index+1 < len(arguments) {
			flags = append(flags, arguments[index+1])
		}
	}
	if len(flags) != 1 {
		t.Fatalf("expected exactly one linker flags argument, got %q", arguments)
	}
	return flags[0]
}
