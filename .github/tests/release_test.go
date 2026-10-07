package release_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

const modulePrefix = "github.com/defos-ai/commander/"

type moduleVersion struct {
	Path    string
	Version string
}

type workspace struct {
	Use     []struct{ DiskPath string }
	Replace []struct {
		Old moduleVersion
		New moduleVersion
	}
}

type module struct {
	Module  moduleVersion
	Require []moduleVersion
	Replace []json.RawMessage
	Exclude []json.RawMessage
}

func TestReleaseVersion(t *testing.T) {
	version := os.Getenv("RELEASE_VERSION")
	if !regexp.MustCompile(`^v[01]\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(version) || version == "v0.0.0" {
		t.Fatal("set RELEASE_VERSION to a stable v0 or v1 version other than v0.0.0, such as v0.1.0")
	}
}

func TestModuleLayout(t *testing.T) {
	root, work, modules := readModules(t)
	for directory, manifest := range modules {
		t.Run(directory, func(t *testing.T) {
			if manifest.Module.Path != modulePrefix+filepath.ToSlash(directory) {
				t.Errorf("module path must match its directory: %s", manifest.Module.Path)
			}
			if len(manifest.Replace) != 0 || len(manifest.Exclude) != 0 {
				t.Error("keep dependency replacements and exclusions out of released go.mod files")
			}
			for _, dependency := range manifest.Require {
				if !isCommanderModule(dependency.Path) {
					continue
				}
				target := strings.TrimPrefix(dependency.Path, modulePrefix)
				if _, ok := modules[filepath.FromSlash(target)]; !ok {
					t.Errorf("dependency %s is not a workspace module", dependency.Path)
				}
				mapped := false
				for _, replacement := range work.Replace {
					if replacement.Old == dependency && replacement.New.Version == "" {
						mapped = filepath.Join(root, replacement.New.Path) == filepath.Join(root, target)
					}
				}
				if !mapped {
					t.Errorf("go.work must map %s@%s to ./%s", dependency.Path, dependency.Version, target)
				}
			}
		})
	}
}

func TestReleaseDependencies(t *testing.T) {
	_, _, modules := readModules(t)
	version := os.Getenv("RELEASE_VERSION")
	for directory, manifest := range modules {
		t.Run(directory, func(t *testing.T) {
			for _, dependency := range manifest.Require {
				if isCommanderModule(dependency.Path) && dependency.Version != version {
					t.Errorf("go.mod requires %s@%s; pin it to release version %s before tagging", dependency.Path, dependency.Version, version)
				}
			}
		})
	}
}

func TestModulesCompile(t *testing.T) {
	root, work, _ := readModules(t)
	for _, entry := range work.Use {
		t.Run(entry.DiskPath, func(t *testing.T) {
			directory := filepath.Join(root, entry.DiskPath)
			runGo(t, directory, "build", "./...")
			// Compile service-backed tests without starting their test binaries.
			runGo(t, directory, "test", "-exec=true", "./...")
			runGo(t, directory, "vet", "./...")
		})
	}
}

func readModules(t *testing.T) (string, workspace, map[string]module) {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(source)))
	var work workspace
	decode(t, runGo(t, root, "work", "edit", "-json"), &work)
	if len(work.Use) == 0 {
		t.Fatal("go.work must list the modules to release")
	}
	modules := make(map[string]module)
	for _, entry := range work.Use {
		directory := filepath.Clean(entry.DiskPath)
		if !filepath.IsLocal(directory) || directory == "." {
			t.Fatalf("workspace module must be a repository subdirectory: %s", directory)
		}
		if entry.DiskPath != filepath.ToSlash(directory) && entry.DiskPath != "./"+filepath.ToSlash(directory) {
			t.Fatalf("workspace module path must be canonical for tagging: %s", entry.DiskPath)
		}
		if _, exists := modules[directory]; exists {
			t.Fatalf("duplicate workspace module: %s", directory)
		}
		var manifest module
		decode(t, runGo(t, filepath.Join(root, directory), "mod", "edit", "-json"), &manifest)
		modules[directory] = manifest
	}
	return root, work, modules
}

func isCommanderModule(path string) bool {
	return path == strings.TrimSuffix(modulePrefix, "/") || strings.HasPrefix(path, modulePrefix)
}

func decode(t *testing.T, data []byte, value any) {
	t.Helper()
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(err)
	}
}

func runGo(t *testing.T, directory string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = directory
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: go %s: %v\n%s", directory, strings.Join(args, " "), err, output)
	}
	return output
}
