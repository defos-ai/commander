package release_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type ciDefaults struct {
	Run struct {
		Directory string `yaml:"working-directory"`
	}
}

type ciStep struct {
	If        string
	Run       string
	Directory string `yaml:"working-directory"`
}

type ciJob struct {
	If       string
	Uses     string
	Defaults ciDefaults
	Steps    []ciStep
}

type ciWorkflow struct {
	Defaults ciDefaults
	Jobs     map[string]ciJob
}

var ciFilterReference = regexp.MustCompile(`contains\(fromJSON\(inputs\.changes\),\s*['"]([^'"]+)['"]\)|steps\.paths\.outputs\.([\w-]+)`)
var ciOwnerCondition = regexp.MustCompile(`^contains\(fromJSON\(inputs\.changes\),\s*['"]([\w-]+)['"]\)$`)

func TestCIComponents(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(source)))
	var filters map[string]yaml.Node
	readCIYAML(t, root, ".github/test-paths.yml", &filters)
	workflows := readCIWorkflows(t, root, filters)
	var work workspace
	data := runGo(t, root, "work", "edit", "-json", filepath.Join(root, "go.work"))
	if err := json.Unmarshal(data, &work); err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, entry := range work.Use {
		registered[filepath.ToSlash(filepath.Clean(entry.DiskPath))] = true
	}
	for _, manifest := range discoverCIComponents(t, root) {
		directory := path.Dir(manifest)
		t.Run(directory, func(t *testing.T) {
			if path.Base(manifest) == "go.mod" && !registered[directory] {
				t.Errorf("%s is missing from go.work; add it with go work use ./%s", directory, directory)
			}
			if err := checkCICoverage(manifest, filters, workflows); err != nil {
				t.Error(err)
			}
		})
	}
}

func discoverCIComponents(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", "framework", "providers", "apps")
	cmd.Dir = root
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifests []string
	for _, file := range strings.Split(string(data), "\x00") {
		if strings.Contains(file, "/node_modules/") || strings.Contains(file, "/vendor/") || strings.Contains(file, "/testdata/") {
			continue
		}
		if path.Base(file) == "go.mod" || path.Base(file) == "package.json" {
			// The index still contains unstaged deletions during local renames.
			if _, err := os.Stat(filepath.Join(root, file)); os.IsNotExist(err) {
				continue
			} else if err != nil {
				t.Fatal(err)
			}
			manifests = append(manifests, file)
		}
	}
	slices.Sort(manifests)
	return slices.Compact(manifests)
}

func readCIYAML(t *testing.T, root, file string, target any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
}

func readCIWorkflows(t *testing.T, root string, filters map[string]yaml.Node) []ciWorkflow {
	t.Helper()
	var workflows []ciWorkflow
	seen := map[string]bool{}
	var visit func(string)
	visit = func(file string) {
		if seen[file] {
			return
		}
		seen[file] = true
		var document yaml.Node
		readCIYAML(t, root, file, &document)
		var inspect func(*yaml.Node)
		inspect = func(node *yaml.Node) {
			if node.Kind == yaml.ScalarNode {
				for _, name := range ciFilterNames(node.Value) {
					if _, exists := filters[name]; !exists {
						t.Errorf("%s:%d references unknown filter %q; define it in .github/test-paths.yml", file, node.Line, name)
					}
				}
			}
			for _, child := range node.Content {
				inspect(child)
			}
		}
		inspect(&document)
		var workflow ciWorkflow
		if err := document.Decode(&workflow); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		workflows = append(workflows, workflow)
		for _, job := range workflow.Jobs {
			if strings.HasPrefix(job.Uses, "./.github/workflows/") && strings.TrimSpace(job.If) != "false" {
				visit(strings.TrimPrefix(job.Uses, "./"))
			}
		}
	}
	visit(".github/workflows/test.yml")
	return workflows
}

func ciFilterNames(expression string) []string {
	var names []string
	for _, match := range ciFilterReference.FindAllStringSubmatch(expression, -1) {
		if match[1] != "" {
			names = append(names, match[1])
		} else if match[2] != "changes" {
			names = append(names, match[2])
		}
	}
	return names
}

func declaresCIPath(node *yaml.Node, pattern string) bool {
	if node.Kind == yaml.ScalarNode {
		return node.Value == pattern
	}
	// A downstream alias does not declare ownership of the referenced component.
	for _, child := range node.Content {
		if declaresCIPath(child, pattern) {
			return true
		}
	}
	return false
}

func checkCICoverage(manifest string, filters map[string]yaml.Node, workflows []ciWorkflow) error {
	directory := path.Dir(manifest)
	var owners []string
	for name, node := range filters {
		if declaresCIPath(&node, directory+"/**") {
			owners = append(owners, name)
		}
	}
	if len(owners) == 0 {
		return fmt.Errorf("%s has no owner filter; declare %q in .github/test-paths.yml", directory, directory+"/**")
	}
	for _, workflow := range workflows {
		for _, job := range workflow.Jobs {
			for _, step := range job.Steps {
				workingDirectory := step.Directory
				if workingDirectory == "" {
					workingDirectory = job.Defaults.Run.Directory
				}
				if workingDirectory == "" {
					workingDirectory = workflow.Defaults.Run.Directory
				}
				if !ciCommandChecks(manifest, workingDirectory, step.Run) {
					continue
				}
				jobFilters, stepFilters := ciFilterNames(job.If), ciFilterNames(step.If)
				for _, owner := range owners {
					if (slices.Contains(jobFilters, owner) || slices.Contains(stepFilters, owner)) &&
						ciSelectedFor(job.If, owner) && ciSelectedFor(step.If, owner) {
						return nil
					}
				}
			}
		}
	}
	return fmt.Errorf("%s has no CI validation command selected by its owner filter; add a build, vet or integration-test step to the test workflow", directory)
}

func ciSelectedFor(expression, owner string) bool {
	expression = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(expression), "${{"), "}}"))
	if expression == "" {
		return true
	}
	selected := false
	for _, term := range strings.Split(expression, "||") {
		term = strings.TrimSpace(term)
		if term == "inputs.all" {
			continue
		}
		match := ciOwnerCondition.FindStringSubmatch(term)
		if match == nil {
			return false
		}
		selected = selected || match[1] == owner
	}
	return selected
}

func ciCommandChecks(manifest, workingDirectory, script string) bool {
	directory := path.Dir(manifest)
	script = strings.ReplaceAll(script, "\\\n", " ")
	for _, line := range strings.Split(script, "\n") {
		words := strings.Fields(line)
		for i, word := range words {
			if strings.HasPrefix(word, "#") || slices.Contains([]string{"&&", "||", ";", "|"}, word) {
				words = words[:i]
				break
			}
		}
		if len(words) < 2 {
			continue
		}
		if path.Base(manifest) == "package.json" {
			if path.Clean(workingDirectory) == directory && words[0] == "npm" &&
				(words[1] == "test" || len(words) >= 3 && words[1] == "run" && slices.Contains([]string{"test", "test:e2e", "typecheck", "build"}, words[2])) {
				return true
			}
			continue
		}
		if words[0] != "go" || !slices.Contains([]string{"test", "vet", "build"}, words[1]) {
			continue
		}
		for _, word := range words[2:] {
			word = strings.Trim(word, "\"'")
			if !strings.HasPrefix(word, "./") && !strings.HasPrefix(word, "../") && word != "." {
				continue
			}
			target := path.Join(workingDirectory, strings.TrimSuffix(word, "/..."))
			if target == directory || strings.HasPrefix(target, directory+"/") {
				return true
			}
		}
	}
	return false
}
