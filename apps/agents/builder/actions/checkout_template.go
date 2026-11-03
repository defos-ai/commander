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
	"github.com/defos-ai/commander/apps/agents/builder/internal/workspaces"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

const CheckoutTemplateName = "checkout-template"
const CheckoutTemplateDescription = "Check out the public Commander example-agent template into a new workspace session. Supply session_scope to create, or session_scope and session_id to reuse an existing checkout on its owning builder. Returns the session binding, opaque workspace ID and exact Git revision. Does not build, run or publish code."
const TemplateURL = "https://github.com/defos-ai/commander-example-agent.git"

type CheckoutTemplateInput struct {
	SessionScope string `json:"session_scope"`
	SessionID    string `json:"session_id,omitempty"`
}

type CheckoutTemplateOutput struct {
	SessionScope     string `json:"session_scope"`
	SessionID        string `json:"session_id"`
	WorkspaceID      string `json:"workspace_id"`
	TemplateRevision string `json:"template_revision"`
}

type CheckoutTemplate struct {
	*action.Base
	workspaces *workspaces.Provider
	executable atomic.Pointer[string]
}

func NewCheckoutTemplate(provider *workspaces.Provider) (*CheckoutTemplate, error) {
	if provider == nil {
		return nil, errors.New("workspace sessions are required")
	}
	outputSchema, err := action.SchemaFor[CheckoutTemplateOutput]()
	if err != nil {
		return nil, err
	}
	return &CheckoutTemplate{
		Base: action.NewBase(actionmodel.Action{
			SessionMode: string(sessions.Create),
			Name:        CheckoutTemplateName,
			Description: CheckoutTemplateDescription,
			InputSchema: json.RawMessage(`{"type":"object","properties":{
"session_scope":{"type":"string","format":"uuid","description":"Session scope; use the operation ID for an operation-local workspace."},
"session_id":{"type":"string","format":"uuid","description":"Existing session to reuse on its owning builder; omit to create."}
},"required":["session_scope"],"additionalProperties":false}`),
			OutputSchema: outputSchema,
		}),
		workspaces: provider,
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
	var input CheckoutTemplateInput
	if err := action.DecodeInput(raw, &input); err != nil {
		return action.Result{}, err
	}
	intent, err := sessions.ResolveIntent(string(sessions.Create), raw)
	if err != nil {
		return action.Result{}, err
	}
	binding, ok := action.SessionFromContext(ctx)
	if !ok || binding.ScopeID != intent.ScopeID || intent.ID != "" && binding.ID != intent.ID || binding.Mode != intent.Mode {
		return action.Result{}, errors.New("checkout requires its framework workspace session binding")
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
	var result action.Result
	err = action.RunWithProgress(ctx, "Template checkout", func() error {
		workspace, session, checkoutErr := a.workspaces.Checkout(ctx, binding, func(ctx context.Context, directory string) (string, error) {
			return a.checkout(ctx, *executable, directory)
		})
		if checkoutErr != nil {
			return checkoutErr
		}
		result, checkoutErr = action.JSONResult(CheckoutTemplateOutput{SessionScope: binding.ScopeID, SessionID: binding.ID,
			WorkspaceID: workspace.ID, TemplateRevision: workspace.Revision})
		result.Session = &session
		return checkoutErr
	})
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		// An executable can disappear between the readiness check and process start.
		if !process.Available(*executable) {
			a.gitUnavailable()
		}
	}
	return result, err
}

func (a *CheckoutTemplate) checkout(ctx context.Context, executable, workspace string) (revision string, err error) {
	// Git receives an empty home so it cannot read the host's credentials or config.
	home := filepath.Join(workspace, ".home")
	if err = os.Mkdir(home, 0700); err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(home)) }()
	command := git.Command{Executable: executable, Home: home}
	source := filepath.Join(workspace, "source")
	if _, err = command.Run(ctx, workspace, "clone", "--quiet", "--depth=1", "--single-branch", "--no-tags", "--no-recurse-submodules", "--template=", "--", TemplateURL, source); err != nil {
		return "", fmt.Errorf("checkout template: %w", err)
	}
	revision, err = command.Run(ctx, source, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", fmt.Errorf("read template revision: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	revision = strings.TrimSpace(revision)
	if hash, decodeErr := hex.DecodeString(revision); decodeErr != nil || (len(hash) != 20 && len(hash) != 32) {
		return "", errors.New("Git returned an invalid template revision")
	}
	return revision, nil
}
