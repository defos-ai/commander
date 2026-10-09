package release_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

var stableImageNames = []string{
	"commander-admin", "commander-aio", "commander-api", "commander-browser-agent",
	"commander-controller", "commander-mcp", "commander-web-agent",
}

type stableFixture struct {
	dir, selectRun, promoteRun string
	env                        map[string]string
	images                     map[string]string
}

func newStableFixture(t *testing.T) *stableFixture {
	t.Helper()
	f := &stableFixture{dir: t.TempDir(), images: map[string]string{}}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct{ ID, Run string }
		}
	}
	readCIYAML(t, "..", "workflows/push-stable.yml", &workflow)
	for _, step := range workflow.Jobs["promote"].Steps {
		switch step.ID {
		case "select":
			f.selectRun = step.Run
		case "promote":
			f.promoteRun = step.Run
		}
	}
	if f.selectRun == "" || f.promoteRun == "" {
		t.Fatal("stable workflow is missing selection or promotion script")
	}
	for i, name := range stableImageNames {
		f.images[name] = fmt.Sprintf("sha256:%064x", i+1)
	}
	f.env = map[string]string{
		"PATH": f.dir + ":/usr/bin:/bin", "STATE": f.dir,
		"VERSION": "v1.10.0", "GH_REPO": "defos-ai/commander",
		"GITHUB_OUTPUT":       filepath.Join(f.dir, "output"),
		"GITHUB_STEP_SUMMARY": filepath.Join(f.dir, "summary"),
		"RELEASES":            `[{"tag_name":"v1.10.0","draft":false,"prerelease":false}]`,
	}
	f.write(t, "gh", `#!/bin/bash
set -euo pipefail
case "$1" in
  api)
    [[ "$*" == 'api --paginate repos/defos-ai/commander/releases?per_page=100' ]]
    printf '%s\n' "$RELEASES"
    exit "${GH_STATUS:-0}"
    ;;
  release)
    [[ "$*" == "release edit $VERSION --latest" ]]
    printf '%s\n' "$*" >> "$STATE/gh.log"
    printf 'gh %s\n' "$*" >> "$STATE/operations.log"
    exit "${LATEST_STATUS:-0}"
    ;;
  *) exit 1 ;;
esac
`)
	f.write(t, "docker", `#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >> "$STATE/docker.log"
printf 'docker %s\n' "$*" >> "$STATE/operations.log"
[[ "$1 $2" == 'buildx imagetools' ]]
case "$3" in
  create)
    [[ $# == 7 && "$4 $5" == '--prefer-index=false --tag' ]]
    name="${6##*/}"; name="${name%:stable}"
    if [[ "$name" == "${FAIL_IMAGE:-}" && ! -e "$STATE/failed" ]]; then
      touch "$STATE/failed"
      exit 1
    fi
    printf '%s\n' "${7##*@}" > "$STATE/$name"
    ;;
  inspect)
    [[ $# == 6 && "$5" == '--format' && "$6" == '{{json .Manifest}}' ]]
    name="${4##*/}"; name="${name%:stable}"
    digest=$(cat "$STATE/$name")
    [[ "$name" != "${MISMATCH_IMAGE:-}" ]] || digest=sha256:wrong
    printf '{"digest":"%s"}\n' "$digest"
    ;;
  *) exit 1 ;;
esac
`)
	return f
}

func (f *stableFixture) write(t *testing.T, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(data), 0700); err != nil {
		t.Fatal(err)
	}
}

func (f *stableFixture) read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func (f *stableFixture) run(t *testing.T, script string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", script)
	images, err := json.Marshal(f.images)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = []string{"IMAGES=" + string(images)}
	for key, value := range f.env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func TestCIStableReleaseSelection(t *testing.T) {
	for _, tc := range []struct {
		name, version, releases, status, want string
		fail                                  bool
	}{
		{"numeric order", "v1.10.0", `[{"tag_name":"v1.9.9"},{"tag_name":"v1.10.0"}]`, "0", "true", false},
		{"maintenance", "v1.9.10", `[{"tag_name":"v1.9.10"},{"tag_name":"v1.10.0"}]`, "0", "false", false},
		{"draft and prerelease ignored", "v1.10.0", `[{"tag_name":"v1.10.0"},{"tag_name":"v1.11.0","draft":true},{"tag_name":"v1.12.0","prerelease":true},{"tag_name":"v2.0.0-rc.1"}]`, "0", "true", false},
		{"absent release", "v1.10.0", `[{"tag_name":"v1.9.0"}]`, "0", "", true},
		{"API failure with output", "v1.10.0", `[{"tag_name":"v1.10.0"}]`, "1", "", true},
		{"prerelease input", "v1.10.0-rc.1", `[{"tag_name":"v1.10.0-rc.1"}]`, "0", "", true},
		{"placeholder input", "v0.0.0", `[{"tag_name":"v0.0.0"}]`, "0", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStableFixture(t)
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
			f.env["VERSION"], f.env["RELEASES"], f.env["GH_STATUS"] = tc.version, string(data), tc.status
			output, err := f.run(t, f.selectRun)
			if (err != nil) != tc.fail {
				t.Fatalf("selection error = %v: %s", err, output)
			}
			want := ""
			if tc.want != "" {
				want = "promote=" + tc.want + "\n"
			}
			if got := f.read(t, "output"); got != want {
				t.Errorf("selection output = %q, want %q", got, want)
			}
			if got := f.read(t, "docker.log"); got != "" {
				t.Fatalf("selection invoked docker: %s", got)
			}
			if got := f.read(t, "gh.log"); got != "" {
				t.Fatalf("selection changed the latest release: %s", got)
			}
		})
	}
	t.Run("newer release on page two", func(t *testing.T) {
		f := newStableFixture(t)
		f.env["VERSION"] = "v1.9.0"
		f.env["RELEASES"] = `[{"tag_name":"v1.9.0","draft":false,"prerelease":false}]
[{"tag_name":"v1.10.0","draft":false,"prerelease":false}]`
		if output, err := f.run(t, f.selectRun); err != nil {
			t.Fatalf("paginated selection failed: %v: %s", err, output)
		}
		if got := f.read(t, "output"); got != "promote=false\n" {
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
	if len(images) != len(stableImageNames) {
		t.Fatalf("expected %d promotion images, got %v", len(stableImageNames), images)
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
		}
		readCIYAML(t, "../..", strings.TrimPrefix(job.Uses, "./"), &publisher)
		outputs := publisher.On["workflow_call"].Outputs
		if len(outputs) == 0 {
			t.Errorf("image publisher %s exposes no image digests", name)
		}
		for output := range outputs {
			found := false
			for _, image := range stableImageNames {
				found = found || images[image] == "${{ needs."+name+".outputs."+output+" }}"
			}
			if !found {
				t.Errorf("image publisher %s does not provide %s to stable promotion", name, output)
			}
		}
	}
}

func TestCIStableDigestValidation(t *testing.T) {
	for _, invalid := range []string{"", "sha256:bad", "sha256:" + strings.Repeat("a", 63), "missing", "extra"} {
		t.Run(invalid, func(t *testing.T) {
			f := newStableFixture(t)
			switch invalid {
			case "missing":
				delete(f.images, "commander-api")
			case "extra":
				f.images["unexpected"] = f.images["commander-api"]
			default:
				f.images["commander-api"] = invalid
			}
			if output, err := f.run(t, f.selectRun); err == nil {
				t.Fatalf("invalid digests accepted: %s", output)
			}
			if f.read(t, "output") != "" || f.read(t, "docker.log") != "" {
				t.Fatal("invalid digests must fail before authorizing promotion")
			}
		})
	}
}

func TestCIStablePromotion(t *testing.T) {
	for _, mode := range []string{"success", "verification mismatch", "retry partial failure", "retry latest failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newStableFixture(t)
			if mode == "verification mismatch" {
				f.env["MISMATCH_IMAGE"] = stableImageNames[0]
			}
			if mode == "retry partial failure" {
				f.env["FAIL_IMAGE"] = stableImageNames[2]
			}
			if mode == "retry latest failure" {
				f.env["LATEST_STATUS"] = "1"
			}
			output, err := f.run(t, f.promoteRun)
			if mode != "success" && err == nil {
				t.Fatal("promotion must fail on registry, verification, or release update errors")
			}
			if mode == "verification mismatch" {
				if !strings.Contains(output, "Stable digest verification failed") || strings.Count(f.read(t, "docker.log"), "\n") != 2 {
					t.Fatalf("verification did not stop promotion: %s", output)
				}
				if f.read(t, "gh.log") != "" {
					t.Fatal("failed image verification must not change the latest release")
				}
				return
			}
			if mode == "retry partial failure" {
				if f.read(t, stableImageNames[0]) == "" || f.read(t, stableImageNames[2]) != "" {
					t.Fatal("expected partial registry state before retry")
				}
				if f.read(t, "gh.log") != "" {
					t.Fatal("partial image promotion must not change the latest release")
				}
			}
			if mode == "retry latest failure" {
				if strings.Count(f.read(t, "docker.log"), "\n") != 2*len(stableImageNames) || f.read(t, "gh.log") == "" {
					t.Fatal("latest update must be attempted after all image tags are verified")
				}
				if strings.Contains(f.read(t, "summary"), "GitHub latest release") {
					t.Fatal("failed latest update must not report success")
				}
				f.env["LATEST_STATUS"] = "0"
			}
			if strings.HasPrefix(mode, "retry ") {
				f.write(t, "docker.log", "")
				f.write(t, "gh.log", "")
				f.write(t, "operations.log", "")
				output, err = f.run(t, f.promoteRun)
			}
			if err != nil {
				t.Fatalf("promotion failed: %v: %s", err, output)
			}
			var want strings.Builder
			for _, name := range stableImageNames {
				image, digest := "ghcr.io/defos-ai/"+name, f.images[name]
				fmt.Fprintf(&want, "buildx imagetools create --prefer-index=false --tag %s:stable %s@%s\n", image, image, digest)
				fmt.Fprintf(&want, "buildx imagetools inspect %s:stable --format {{json .Manifest}}\n", image)
				if got := strings.TrimSpace(f.read(t, name)); got != digest {
					t.Errorf("%s stable digest = %q, want %q", name, got, digest)
				}
			}
			if got := f.read(t, "docker.log"); got != want.String() {
				t.Errorf("unexpected Docker operations:\n%s\nwant:\n%s", got, want.String())
			}
			latest := "release edit " + f.env["VERSION"] + " --latest\n"
			if got := f.read(t, "gh.log"); got != latest {
				t.Errorf("latest release update = %q, want %q", got, latest)
			}
			var operations strings.Builder
			for _, operation := range strings.Split(strings.TrimSuffix(want.String(), "\n"), "\n") {
				fmt.Fprintln(&operations, "docker "+operation)
			}
			operations.WriteString("gh " + latest)
			if got := f.read(t, "operations.log"); got != operations.String() {
				t.Errorf("latest must follow all image verifications:\n%s", got)
			}
		})
	}
}
