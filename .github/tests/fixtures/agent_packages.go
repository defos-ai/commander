package fixtures

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func NewAgentPackage(t *testing.T, agent string) *Native {
	t.Helper()
	f := NewNative(t)
	f.env["AGENT"] = agent
	f.env["TAG"] = "apps/agents/" + agent + "/v0.1.0"
	f.env["RELEASE_TAG"] = f.env["TAG"]
	f.env["RELEASE_TITLE"] = "Commander " + agent + " agent v0.1.0"
	f.artifacts = f.dir
	f.script = WorkflowScript(t, "push-binaries.yml", "agents", "stage")
	if output, err := f.Run(t); err != nil {
		t.Fatalf("stage failed: %v: %s", err, output)
	}
	f.artifacts = filepath.Join(f.dir, "package")
	f.Write(t, "package/agent.yaml", fmt.Sprintf("schema_version: 1\ntype: %s-agent\nversion: v0.1.0\ndocker:\n  image: ghcr.io/defos-ai/commander-%s-agent@sha256:%s\n", agent, agent, strings.Repeat("a", 64)))
	f.script = WorkflowScript(t, "push-binaries.yml", "agents", "publish")
	return f
}
