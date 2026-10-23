package release_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var commanderBinaryNames = []string{
	"commander_linux_amd64", "commander_linux_arm64",
	"commander_darwin_amd64", "commander_darwin_arm64",
	"commander_windows_amd64.exe", "commander_windows_arm64.exe",
}

var rootBinaryNames = append(append([]string{}, commanderBinaryNames...),
	"commander-admin_linux_amd64", "commander-admin_linux_arm64",
	"commander-admin_darwin_amd64", "commander-admin_darwin_arm64",
	"commander-admin_windows_amd64.exe", "commander-admin_windows_arm64.exe",
)

var nativeBinaryNames = append(append([]string{}, rootBinaryNames...),
	"commander-spawner-agent_linux_amd64", "commander-spawner-agent_linux_arm64",
	"commander-spawner-agent_darwin_amd64", "commander-spawner-agent_darwin_arm64",
	"commander-spawner-agent_windows_amd64.exe", "commander-spawner-agent_windows_arm64.exe",
	"commander-builder-agent_linux_amd64", "commander-builder-agent_linux_arm64",
	"commander-builder-agent_darwin_amd64", "commander-builder-agent_darwin_arm64",
	"commander-builder-agent_windows_amd64.exe", "commander-builder-agent_windows_arm64.exe",
)

func agentNativeBinaryNames(agent string) []string {
	var names []string
	for _, name := range nativeBinaryNames {
		if strings.HasPrefix(name, "commander-"+agent+"-agent_") {
			names = append(names, name)
		}
	}
	return names
}

type nativeFixture struct {
	dir, artifacts, script string
	env                    map[string]string
}

func newNativeFixture(t *testing.T) *nativeFixture {
	t.Helper()
	f := &nativeFixture{dir: t.TempDir()}
	f.artifacts = filepath.Join(f.dir, "artifacts")
	if err := os.Mkdir(f.artifacts, 0700); err != nil {
		t.Fatal(err)
	}
	f.script = nativeWorkflowScript(t, "push-binaries.yml", "release", "publish")
	f.env = map[string]string{
		"PATH": f.dir + ":/usr/bin:/bin", "STATE": f.dir,
		"VERSION": "v0.1.0", "GH_REPO": "defos-ai/commander",
		"RELEASE_TAG": "v0.1.0", "RELEASE_TITLE": "Commander v0.1.0",
		"RELEASES": "", "GH_STATUS": "0",
	}
	f.write(t, "LICENSE", "Commander test license\n")
	for _, name := range nativeBinaryNames {
		f.write(t, "artifacts/"+name, name+"\n")
	}
	f.write(t, "gh", `#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >> "$STATE/gh.log"
case "$1 $2" in
  'api --paginate')
    [[ "$3 $4 $5" == 'repos/defos-ai/commander/releases?per_page=100 --jq .[]' ]]
    printf '%s\n' "$RELEASES"
    exit "$GH_STATUS"
    ;;
  'release create')
    [[ "$*" == "release create $RELEASE_TAG --verify-tag --draft --title $RELEASE_TITLE --generate-notes --latest=false" ]]
    ;;
  'release download')
    [[ "$3" == "$RELEASE_TAG" && "$4" == '--dir' && $# == 5 ]]
    [[ "${FAIL_DOWNLOAD:-0}" == 0 ]] || exit 1
    cp -a "$STATE/published/." "$5/"
    ;;
  'release upload')
    [[ "${3}" == "$RELEASE_TAG" && "${@: -1}" == '--clobber' ]]
    [[ "${FAIL_UPLOAD:-0}" == 0 ]] || exit 1
    mkdir "$STATE/uploaded"
    shift 3
    for asset in "$@"; do
      [[ "$asset" == '--clobber' ]] || cp "$asset" "$STATE/uploaded/"
    done
    ;;
  'release edit')
    [[ "$*" == "release edit $RELEASE_TAG --draft=false --latest=false" ]]
    ;;
  *) exit 1 ;;
esac
`)
	return f
}

func nativeWorkflowScript(t *testing.T, file, job, stepID string) string {
	t.Helper()
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct{ ID, Run string }
		}
	}
	readCIYAML(t, "..", "workflows/"+file, &workflow)
	for _, step := range workflow.Jobs[job].Steps {
		if step.ID == stepID && step.Run != "" {
			return step.Run
		}
	}
	t.Fatalf("%s lacks %s step %s", file, job, stepID)
	return ""
}

func (f *nativeFixture) write(t *testing.T, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(data), 0700); err != nil {
		t.Fatal(err)
	}
}

func (f *nativeFixture) read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func (f *nativeFixture) run(t *testing.T) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", f.script)
	cmd.Dir = f.artifacts
	for key, value := range f.env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func TestCINativeBinaryPublication(t *testing.T) {
	for _, draft := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing draft %t", draft), func(t *testing.T) {
			f := newNativeFixture(t)
			if draft {
				f.env["RELEASES"] = `{"tag_name":"v0.1.0","draft":true}`
			}
			if output, err := f.run(t); err != nil {
				t.Fatalf("publish failed: %v: %s", err, output)
			}
			log := f.read(t, "gh.log")
			if got := strings.Contains(log, "release create"); got == draft {
				t.Errorf("create release = %t, existing draft = %t", got, draft)
			}
			assets := append(append([]string{}, rootBinaryNames...), "LICENSE")
			var checksums strings.Builder
			for _, name := range assets {
				got := f.read(t, "uploaded/"+name)
				want := f.read(t, "artifacts/"+name)
				if got == "" || got != want {
					t.Errorf("uploaded %s = %q, want %q", name, got, want)
				}
				fmt.Fprintf(&checksums, "%x  %s\n", sha256.Sum256([]byte(want)), name)
			}
			if got := f.read(t, "uploaded/checksums.txt"); got != checksums.String() {
				t.Errorf("checksums = %q, want %q", got, checksums.String())
			}
			entries, err := os.ReadDir(filepath.Join(f.dir, "uploaded"))
			if err != nil || len(entries) != len(rootBinaryNames)+2 {
				t.Fatalf("root release must contain Commander and Admin binaries, license and checksums: %v, %v", entries, err)
			}
			if !strings.HasSuffix(log, "release edit v0.1.0 --draft=false --latest=false\n") {
				t.Errorf("release must await final latest promotion: %s", log)
			}
		})
	}
}

func TestCINativeBinaryPublicationRejectsInvalidAssets(t *testing.T) {
	for _, mode := range []string{"missing", "empty", "directory", "symlink", "extra", "wrong name", "missing Admin"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeFixture(t)
			path := filepath.Join(f.artifacts, commanderBinaryNames[0])
			if mode == "missing Admin" {
				path = filepath.Join(f.artifacts, "commander-admin_linux_amd64")
			}
			if mode != "extra" {
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
				err = os.Symlink("../LICENSE", path)
			case "extra":
				f.write(t, "artifacts/commander_linux_386", "unexpected")
			case "wrong name":
				f.write(t, "artifacts/commander_v0.1.0_linux_amd64", "old name")
			}
			if err != nil {
				t.Fatal(err)
			}
			if output, err := f.run(t); err == nil {
				t.Fatalf("invalid assets published: %s", output)
			}
			if got := f.read(t, "gh.log"); got != "" {
				t.Errorf("invalid assets reached GitHub: %s", got)
			}
		})
	}
}

func TestCINativeBinaryPublicationFailures(t *testing.T) {
	for _, mode := range []string{"API failure", "upload failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeFixture(t)
			switch mode {
			case "API failure":
				f.env["GH_STATUS"] = "1"
			case "upload failure":
				f.env["FAIL_UPLOAD"] = "1"
			}
			if output, err := f.run(t); err == nil {
				t.Fatalf("publication failure ignored: %s", output)
			}
			log := f.read(t, "gh.log")
			if strings.Contains(log, "release edit") {
				t.Errorf("failed publication finalized release: %s", log)
			}
			if mode != "upload failure" && strings.Contains(log, "release upload") {
				t.Errorf("publication must not upload after lookup failure: %s", log)
			}
		})
	}
}

func TestCIPublishedReleaseRetry(t *testing.T) {
	for _, name := range []string{"root", "spawner"} {
		t.Run(name, func(t *testing.T) {
			newFixture := func(t *testing.T) *nativeFixture {
				if name == "root" {
					return newNativeFixture(t)
				}
				return newAgentPackageFixture(t, name)
			}
			original := newFixture(t)
			if output, err := original.run(t); err != nil {
				t.Fatalf("initial publication failed: %v: %s", err, output)
			}
			entries, err := os.ReadDir(filepath.Join(original.dir, "uploaded"))
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"identical", "changed bytes", "changed checksums", "missing asset", "extra asset", "download failure"} {
				t.Run(mode, func(t *testing.T) {
					f := newFixture(t)
					f.env["RELEASES"] = fmt.Sprintf(`{"tag_name":%q,"draft":false}`, f.env["RELEASE_TAG"])
					if err := os.Mkdir(filepath.Join(f.dir, "published"), 0700); err != nil {
						t.Fatal(err)
					}
					for _, entry := range entries {
						f.write(t, "published/"+entry.Name(), original.read(t, "uploaded/"+entry.Name()))
					}
					switch mode {
					case "changed bytes":
						asset := "agent.yaml"
						if name == "root" {
							asset = nativeBinaryNames[0]
						}
						f.write(t, "published/"+asset, "different release bytes")
					case "changed checksums":
						f.write(t, "published/checksums.txt", "different checksums")
					case "missing asset":
						if err := os.Remove(filepath.Join(f.dir, "published", entries[0].Name())); err != nil {
							t.Fatal(err)
						}
					case "extra asset":
						f.write(t, "published/extra", "unexpected asset")
					case "download failure":
						f.env["FAIL_DOWNLOAD"] = "1"
					}
					if output, err := f.run(t); (err != nil) != (mode != "identical") {
						t.Fatalf("published release retry = %v: %s", err, output)
					}
					log := f.read(t, "gh.log")
					if !strings.Contains(log, "release download "+f.env["RELEASE_TAG"]) {
						t.Errorf("retry must read the published assets: %s", log)
					}
					for _, mutation := range []string{"release create", "release upload", "release edit"} {
						if strings.Contains(log, mutation) {
							t.Errorf("published release retry must not write: %s", log)
						}
					}
				})
			}
		})
	}
}

func TestCINativeBinaryBuildNames(t *testing.T) {
	for _, platform := range []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64", "windows_amd64", "windows_arm64"} {
		t.Run(platform, func(t *testing.T) {
			f := &nativeFixture{dir: t.TempDir()}
			f.artifacts = f.dir
			f.script = nativeWorkflowScript(t, "build-binaries.yml", "build", "build")
			goos, goarch, _ := strings.Cut(platform, "_")
			f.env = map[string]string{
				"PATH": f.dir + ":/usr/bin:/bin", "STATE": f.dir,
				"GOOS": goos, "GOARCH": goarch, "VERSION": "v0.1.0",
			}
			f.write(t, "go", `#!/bin/bash
set -euo pipefail
[[ "$1" == build ]]
case "${@: -1}" in
  ./apps/playground/cmd/commander)
    [[ " $* " == *' -tags embed_ui '* ]]
    prefix=commander
    ;;
  ./apps/admin/cmd/commander-admin)
    [[ " $* " == *' -tags embed_ui '* ]]
    prefix=commander-admin
    ;;
  ./apps/agents/spawner/cmd) prefix=commander-spawner-agent ;;
  ./apps/agents/builder/cmd) prefix=commander-builder-agent ;;
  *) exit 1 ;;
esac
while [[ "$1" != '-o' ]]; do shift; done
[[ "$2" == "dist/${prefix}_${GOOS}_${GOARCH}"* ]]
cat > "$2" <<'BINARY'
#!/bin/bash
[[ "$*" == '--help' ]]
printf 'help called\n' > "$STATE/help-${0##*/}"
BINARY
chmod +x "$2"
`)
			if output, err := f.run(t); err != nil {
				t.Fatalf("build failed: %v: %s", err, output)
			}
			entries, err := os.ReadDir(filepath.Join(f.dir, "dist"))
			if err != nil || len(entries) != 4 {
				t.Fatalf("build must produce Commander, Admin, spawner and builder binaries: %v, %v", entries, err)
			}
			for _, prefix := range []string{"commander", "commander-admin", "commander-spawner-agent", "commander-builder-agent"} {
				name := prefix + "_" + platform
				if goos == "windows" {
					name += ".exe"
				}
				if info, err := os.Stat(filepath.Join(f.dir, "dist", name)); err != nil || !info.Mode().IsRegular() {
					t.Errorf("missing directly downloadable %s: %v", name, err)
				}
				if got := f.read(t, "help-"+name); got != "help called\n" {
					t.Errorf("%s native executable smoke check did not run: %q", name, got)
				}
			}
		})
	}
}
