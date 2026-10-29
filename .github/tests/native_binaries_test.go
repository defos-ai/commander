package release_test

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fixtures "github.com/defos-ai/commander/.github/tests/fixtures"
)

func TestCINativeTestTargets(t *testing.T) {
	var workflow ciWorkflow
	readCIYAML(t, "..", "workflows/build-binaries.yml", &workflow)
	var targets int
	for _, step := range workflow.Jobs["build"].Steps {
		for _, line := range strings.Split(step.Run, "\n") {
			words := strings.Fields(line)
			if len(words) < 3 || words[0] != "go" || words[1] != "test" {
				continue
			}
			for _, target := range words[2:] {
				if !strings.HasPrefix(target, "./") {
					continue
				}
				targets++
				directory := filepath.Join("../..", strings.TrimSuffix(target, "/..."))
				found := false
				err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if entry.IsDir() && path != directory && !strings.HasSuffix(target, "/...") {
						return filepath.SkipDir
					}
					if !entry.IsDir() && strings.HasSuffix(entry.Name(), "_test.go") {
						found = true
						return filepath.SkipAll
					}
					return nil
				})
				if err != nil || !found {
					t.Errorf("native workflow target %s must contain retained Go tests: %v", target, err)
				}
			}
		}
	}
	if targets == 0 {
		t.Fatal("native workflow must run retained Go test suites")
	}
}

func TestCINativeBinaryPublication(t *testing.T) {
	for _, draft := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing draft %t", draft), func(t *testing.T) {
			f := fixtures.NewNative(t)
			if draft {
				f.SetEnvironment("RELEASES", `{"tag_name":"v0.1.0","draft":true}`)
			}

			output, err := f.Run(t)

			if err != nil {
				t.Fatalf("publish failed: %v: %s", err, output)
			}
			log := f.Read(t, "gh.log")
			if got := strings.Contains(log, "release create"); got == draft {
				t.Errorf("create release = %t, existing draft = %t", got, draft)
			}
			assets := append(append([]string{}, fixtures.RootBinaryNames()...), "LICENSE")
			var checksums strings.Builder
			for _, name := range assets {
				got := f.Read(t, "uploaded/"+name)
				want := f.Read(t, "artifacts/"+name)
				if got == "" || got != want {
					t.Errorf("uploaded %s = %q, want %q", name, got, want)
				}
				fmt.Fprintf(&checksums, "%x  %s\n", sha256.Sum256([]byte(want)), name)
			}
			if got := f.Read(t, "uploaded/checksums.txt"); got != checksums.String() {
				t.Errorf("checksums = %q, want %q", got, checksums.String())
			}
			entries, err := os.ReadDir(filepath.Join(f.Dir(), "uploaded"))
			if err != nil || len(entries) != len(fixtures.RootBinaryNames())+2 {
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
			f := fixtures.NewNative(t)
			path := filepath.Join(f.Artifacts(), fixtures.CommanderBinaryNames()[0])
			if mode == "missing Admin" {
				path = filepath.Join(f.Artifacts(), "commander-admin_linux_amd64")
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
				f.Write(t, "artifacts/commander_linux_386", "unexpected")
			case "wrong name":
				f.Write(t, "artifacts/commander_v0.1.0_linux_amd64", "old name")
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

func TestCINativeBinaryPublicationFailures(t *testing.T) {
	for _, mode := range []string{"API failure", "upload failure"} {
		t.Run(mode, func(t *testing.T) {
			f := fixtures.NewNative(t)
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

func TestCIPublishedReleaseRetry(t *testing.T) {
	for _, name := range []string{"root", "spawner"} {
		t.Run(name, func(t *testing.T) {
			newFixture := func(t *testing.T) *fixtures.Native {
				if name == "root" {
					return fixtures.NewNative(t)
				}
				return fixtures.NewAgentPackage(t, name)
			}
			original := newFixture(t)
			if output, err := original.Run(t); err != nil {
				t.Fatalf("initial publication failed: %v: %s", err, output)
			}
			entries, err := os.ReadDir(filepath.Join(original.Dir(), "uploaded"))
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"identical", "changed bytes", "changed checksums", "missing asset", "extra asset", "download failure"} {
				t.Run(mode, func(t *testing.T) {
					f := newFixture(t)
					f.SetEnvironment("RELEASES", fmt.Sprintf(`{"tag_name":%q,"draft":false}`, f.Environment("RELEASE_TAG")))
					if err := os.Mkdir(filepath.Join(f.Dir(), "published"), 0700); err != nil {
						t.Fatal(err)
					}
					for _, entry := range entries {
						f.Write(t, "published/"+entry.Name(), original.Read(t, "uploaded/"+entry.Name()))
					}
					switch mode {
					case "changed bytes":
						asset := "agent.yaml"
						if name == "root" {
							asset = fixtures.NativeBinaryNames()[0]
						}
						f.Write(t, "published/"+asset, "different release bytes")
					case "changed checksums":
						f.Write(t, "published/checksums.txt", "different checksums")
					case "missing asset":
						if err := os.Remove(filepath.Join(f.Dir(), "published", entries[0].Name())); err != nil {
							t.Fatal(err)
						}
					case "extra asset":
						f.Write(t, "published/extra", "unexpected asset")
					case "download failure":
						f.SetEnvironment("FAIL_DOWNLOAD", "1")
					}
					if output, err := f.Run(t); (err != nil) != (mode != "identical") {
						t.Fatalf("published release retry = %v: %s", err, output)
					}
					log := f.Read(t, "gh.log")
					if !strings.Contains(log, "release download "+f.Environment("RELEASE_TAG")) {
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
			f := fixtures.NewNativeBuild(t, platform)
			goos, _, _ := strings.Cut(platform, "_")

			output, err := f.Run(t)

			if err != nil {
				t.Fatalf("build failed: %v: %s", err, output)
			}
			entries, err := os.ReadDir(filepath.Join(f.Dir(), "dist"))
			if err != nil || len(entries) != 4 {
				t.Fatalf("build must produce Commander, Admin, spawner and builder binaries: %v, %v", entries, err)
			}
			for _, prefix := range []string{"commander", "commander-admin", "commander-spawner-agent", "commander-builder-agent"} {
				name := prefix + "_" + platform
				if goos == "windows" {
					name += ".exe"
				}
				if info, err := os.Stat(filepath.Join(f.Dir(), "dist", name)); err != nil || !info.Mode().IsRegular() {
					t.Errorf("missing directly downloadable %s: %v", name, err)
				}
				if got := f.Read(t, "help-"+name); got != "help called\n" {
					t.Errorf("%s native executable smoke check did not run: %q", name, got)
				}
			}
		})
	}
}
