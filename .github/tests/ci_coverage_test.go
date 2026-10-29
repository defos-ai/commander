package release_test

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestCICoverage(t *testing.T) {
	const condition = "contains(fromJSON(inputs.changes), 'agent')"
	const filters = "agent:\n  - &owner 'apps/agents/example/**'\nconsumer:\n  - *owner\n"
	cases := []struct {
		name      string
		manifest  string
		filters   string
		jobIf     string
		stepIf    string
		directory string
		run       string
		wantError string
	}{
		{name: "module test", run: "go test -race ./apps/agents/example/..."},
		{name: "host tests", run: "go test ./apps/agents/example/tests/host/..."},
		{name: "working directory", directory: "apps/agents/example", run: "go test ./..."},
		{name: "job condition", jobIf: condition, directory: "apps/agents/example", run: "go test ./..."},
		{name: "missing owner", filters: "fallback: ['apps/**']", run: "go test ./apps/agents/example/...", wantError: "no owner filter"},
		{name: "missing command", wantError: "no CI test command"},
		{name: "commented command", run: "# go test ./apps/agents/example/...", wantError: "no CI test command"},
		{name: "echoed command", run: "echo go test ./apps/agents/example/...", wantError: "no CI test command"},
		{name: "path in comment", run: "go test ./other/... # ./apps/agents/example/...", wantError: "no CI test command"},
		{name: "path in another command", run: "go test ./other/... && echo ./apps/agents/example/...", wantError: "no CI test command"},
		{name: "vet only", run: "go vet ./apps/agents/example/...", wantError: "no CI test command"},
		{name: "similarly named module", run: "go test ./apps/agents/example-other/...", wantError: "no CI test command"},
		{name: "parent wildcard does not cross modules", run: "go test ./apps/agents/...", wantError: "no CI test command"},
		{name: "consumer alias is not owner", stepIf: "contains(fromJSON(inputs.changes), 'consumer')", run: "go test ./apps/agents/example/...", wantError: "no CI test command"},
		{name: "wrong job filter", jobIf: "contains(fromJSON(inputs.changes), 'consumer')", stepIf: condition, run: "go test ./apps/agents/example/...", wantError: "no CI test command"},
		{name: "disabled step", jobIf: condition, stepIf: "false", run: "go test ./apps/agents/example/...", wantError: "no CI test command"},
		{name: "negated owner", stepIf: "!contains(fromJSON(inputs.changes), 'agent')", run: "go test ./apps/agents/example/...", wantError: "no CI test command"},
		{name: "owner requires full run", stepIf: "inputs.all && contains(fromJSON(inputs.changes), 'agent')", run: "go test ./apps/agents/example/...", wantError: "no CI test command"},
		{name: "release-only step", jobIf: condition, stepIf: "inputs.all", run: "go test ./apps/agents/example/...", wantError: "no CI test command"},
		{name: "frontend", manifest: "apps/agents/example/package.json", directory: "apps/agents/example", run: "npm test"},
		{name: "frontend script", manifest: "apps/agents/example/package.json", directory: "apps/agents/example", run: "npm run test"},
		{name: "frontend build only", manifest: "apps/agents/example/package.json", directory: "apps/agents/example", run: "npm run build", wantError: "no CI test command"},
		{name: "frontend wrong directory", manifest: "apps/agents/example/package.json", run: "npm test", wantError: "no CI test command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.manifest == "" {
				tc.manifest = "apps/agents/example/go.mod"
			}
			if tc.filters == "" {
				tc.filters = filters
			}
			if tc.jobIf == "" && tc.stepIf == "" {
				tc.stepIf = condition
			}
			var paths map[string]yaml.Node
			if err := yaml.Unmarshal([]byte(tc.filters), &paths); err != nil {
				t.Fatal(err)
			}
			job := ciJob{If: tc.jobIf, Steps: []ciStep{{If: tc.stepIf, Run: tc.run}}}
			job.Defaults.Run.Directory = tc.directory
			workflow := ciWorkflow{Jobs: map[string]ciJob{"test": job}}
			err := checkCICoverage(tc.manifest, paths, []ciWorkflow{workflow})
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("got %v, want an error containing %q", err, tc.wantError)
			}
		})
	}
}
