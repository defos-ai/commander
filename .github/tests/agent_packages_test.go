package release_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	fakes "github.com/defos-ai/commander/.github/tests/fakes"
	fixtures "github.com/defos-ai/commander/.github/tests/fixtures"
	"go.yaml.in/yaml/v3"
)

func TestCIAgentPackagePublication(t *testing.T) {
	for _, agent := range []string{"builder", "spawner"} {
		for _, draft := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing draft %t", agent, draft), func(t *testing.T) {
				f := fixtures.NewAgentPackage(t, agent)
				if draft {
					f.SetEnvironment("RELEASES", fmt.Sprintf(`{"tag_name":%q,"draft":true}`, f.Environment("TAG")))
				}
				if output, err := f.Run(t); err != nil {
					t.Fatalf("publish failed: %v: %s", err, output)
				}
				log := f.Read(t, "gh.log")
				if got := strings.Contains(log, "release create"); got == draft {
					t.Errorf("create release = %t, existing draft = %t", got, draft)
				}
				assets := []string{"agent.yaml", "LICENSE"}
				assets = append(assets, fixtures.AgentNativeBinaryNames(agent)...)
				var checksums strings.Builder
				for _, name := range assets {
					got, want := f.Read(t, "uploaded/"+name), f.Read(t, "package/"+name)
					if got == "" || got != want {
						t.Errorf("uploaded %s = %q, want %q", name, got, want)
					}
					fmt.Fprintf(&checksums, "%x  %s\n", sha256.Sum256([]byte(want)), name)
				}
				if got := f.Read(t, "uploaded/checksums.txt"); got != checksums.String() {
					t.Errorf("checksums = %q, want %q", got, checksums.String())
				}
				entries, err := os.ReadDir(filepath.Join(f.Dir(), "uploaded"))
				if err != nil || len(entries) != len(assets)+1 {
					t.Fatalf("%s package must contain %d assets: %v, %v", agent, len(assets)+1, entries, err)
				}
				if !strings.HasSuffix(log, "release edit "+f.Environment("TAG")+" --draft=false --latest=false\n") {
					t.Errorf("agent release must publish without promoting Latest: %s", log)
				}
			})
		}
	}
}

func TestCIAgentPackageWorkflowManifest(t *testing.T) {
	generator := fixtures.BuildAgentManifestGenerator(t)
	for _, agent := range []string{"builder", "spawner"} {
		t.Run(agent, func(t *testing.T) {
			f := fixtures.NewAgentPackage(t, agent)
			if err := os.Remove(filepath.Join(f.Artifacts(), "agent.yaml")); err != nil {
				t.Fatal(err)
			}
			f.SetEnvironment("GENERATOR", generator)
			f.SetEnvironment("DIGEST", "sha256:"+strings.Repeat("b", 64))
			f.Write(t, "go", fakes.AgentManifestGo)
			f.SetArtifacts(f.Dir())
			f.SetScript(fixtures.WorkflowScript(t, "push-binaries.yml", "agents", "manifest"))
			if output, err := f.Run(t); err != nil {
				t.Fatalf("workflow manifest generation failed: %v: %s", err, output)
			}
			f.SetArtifacts(filepath.Join(f.Dir(), "package"))
			f.SetScript(fixtures.WorkflowScript(t, "push-binaries.yml", "agents", "publish"))
			if output, err := f.Run(t); err != nil {
				t.Fatalf("generated package publication failed: %v: %s", err, output)
			}
			if got, want := f.Read(t, "uploaded/agent.yaml"), f.Read(t, "package/agent.yaml"); got == "" || got != want {
				t.Errorf("published manifest = %q, want generated manifest %q", got, want)
			}
		})
	}
}

func TestCIAgentPackageReleaseTag(t *testing.T) {
	const agent = "spawner"
	for _, mode := range []string{"tested SHA", "different SHA", "missing tag", "invalid version"} {
		t.Run(agent+"/"+mode, func(t *testing.T) {
			f := fixtures.NewNative(t)
			f.SetEnvironment("TAG", "apps/agents/"+agent+"/v0.1.0")
			f.SetEnvironment("GITHUB_SHA", strings.Repeat("a", 40))
			f.SetEnvironment("TAG_SHA", f.Environment("GITHUB_SHA"))
			f.Write(t, "git", fakes.AgentTagGit)
			switch mode {
			case "different SHA":
				f.SetEnvironment("TAG_SHA", strings.Repeat("b", 40))
			case "missing tag":
				f.SetEnvironment("MISSING_TAG", "1")
			case "invalid version":
				f.SetEnvironment("VERSION", "v0.0.0")
			}
			f.SetScript(fixtures.WorkflowScript(t, "push-binaries.yml", "agents", "verify"))
			if output, err := f.Run(t); (err != nil) != (mode != "tested SHA") {
				t.Fatalf("module tag verification = %v: %s", err, output)
			}
		})
	}
}

func TestCIAgentPackagePublicationRejectsInvalidAssets(t *testing.T) {
	const agent = "spawner"
	for _, mode := range []string{"missing", "empty", "directory", "symlink", "extra", "hidden extra", "wrong name"} {
		t.Run(agent+"/"+mode, func(t *testing.T) {
			f := fixtures.NewAgentPackage(t, agent)
			path := filepath.Join(f.Artifacts(), "agent.yaml")
			if mode != "extra" && mode != "hidden extra" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			switch mode {
			case "empty":
				err = os.WriteFile(path, nil, 0600)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "symlink":
				err = os.Symlink("LICENSE", path)
			case "extra":
				f.Write(t, "package/extra", "unexpected")
			case "hidden extra":
				f.Write(t, "package/.extra", "unexpected")
			case "wrong name":
				f.Write(t, "package/"+agent+"-agent.yaml", "wrong manifest name")
			}
			if err != nil {
				t.Fatal(err)
			}
			if output, err := f.Run(t); err == nil {
				t.Fatalf("invalid assets published: %s", output)
			}
			if got := f.Read(t, "gh.log"); got != "" {
				t.Errorf("invalid assets reached GitHub: %s", got)
			}
		})
	}
}

func TestCIAgentPackagePublicationFailures(t *testing.T) {
	const agent = "spawner"
	for _, mode := range []string{"API failure", "upload failure"} {
		t.Run(agent+"/"+mode, func(t *testing.T) {
			f := fixtures.NewAgentPackage(t, agent)
			switch mode {
			case "API failure":
				f.SetEnvironment("GH_STATUS", "1")
			case "upload failure":
				f.SetEnvironment("FAIL_UPLOAD", "1")
			}
			if output, err := f.Run(t); err == nil {
				t.Fatalf("publication failure ignored: %s", output)
			}
			log := f.Read(t, "gh.log")
			if strings.Contains(log, "release edit") {
				t.Errorf("failed publication finalized release: %s", log)
			}
			if mode != "upload failure" && strings.Contains(log, "release upload") {
				t.Errorf("publication must not upload after lookup failure: %s", log)
			}
		})
	}
}

func TestCIAgentPackageStageRejectsInvalidBinaries(t *testing.T) {
	for _, mode := range []string{"missing", "empty", "directory", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			f := fixtures.NewNative(t)
			path := filepath.Join(f.Artifacts(), fixtures.AgentNativeBinaryNames("spawner")[0])
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			var err error
			switch mode {
			case "empty":
				err = os.WriteFile(path, nil, 0600)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "symlink":
				err = os.Symlink("../LICENSE", path)
			}
			if err != nil {
				t.Fatal(err)
			}
			f.SetEnvironment("AGENT", "spawner")
			f.SetArtifacts(f.Dir())
			f.SetScript(fixtures.WorkflowScript(t, "push-binaries.yml", "agents", "stage"))
			if output, err := f.Run(t); err == nil {
				t.Fatalf("invalid spawner binary staged: %s", output)
			}
		})
	}
}

func TestCIAgentPackageReleaseWiring(t *testing.T) {
	var release struct {
		Jobs map[string]struct {
			Needs yaml.Node
			Uses  string
			With  map[string]string
		}
	}
	readCIYAML(t, "..", "workflows/make-release.yml", &release)
	job := release.Jobs["release"]
	var needs []string
	if err := job.Needs.Decode(&needs); err != nil {
		t.Fatal(err)
	}
	if job.Uses != "./.github/workflows/push-binaries.yml" || !slices.Contains(needs, "agents") {
		t.Error("release must wait for agent images before publishing their packages")
	}
	for _, agent := range []string{"builder", "spawner"} {
		if job.With[agent+"-digest"] != "${{ needs.agents.outputs."+agent+"-digest }}" {
			t.Errorf("%s package must receive the image digest from this release run", agent)
		}
	}
	var publisher struct {
		On map[string]struct {
			Inputs map[string]struct {
				Type     string
				Required bool
			}
		}
		Jobs map[string]struct {
			Needs    string
			Env      map[string]string
			Strategy struct {
				FailFast *bool `yaml:"fail-fast"`
				Matrix   struct {
					Include []struct{ Agent, Digest string }
				}
			}
			Steps []struct {
				ID, Uses, If, Run string
				With              map[string]string
				Directory         string `yaml:"working-directory"`
			}
		}
	}
	readCIYAML(t, "..", "workflows/push-binaries.yml", &publisher)
	if len(publisher.On) != 1 {
		t.Error("package publication must remain reusable-only")
	}
	for _, input := range []string{"version", "builder-digest", "spawner-digest"} {
		if config := publisher.On["workflow_call"].Inputs[input]; !config.Required || config.Type != "string" {
			t.Errorf("package publisher must require string input %s", input)
		}
	}
	if publisher.Jobs["release"].Needs != "agents" {
		t.Error("root release must wait for both agent package releases")
	}
	agents := publisher.Jobs["agents"]
	if agents.Strategy.FailFast == nil || *agents.Strategy.FailFast {
		t.Error("package matrix must finish independent releases so failed jobs can be retried")
	}
	if len(agents.Strategy.Matrix.Include) != 2 {
		t.Fatal("package matrix must publish exactly builder and spawner")
	}
	for _, entry := range agents.Strategy.Matrix.Include {
		if !slices.Contains([]string{"builder", "spawner"}, entry.Agent) || entry.Digest != "${{ inputs."+entry.Agent+"-digest }}" {
			t.Errorf("invalid agent package matrix entry: %+v", entry)
		}
	}
	if agents.Env["TAG"] != "apps/agents/${{ matrix.agent }}/${{ inputs.version }}" {
		t.Error("agent packages must use their module release tags")
	}
	positions := map[string]int{}
	for i, step := range agents.Steps {
		if step.ID != "" {
			positions[step.ID] = i
		}
		switch {
		case strings.HasPrefix(step.Uses, "actions/checkout@"):
			if step.With["ref"] != "${{ github.sha }}" || step.With["fetch-depth"] != "0" {
				t.Error("agent publisher must check out the tested SHA with its module tags")
			}
		case strings.HasPrefix(step.Uses, "actions/download-artifact@"):
			if step.If != "" || step.With["pattern"] != "commander-native-*" || step.With["run-id"] != "" {
				t.Error("agent packages must use native artifacts from this release run")
			}
		case step.ID == "manifest":
			if step.If != "" || step.Directory != "" || !strings.Contains(step.Run, "go run ./.github/scripts/agent-manifest.go") || !strings.Contains(step.Run, "--artifacts package --output package/agent.yaml") {
				t.Error("each package must generate its manifest from staged binaries in the release workspace")
			}
		case step.ID == "publish":
			if step.If != "" || step.Directory != "package" {
				t.Error("package publication must require successful manifest generation")
			}
		}
	}
	for _, id := range []string{"verify", "stage", "manifest", "publish"} {
		if _, ok := positions[id]; !ok {
			t.Fatalf("agent publisher lacks %s step", id)
		}
	}
	if !(positions["verify"] < positions["stage"] && positions["stage"] < positions["manifest"] && positions["manifest"] < positions["publish"]) {
		t.Error("agent publication must follow tag verification, staging and manifest generation")
	}
}
