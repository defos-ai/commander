package actions

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/apps/agents/builder/internal/git"
	"github.com/defos-ai/commander/apps/agents/builder/internal/process"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

const CheckoutTemplateName = "checkout-template"
const TemplateURL = "https://github.com/defos-ai/commander-example-agent.git"

type CheckoutTemplateOutput struct {
	WorkspaceID      string `json:"workspace_id"`
	TemplateRevision string `json:"template_revision"`
}

type CheckoutTemplate struct {
	*action.Base
	workspaceDir string
	executable   atomic.Pointer[string]
}

func NewCheckoutTemplate(workspaceDir string) (*CheckoutTemplate, error) {
	if strings.TrimSpace(workspaceDir) == "" {
		return nil, errors.New("workspace directory must not be blank")
	}
	root, err := filepath.Abs(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("workspace directory: %w", err)
	}
	outputSchema, err := action.SchemaFor[CheckoutTemplateOutput]()
	if err != nil {
		return nil, err
	}
	return &CheckoutTemplate{
		Base: action.NewBase(actionmodel.Action{
			Name:         CheckoutTemplateName,
			Description:  "Check out the public Commander example-agent template into a new local workspace. Returns its workspace ID and exact Git revision. Does not build, run or publish code; the workspace belongs to this builder instance.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			OutputSchema: outputSchema,
		}),
		workspaceDir: root,
	}, nil
}

func (a *CheckoutTemplate) Discover(ctx context.Context, env environment.Environment, scope *environment.DiscoveryScope) (action.Status, error) {
	a.executable.Store(nil)
	a.SetStatus(action.Unknown)
	if env == nil {
		env = &environment.Local{}
	}
	path, err := env.FindExecutable(ctx, "git", scope)
	if err != nil {
		return action.Unknown, err
	}
	if err := ctx.Err(); err != nil {
		return action.Unknown, err
	}
	if path == "" {
		a.gitUnavailable()
		return action.Unavailable, nil
	}
	a.executable.Store(&path)
	a.SetStatus(action.Ready)
	return action.Ready, nil
}

func (a *CheckoutTemplate) gitUnavailable() {
	a.executable.Store(nil)
	a.SetUnavailable("Install Git and run discovery again", actionmodel.Requirement{Kind: actionmodel.ExecutableRequirement, Name: "git"})
}

func (a *CheckoutTemplate) Run(ctx context.Context, raw json.RawMessage) (action.Result, error) {
	if err := ctx.Err(); err != nil {
		return action.Result{}, err
	}
	if err := a.Check(); err != nil {
		return action.Result{}, err
	}
	if err := action.DecodeInput(raw, &struct{}{}); err != nil {
		return action.Result{}, err
	}
	executable := a.executable.Load()
	if executable == nil {
		return action.Result{}, errors.New("Git is unavailable; run discovery first")
	}
	if !process.Available(*executable) {
		a.gitUnavailable()
		return action.Result{}, errors.New("Git is unavailable; run discovery again")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	result, err := a.checkout(ctx, *executable)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		// An executable can disappear between the readiness check and process start.
		if !process.Available(*executable) {
			a.gitUnavailable()
		}
	}
	return result, err
}

func (a *CheckoutTemplate) checkout(ctx context.Context, executable string) (result action.Result, err error) {
	if err = os.MkdirAll(a.workspaceDir, 0700); err != nil {
		return result, fmt.Errorf("create workspace directory: %w", err)
	}
	workspace, err := os.MkdirTemp(a.workspaceDir, "checkout-")
	if err != nil {
		return result, fmt.Errorf("create checkout workspace: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(workspace))
		}
	}()
	// Git receives an empty home so it cannot read the host's credentials or config.
	home := filepath.Join(workspace, ".home")
	if err = os.Mkdir(home, 0700); err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(home)) }()
	command := git.Command{Executable: executable, Home: home}
	source := filepath.Join(workspace, "source")
	if _, err = command.Run(ctx, workspace, "clone", "--quiet", "--depth=1", "--single-branch", "--no-tags", "--no-recurse-submodules", "--template=", "--", TemplateURL, source); err != nil {
		return result, fmt.Errorf("checkout template: %w", err)
	}
	revision, err := command.Run(ctx, source, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return result, fmt.Errorf("read template revision: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	revision = strings.TrimSpace(revision)
	if hash, decodeErr := hex.DecodeString(revision); decodeErr != nil || (len(hash) != 20 && len(hash) != 32) {
		return result, errors.New("Git returned an invalid template revision")
	}
	return action.JSONResult(CheckoutTemplateOutput{
		WorkspaceID: filepath.Base(workspace), TemplateRevision: revision,
	})
}
