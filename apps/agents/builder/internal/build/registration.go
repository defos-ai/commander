package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
)

// ValidateRegistration inspects the default constructor's registered actions,
// independently of release summaries and without starting discovery or serving.
func ValidateRegistration(ctx context.Context, goTool, source, scratch string, env []string, metadata PackageMetadata, expected []actions.Definition) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.CreateTemp(scratch, "action-contracts-*.go")
	if err != nil {
		return errors.New("prepare generated action contract inspection failed")
	}
	probe := file.Name()
	defer os.Remove(probe)
	_, writeErr := file.WriteString(fmt.Sprintf(registrationProbe, metadata.ModulePath))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("prepare generated action contract inspection failed")
	}
	// Full schemas need more room than release summaries. Never return raw child
	// output or parse errors, which can contain generated code or sensitive data.
	output, err := readCommandOutput(ctx, "Generated action contracts", goTool, source, env, 1<<20, "run", probe)
	if err != nil {
		return err
	}
	var registered struct {
		AgentType string               `json:"agent_type"`
		Actions   []actions.Definition `json:"actions"`
	}
	if err := decodeReleaseJSON(output, &registered, true); err != nil {
		return errors.New("generated action contracts must contain one valid agent_type and actions JSON document")
	}
	if registered.AgentType != metadata.AgentType {
		return errors.New("registered agent type does not match the generated package identity")
	}
	return CompareActions(expected, registered.Actions)
}

const registrationProbe = `package main

import (
  "context"
  "encoding/json"
  "errors"
  "os"

  generated %q
  "github.com/defos-ai/commander/framework/agent"
  "github.com/defos-ai/commander/framework/domain/artifacts"
)

type unavailableFiles struct{}

func (unavailableFiles) Put(context.Context, string, artifacts.FileSource) (artifacts.ArtifactFile, error) {
  return artifacts.ArtifactFile{}, errors.New("artifact storage is unavailable during inspection")
}

func (unavailableFiles) Get(context.Context, string, artifacts.ArtifactFile) ([]byte, error) {
  return nil, errors.New("artifact storage is unavailable during inspection")
}

// Project wire metadata so discovery state never participates in comparison.
type definition struct {
  Name string ` + "`json:\"name\"`" + `
  Description string ` + "`json:\"description\"`" + `
  SessionMode string ` + "`json:\"session_mode,omitempty\"`" + `
  RequiredSecrets []string ` + "`json:\"required_secrets,omitempty\"`" + `
  InputSchema json.RawMessage ` + "`json:\"input_schema\"`" + `
  OutputSchema json.RawMessage ` + "`json:\"output_schema\"`" + `
}

func main() {
  if err := inspect(); err != nil { os.Exit(1) }
}

func inspect() error {
  instance, err := generated.NewAgent(agent.AgentConfig{
    Server: agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "contract-check"},
    ArtifactFileStore: unavailableFiles{},
  })
  if err != nil { return err }
  if instance == nil { return errors.New("agent constructor returned nil") }
  snapshot := instance.Describe()
  definitions := make([]definition, 0, len(snapshot.Actions))
  for _, action := range snapshot.Actions {
    raw, err := json.Marshal(action)
    if err != nil { return err }
    var value definition
    if err := json.Unmarshal(raw, &value); err != nil { return err }
    definitions = append(definitions, value)
  }
  return json.NewEncoder(os.Stdout).Encode(struct {
    AgentType string ` + "`json:\"agent_type\"`" + `
    Actions []definition ` + "`json:\"actions\"`" + `
  }{AgentType: string(snapshot.Type), Actions: definitions})
}
`
