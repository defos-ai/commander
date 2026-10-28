package release_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	fixtures "github.com/defos-ai/commander/.github/tests/fixtures"
	"go.yaml.in/yaml/v3"
)

func TestCIStableReleaseSelection(t *testing.T) {
	for _, tc := range []struct {
		name, version, releases, status, want string
		fail                                  bool
	}{
		{"numeric order", "v1.10.0", `[{"tag_name":"v1.9.9"},{"tag_name":"v1.10.0"}]`, "0", "true", false},
		{"maintenance", "v1.9.10", `[{"tag_name":"v1.9.10"},{"tag_name":"v1.10.0"}]`, "0", "false", false},
		{"draft and prerelease ignored", "v1.10.0", `[{"tag_name":"v1.10.0"},{"tag_name":"v1.11.0","draft":true},{"tag_name":"v1.12.0","prerelease":true},{"tag_name":"v2.0.0-rc.1"}]`, "0", "true", false},
		{"agent module releases ignored", "v1.10.0", `[{"tag_name":"v1.10.0"},{"tag_name":"apps/agents/builder/v9.0.0"},{"tag_name":"apps/agents/spawner/v9.0.0"}]`, "0", "true", false},
		{"absent release", "v1.10.0", `[{"tag_name":"v1.9.0"}]`, "0", "", true},
		{"API failure with output", "v1.10.0", `[{"tag_name":"v1.10.0"}]`, "1", "", true},
		{"prerelease input", "v1.10.0-rc.1", `[{"tag_name":"v1.10.0-rc.1"}]`, "0", "", true},
		{"placeholder input", "v0.0.0", `[{"tag_name":"v0.0.0"}]`, "0", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixtures.NewStable(t)
			// Fill only omitted flags, preserving explicitly draft/prerelease releases.
			var releases []map[string]any
			if err := json.Unmarshal([]byte(tc.releases), &releases); err != nil {
				t.Fatal(err)
			}
			for _, release := range releases {
				for _, flag := range []string{"draft", "prerelease"} {
					if _, ok := release[flag]; !ok {
						release[flag] = false
					}
				}
			}
			data, _ := json.Marshal(releases)
			f.SetEnvironment("VERSION", tc.version)
			f.SetEnvironment("RELEASES", string(data))
			f.SetEnvironment("GH_STATUS", tc.status)

			output, err := f.Select(t)

			if (err != nil) != tc.fail {
				t.Fatalf("selection error = %v: %s", err, output)
			}
			want := ""
			if tc.want != "" {
				want = "promote=" + tc.want + "\n"
			}
			if got := f.Read(t, "output"); got != want {
				t.Errorf("selection output = %q, want %q", got, want)
			}
			if got := f.Read(t, "docker.log"); got != "" {
				t.Fatalf("selection invoked docker: %s", got)
			}
			if got := f.Read(t, "gh.log"); got != "" {
				t.Fatalf("selection changed the latest release: %s", got)
			}
		})
	}
	t.Run("newer release on page two", func(t *testing.T) {
		f := fixtures.NewStable(t)
		f.SetEnvironment("VERSION", "v1.9.0")
		f.SetEnvironment("RELEASES", `[{"tag_name":"v1.9.0","draft":false,"prerelease":false}]
[{"tag_name":"v1.10.0","draft":false,"prerelease":false}]`)
		if output, err := f.Select(t); err != nil {
			t.Fatalf("paginated selection failed: %v: %s", err, output)
		}
		if got := f.Read(t, "output"); got != "promote=false\n" {
			t.Errorf("paginated selection = %q, want promote=false", got)
		}
	})
}

func TestCIStableReleaseWiring(t *testing.T) {
	var workflow struct {
		Jobs map[string]struct {
			Needs       yaml.Node
			Uses        string
			With        map[string]string
			Permissions map[string]string
		}
	}
	readCIYAML(t, "..", "workflows/make-release.yml", &workflow)
	stable := workflow.Jobs["stable"]
	if stable.Uses != "./.github/workflows/push-stable.yml" {
		t.Fatal("release must call the stable promotion workflow")
	}
	if stable.Permissions["contents"] != "write" || stable.Permissions["packages"] != "write" {
		t.Error("release must grant stable promotion access to update releases and packages")
	}
	var promotion struct {
		Permissions map[string]string
		Jobs        map[string]struct {
			Steps []struct {
				ID, If string
				Env    map[string]string
			}
		}
	}
	readCIYAML(t, "..", "workflows/push-stable.yml", &promotion)
	if promotion.Permissions["contents"] != "write" || promotion.Permissions["packages"] != "write" {
		t.Error("stable workflow must request access to update releases and packages")
	}
	for _, step := range promotion.Jobs["promote"].Steps {
		if step.ID != "promote" {
			continue
		}
		if step.If != "steps.select.outputs.promote == 'true'" {
			t.Error("stable and latest promotion must be gated by the newest published release")
		}
		if step.Env["GH_TOKEN"] != "${{ secrets.GITHUB_TOKEN }}" || step.Env["GH_REPO"] != "${{ github.repository }}" {
			t.Error("latest release promotion must use the repository token and target")
		}
	}
	var needs []string
	if stable.Needs.Kind == yaml.ScalarNode {
		needs = []string{stable.Needs.Value}
	} else if err := stable.Needs.Decode(&needs); err != nil {
		t.Fatal(err)
	}
	dependencies := map[string]bool{}
	for _, name := range needs {
		dependencies[name] = true
	}
	if !dependencies["release"] {
		t.Error("stable promotion must wait for the completed GitHub release")
	}
	var images map[string]string
	if err := json.Unmarshal([]byte(stable.With["images"]), &images); err != nil {
		t.Fatal(err)
	}
	if len(images) != len(fixtures.StableImageNames()) {
		t.Fatalf("expected %d promotion images, got %v", len(fixtures.StableImageNames()), images)
	}
	for name, job := range workflow.Jobs {
		if !strings.HasPrefix(job.Uses, "./.github/workflows/push-") || name == "release" || name == "stable" {
			continue
		}
		if !dependencies[name] {
			t.Errorf("stable promotion does not depend on image publisher %s", name)
		}
		var publisher struct {
			On map[string]struct {
				Outputs map[string]struct{ Value string }
			}
			Jobs map[string]struct {
				Steps []struct {
					Uses string
					With map[string]string
				}
			}
		}
		readCIYAML(t, "../..", strings.TrimPrefix(job.Uses, "./"), &publisher)
		// Check destinations too: a publish step can overwrite an independent
		// agent's image even when it does not expose a digest output.
		for _, job := range publisher.Jobs {
			for _, step := range job.Steps {
				if !strings.HasPrefix(step.Uses, "docker/build-push-action@") {
					continue
				}
				for _, tag := range strings.Split(step.With["tags"], "\n") {
					image, _, _ := strings.Cut(strings.TrimSpace(tag), ":")
					const prefix = "ghcr.io/defos-ai/"
					if !strings.HasPrefix(image, prefix) || !slices.Contains(fixtures.StableImageNames(), strings.TrimPrefix(image, prefix)) {
						t.Errorf("image publisher %s targets image outside Commander releases: %s", name, image)
					}
				}
			}
		}
		outputs := publisher.On["workflow_call"].Outputs
		if len(outputs) == 0 {
			t.Errorf("image publisher %s exposes no image digests", name)
		}
		for output := range outputs {
			found := false
			for _, image := range fixtures.StableImageNames() {
				found = found || images[image] == "${{ needs."+name+".outputs."+output+" }}"
			}
			if !found {
				t.Errorf("image publisher %s does not provide %s to stable promotion", name, output)
			}
		}
	}
}

func TestCIStableDigestValidation(t *testing.T) {
	for _, invalid := range []string{"", "sha256:bad", "sha256:" + strings.Repeat("a", 63), "missing", "extra", "commander-web-agent", "commander-browser-agent"} {
		t.Run(invalid, func(t *testing.T) {
			f := fixtures.NewStable(t)
			switch invalid {
			case "missing":
				f.RemoveImage("commander-api")
			case "extra":
				f.SetImage("unexpected", f.Image("commander-api"))
			case "commander-web-agent", "commander-browser-agent":
				f.SetImage(invalid, f.Image("commander-api"))
			default:
				f.SetImage("commander-api", invalid)
			}
			if output, err := f.Select(t); err == nil {
				t.Fatalf("invalid digests accepted: %s", output)
			}
			if f.Read(t, "output") != "" || f.Read(t, "docker.log") != "" {
				t.Fatal("invalid digests must fail before authorizing promotion")
			}
		})
	}
}

func TestCIStablePromotion(t *testing.T) {
	for _, mode := range []string{"success", "verification mismatch", "retry partial failure", "retry latest failure"} {
		t.Run(mode, func(t *testing.T) {
			f := fixtures.NewStable(t)
			if mode == "verification mismatch" {
				f.SetEnvironment("MISMATCH_IMAGE", fixtures.StableImageNames()[0])
			}
			if mode == "retry partial failure" {
				f.SetEnvironment("FAIL_IMAGE", fixtures.StableImageNames()[2])
			}
			if mode == "retry latest failure" {
				f.SetEnvironment("LATEST_STATUS", "1")
			}

			output, err := f.Promote(t)

			if mode != "success" && err == nil {
				t.Fatal("promotion must fail on registry, verification, or release update errors")
			}
			if mode == "verification mismatch" {
				if !strings.Contains(output, "Stable digest verification failed") || strings.Count(f.Read(t, "docker.log"), "\n") != 2 {
					t.Fatalf("verification did not stop promotion: %s", output)
				}
				if f.Read(t, "gh.log") != "" {
					t.Fatal("failed image verification must not change the latest release")
				}
				return
			}
			if mode == "retry partial failure" {
				if f.Read(t, fixtures.StableImageNames()[0]) == "" || f.Read(t, fixtures.StableImageNames()[2]) != "" {
					t.Fatal("expected partial registry state before retry")
				}
				if f.Read(t, "gh.log") != "" {
					t.Fatal("partial image promotion must not change the latest release")
				}
			}
			if mode == "retry latest failure" {
				if strings.Count(f.Read(t, "docker.log"), "\n") != 2*len(fixtures.StableImageNames()) || f.Read(t, "gh.log") == "" {
					t.Fatal("latest update must be attempted after all image tags are verified")
				}
				if strings.Contains(f.Read(t, "summary"), "GitHub latest release") {
					t.Fatal("failed latest update must not report success")
				}
				f.SetEnvironment("LATEST_STATUS", "0")
			}
			if strings.HasPrefix(mode, "retry ") {
				f.Write(t, "docker.log", "")
				f.Write(t, "gh.log", "")
				f.Write(t, "operations.log", "")
				output, err = f.Promote(t)
			}
			if err != nil {
				t.Fatalf("promotion failed: %v: %s", err, output)
			}
			var want strings.Builder
			for _, name := range fixtures.StableImageNames() {
				image, digest := "ghcr.io/defos-ai/"+name, f.Image(name)
				fmt.Fprintf(&want, "buildx imagetools create --prefer-index=false --tag %s:stable %s@%s\n", image, image, digest)
				fmt.Fprintf(&want, "buildx imagetools inspect %s:stable --format {{json .Manifest}}\n", image)
				if got := strings.TrimSpace(f.Read(t, name)); got != digest {
					t.Errorf("%s stable digest = %q, want %q", name, got, digest)
				}
			}
			if got := f.Read(t, "docker.log"); got != want.String() {
				t.Errorf("unexpected Docker operations:\n%s\nwant:\n%s", got, want.String())
			}
			latest := "release edit " + f.Environment("VERSION") + " --latest\n"
			if got := f.Read(t, "gh.log"); got != latest {
				t.Errorf("latest release update = %q, want %q", got, latest)
			}
			var operations strings.Builder
			for _, operation := range strings.Split(strings.TrimSuffix(want.String(), "\n"), "\n") {
				fmt.Fprintln(&operations, "docker "+operation)
			}
			operations.WriteString("gh " + latest)
			if got := f.Read(t, "operations.log"); got != operations.String() {
				t.Errorf("latest must follow all image verifications:\n%s", got)
			}
		})
	}
}
