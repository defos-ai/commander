package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/defos-ai/commander/apps/agents/builder/internal/build"
	"github.com/defos-ai/commander/apps/agents/builder/internal/process"
	"github.com/defos-ai/commander/apps/agents/builder/internal/progress"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
)

const BuildAgentName = "build-agent"
const BuildAgentDescription = "Implement the requested Commander agent with Codex from explicit action contracts and instructions in a workspace returned by checkout-template on this builder. Each action needs a unique name, description, input/output object schemas and any required secret names. Run Go tests and build ./cmd, then verify registered actions, generated identity and release structure before returning its source archive and package metadata. Contract checks compare names, descriptions, schemas and required secret names without discovery or execution. Temporary assets verify manifest generation; this does not test Docker, six native platforms or GitHub Actions. Keep checkout and build in one operation with the same-agent rule. Requires the managed CODEX_API_KEY secret; never include keys in instructions or contracts. Does not publish or launch the agent."

type BuildAgentInput struct {
	WorkspaceID  string                   `json:"workspace_id"`
	Instructions string                   `json:"instructions"`
	Actions      []actionmodel.Definition `json:"actions"`
}

func (i BuildAgentInput) Validate() error {
	if !workspaceIDPattern.MatchString(i.WorkspaceID) || !utf8.ValidString(i.Instructions) || strings.TrimSpace(i.Instructions) == "" || len(i.Instructions) > 32000 || strings.ContainsRune(i.Instructions, 0) {
		return errors.New("input requires a checkout workspace_id and nonblank instructions of at most 32000 bytes")
	}
	return build.ValidateActions(i.Actions)
}

type BuildAgentOutput struct {
	WorkspaceID string `json:"workspace_id"`
	SourceFiles int    `json:"source_files"`
	AgentType   string `json:"agent_type"`
	ModulePath  string `json:"module_path"`
	Binary      string `json:"binary"`
}

type buildTools struct{ codex, goTool string }

type BuildAgent struct {
	*action.Base
	workspaceDir string
	tools        atomic.Pointer[buildTools]
	building     sync.Mutex
}

func NewBuildAgent(workspaceDir string) (*BuildAgent, error) {
	if strings.TrimSpace(workspaceDir) == "" {
		return nil, errors.New("workspace directory must not be blank")
	}
	root, err := filepath.Abs(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("workspace directory: %w", err)
	}
	output, err := action.SchemaFor[BuildAgentOutput]()
	if err != nil {
		return nil, err
	}
	return &BuildAgent{workspaceDir: root, Base: action.NewBase(actionmodel.Action{
		Name:            BuildAgentName,
		Description:     BuildAgentDescription,
		RequiredSecrets: []string{"CODEX_API_KEY"},
		InputSchema: json.RawMessage(`{
  "type":"object",
  "properties":{
    "workspace_id":{"type":"string","pattern":"^checkout-[0-9]+$"},
    "instructions":{"type":"string","minLength":1,"maxLength":32000,"pattern":"\\S"},
    "actions":{
      "type":"array","minItems":1,"maxItems":100,
      "description":"Requested action contracts. Required secret names belong to the generated agent; supply no secret values.",
      "items":{
        "type":"object","additionalProperties":false,
        "properties":{
          "name":{"type":"string","maxLength":128,"pattern":"^[A-Za-z0-9][A-Za-z0-9._-]*$"},
          "description":{"type":"string","maxLength":8192,"pattern":"^\\S(?:[\\s\\S]*\\S)?$"},
          "required_secrets":{"type":["array","null"],"uniqueItems":true,"items":{"type":"string","maxLength":128,"pattern":"^[A-Za-z0-9_][A-Za-z0-9_.-]*$"}},
          "input_schema":{"type":"object","properties":{"type":{"const":"object"}},"required":["type"]},
          "output_schema":{"type":"object","properties":{"type":{"const":"object"}},"required":["type"]}
        },
        "required":["name","description","input_schema","output_schema"]
      }
    }
  },
  "required":["workspace_id","instructions","actions"],
  "additionalProperties":false
}`),
		OutputSchema: output,
	})}, nil
}

func (a *BuildAgent) Discover(ctx context.Context, env environment.Environment, scope *environment.DiscoveryScope) (action.Status, error) {
	a.tools.Store(nil)
	a.SetStatus(action.Unknown)
	if env == nil {
		env = &environment.Local{}
	}
	tools := &buildTools{}
	var missing []actionmodel.Requirement
	for _, tool := range []struct {
		name string
		path *string
	}{{"codex", &tools.codex}, {"go", &tools.goTool}} {
		path, err := env.FindExecutable(ctx, tool.name, scope)
		if err != nil {
			return action.Unknown, err
		}
		*tool.path = path
		if path == "" {
			missing = append(missing, actionmodel.Requirement{Kind: actionmodel.ExecutableRequirement, Name: tool.name})
		}
	}
	if err := ctx.Err(); err != nil {
		return action.Unknown, err
	}
	if len(missing) > 0 {
		a.SetUnavailable("Install Go 1.26.7+ and Codex CLI, then run discovery again", missing...)
		return action.Unavailable, nil
	}
	a.tools.Store(tools)
	a.SetStatus(action.Ready)
	return action.Ready, nil
}

var workspaceIDPattern = regexp.MustCompile(`^checkout-[0-9]+$`)

func (a *BuildAgent) Run(ctx context.Context, raw json.RawMessage) (action.Result, error) {
	if err := ctx.Err(); err != nil {
		return action.Result{}, err
	}
	if err := a.Check(); err != nil {
		return action.Result{}, err
	}
	var input BuildAgentInput
	if err := action.DecodeInput(raw, &input); err != nil {
		return action.Result{}, err
	}
	if err := input.Validate(); err != nil {
		return action.Result{}, err
	}
	if !a.building.TryLock() {
		return action.Result{}, errors.New("another build is running on this builder; wait for its outcome")
	}
	defer a.building.Unlock()
	tools := a.tools.Load()
	if tools == nil || !process.Available(tools.codex) || !process.Available(tools.goTool) {
		a.tools.Store(nil)
		a.SetStatus(action.Unknown)
		return action.Result{}, errors.New("build tools changed; run discovery again")
	}
	source, err := a.sourceDirectory(input.WorkspaceID)
	if err != nil {
		return action.Result{}, err
	}
	key, err := action.Secret(ctx, "CODEX_API_KEY")
	if err != nil {
		return action.Result{}, err
	}
	defer clear(key)
	if len(key) == 0 || strings.ContainsAny(string(key), "\x00\r\n") {
		return action.Result{}, action.ErrSecretUnavailable
	}
	scratch, err := os.MkdirTemp(filepath.Dir(source), ".build-")
	if err != nil {
		return action.Result{}, errors.New("create temporary build directory failed")
	}
	defer os.RemoveAll(scratch)
	env, err := build.Environment(scratch, tools.codex, tools.goTool)
	if err != nil {
		return action.Result{}, err
	}
	if err := progress.Run(ctx, "Codex generation", func() error {
		return build.RunCodex(ctx, tools.codex, source, scratch, env, key, input.Instructions, input.Actions)
	}); err != nil {
		return action.Result{}, err
	}
	// Check the original directory again: generated files cannot redirect later
	// verification or artifact reads to another workspace through a symlink.
	if _, err := a.sourceDirectory(input.WorkspaceID); err != nil {
		return action.Result{}, err
	}
	if err := progress.Run(ctx, "Go tests", func() error {
		return build.RunCommand(ctx, "Go tests", tools.goTool, source, env, nil, "test", "-count=1", "-timeout=2m", "./...")
	}); err != nil {
		return action.Result{}, err
	}
	binary := filepath.Join(scratch, "agent")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if err := progress.Run(ctx, "Go build", func() error {
		return build.RunCommand(ctx, "Go build", tools.goTool, source, env, nil, "build", "-o", binary, "./cmd")
	}); err != nil {
		return action.Result{}, err
	}
	if _, err := a.sourceDirectory(input.WorkspaceID); err != nil {
		return action.Result{}, err
	}
	var metadata build.PackageMetadata
	if err := progress.Run(ctx, "Release structure validation", func() error {
		var validationErr error
		metadata, validationErr = build.ValidateRelease(ctx, tools.goTool, source, scratch, env, input.Actions)
		return validationErr
	}); err != nil {
		return action.Result{}, err
	}
	if _, err := a.sourceDirectory(input.WorkspaceID); err != nil {
		return action.Result{}, err
	}
	if err := progress.Run(ctx, "Registered action validation", func() error {
		return build.ValidateRegistration(ctx, tools.goTool, source, scratch, env, metadata, input.Actions)
	}); err != nil {
		return action.Result{}, err
	}
	if _, err := a.sourceDirectory(input.WorkspaceID); err != nil {
		return action.Result{}, err
	}
	var archive []byte
	var count int
	if err := progress.Run(ctx, "Source archive preparation", func() error {
		var archiveErr error
		archive, count, archiveErr = build.ArchiveSource(ctx, source, key)
		return archiveErr
	}); err != nil {
		return action.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return action.Result{}, err
	}
	result, err := action.JSONResult(BuildAgentOutput{
		WorkspaceID: input.WorkspaceID, SourceFiles: count,
		AgentType: metadata.AgentType, ModulePath: metadata.ModulePath, Binary: metadata.Binary,
	})
	if err != nil {
		return action.Result{}, err
	}
	result.Artifacts = []action.Artifact{{Name: "Agent source", Files: []artifacts.FileSource{{Name: "source.tar.gz", MediaType: "application/gzip", Data: archive}}}}
	return result, nil
}

func (a *BuildAgent) sourceDirectory(workspaceID string) (string, error) {
	info, err := os.Lstat(a.workspaceDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("builder workspace directory is unavailable or unsafe")
	}
	root, err := os.OpenRoot(a.workspaceDir)
	if err != nil {
		return "", errors.New("builder workspace directory is unavailable")
	}
	defer root.Close()
	for _, name := range []string{workspaceID, filepath.Join(workspaceID, "source")} {
		info, err := root.Lstat(name)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("workspace is missing or unsafe; use the workspace_id returned by checkout-template on this builder")
		}
	}
	return filepath.Join(a.workspaceDir, workspaceID, "source"), nil
}
