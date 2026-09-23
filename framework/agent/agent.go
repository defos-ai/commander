package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"commander/framework/agent/action"
	"commander/framework/agent/consumers"
	"commander/framework/agent/environment"
	"commander/framework/artifact"
	artifactstore "commander/framework/artifact/store"
	"commander/framework/domain"
	actionmodel "commander/framework/domain/actions"
	"commander/framework/id"
)

type AgentConfig struct {
	// ID is optional. Missing IDs become <type>-agent-<8 lowercase hex characters>.
	ID   string
	Type AgentType
	// Server overrides host information. Missing fields use SERVER_IP_ADDRESS
	// and SERVER_HOSTNAME, then local discovery, once at construction.
	Server             ServerInfo
	Responsibilities   []string
	Actions            []action.Executor
	GrantedPermissions []string
	PublicConfig       json.RawMessage
	Environment        environment.Environment
	// ArtifactStore is required and is created by the host.
	ArtifactStore artifactstore.ArtifactStore
	// Logger receives runtime events without action inputs or results. Nil is silent.
	Logger *slog.Logger
	// Nil uses 100,000 bytes; zero stores every structured result.
	InlineOutputLimit *int
	// Nil uses thirty seconds; zero disables periodic announcements.
	HeartbeatInterval *time.Duration
}

// Agent owns an explicit action catalog and executes controller instructions.
// Give each agent its own action instances; actions contain discovery state.
type Agent struct {
	mu                sync.RWMutex
	config            AgentConfig
	status            AgentStatus
	serving           bool
	scope             *environment.DiscoveryScope
	inlineLimit       int
	heartbeatInterval time.Duration
	stateGate         chan struct{}
	outcomes          consumers.Outcomes
}

const DefaultHeartbeatInterval = 30 * time.Second

var errArtifactPersistence = actionmodel.ErrArtifactPersistence

func NewAgent(config AgentConfig) (*Agent, error) {
	if config.Type == "" {
		config.Type = GenericAgentType
	}
	if strings.TrimSpace(string(config.Type)) == "" {
		return nil, errors.New("agent type must not be blank")
	}
	if config.ID == "" {
		var err error
		config.ID, err = id.AgentID(string(config.Type))
		if err != nil {
			return nil, err
		}
	}
	if kind, ok := id.RuntimeType(config.ID, id.Agent); !ok || kind != string(config.Type) {
		return nil, fmt.Errorf("agent ID must match %s-agent-<8 lowercase hex characters>", config.Type)
	}
	server, err := environment.ResolveHostInfo(environment.HostInfo{IPAddress: config.Server.IPAddress, Hostname: config.Server.Hostname})
	if err != nil {
		return nil, err
	}
	config.Server = ServerInfo{IPAddress: server.IPAddress, Hostname: server.Hostname}
	heartbeatInterval := DefaultHeartbeatInterval
	if config.HeartbeatInterval != nil {
		heartbeatInterval = *config.HeartbeatInterval
	}
	if heartbeatInterval < 0 {
		return nil, errors.New("heartbeat interval must not be negative")
	}
	config.HeartbeatInterval = nil
	seen := make(map[string]bool)
	for _, t := range config.Actions {
		if t == nil {
			return nil, errors.New("action must not be nil")
		}
		name := t.Action().Name
		if strings.TrimSpace(name) == "" || seen[name] {
			return nil, errors.New("action names must be nonblank and unique within an agent")
		}
		seen[name] = true
	}
	if len(config.PublicConfig) == 0 {
		config.PublicConfig = json.RawMessage(`{}`)
	}
	if err := domain.ValidateObject(config.PublicConfig); err != nil {
		return nil, fmt.Errorf("public config: %w", err)
	}
	if config.Environment == nil {
		config.Environment = &environment.Local{}
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	limit := DefaultInlineOutputLimit
	if config.InlineOutputLimit != nil {
		limit = *config.InlineOutputLimit
	}
	if limit < 0 {
		return nil, errors.New("inline output limit must not be negative")
	}
	if config.ArtifactStore == nil {
		return nil, errors.New("artifact store is required")
	}
	config.InlineOutputLimit = nil // Store the copied value, not the caller's pointer.
	config.Actions = append([]action.Executor(nil), config.Actions...)
	config.Responsibilities = append([]string(nil), config.Responsibilities...)
	config.GrantedPermissions = append([]string(nil), config.GrantedPermissions...)
	config.PublicConfig = append(json.RawMessage(nil), config.PublicConfig...)
	return &Agent{config: config, status: AgentIdle, inlineLimit: limit, heartbeatInterval: heartbeatInterval, stateGate: make(chan struct{}, 1)}, nil
}

func (a *Agent) ID() string { return a.config.ID }

func (a *Agent) Status() AgentStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

func (a *Agent) ArtifactStore() artifactstore.ArtifactStore { return a.config.ArtifactStore }

func (a *Agent) Describe() AgentSnapshot {
	a.mu.RLock()
	status, scope := a.status, a.scope.Clone()
	a.mu.RUnlock()
	description := AgentSnapshot{
		ID: a.ID(), Type: a.config.Type, Status: status,
		Server:             a.config.Server,
		Responsibilities:   append([]string(nil), a.config.Responsibilities...),
		GrantedPermissions: append([]string(nil), a.config.GrantedPermissions...),
		DiscoveryScope:     scope, Config: append(json.RawMessage(nil), a.config.PublicConfig...),
		Actions: make([]actionmodel.Action, 0, len(a.config.Actions)),
	}
	for _, instance := range a.config.Actions {
		d := instance.Action()
		for _, permission := range d.RequiredPermissions {
			if !slices.Contains(a.config.GrantedPermissions, permission) {
				if d.StatusReason == "" {
					d.StatusReason = "missing permission: " + permission
				}
				d.Status = action.Unavailable
				requirement := actionmodel.Requirement{Kind: actionmodel.PermissionRequirement, Name: permission}
				if !slices.Contains(d.MissingRequirements, requirement) {
					d.MissingRequirements = append(d.MissingRequirements, requirement)
				}
			}
		}
		description.Actions = append(description.Actions, d)
	}

	return description
}

// DiscoverActions refreshes presence. Missing actions do not block readiness.
func (a *Agent) DiscoverActions(ctx context.Context, scope *environment.DiscoveryScope) error {
	return a.discover(ctx, scope, false)
}

func (a *Agent) discover(ctx context.Context, scope *environment.DiscoveryScope, fromServer bool) (discoveryErr error) {
	started := false
	discoveryErr = a.stateChange(ctx, func() error {
		if a.status == AgentDiscovering || a.status == AgentWorking || a.serving && !fromServer {
			return errors.New("agent is busy")
		}
		a.status, a.scope = AgentDiscovering, scope.Clone()
		started = true
		return nil
	}, nil)
	if started {
		defer func() {
			status := AgentReady
			if discoveryErr != nil {
				status = AgentUnavailable
			}
			discoveryErr = errors.Join(discoveryErr, a.finishState(ctx, status))
		}()
	}
	if discoveryErr != nil {
		return discoveryErr
	}
	for _, t := range a.config.Actions {
		if discoveryErr = ctx.Err(); discoveryErr != nil {
			break
		}
		status, err := t.Discover(ctx, a.config.Environment, scope.Clone())
		if err != nil {
			a.config.Logger.Warn("action discovery failed", "action_name", t.Action().Name, "error_type", fmt.Sprintf("%T", err))
			discoveryErr = err
			break
		}
		a.config.Logger.Debug("action discovered", "action_name", t.Action().Name, "status", status)
		if status != action.Ready && status != action.Unavailable {
			discoveryErr = fmt.Errorf("action %q returned invalid discovery status %q", t.Action().Name, status)
			break
		}
	}
	if discoveryErr == nil {
		discoveryErr = ctx.Err()
	}
	return discoveryErr
}

// Work executes one externally selected action. Serve uses the same execution and
// artifact path, adding the command ID to artifact ownership and replies.
func (a *Agent) Work(ctx context.Context, call ActionCall) (ActionOutput, error) {
	return a.work(ctx, call, "", false)
}

func (a *Agent) work(ctx context.Context, call ActionCall, commandID string, fromServer bool) (output ActionOutput, workErr error) {
	startedAt := time.Now()
	logger := a.config.Logger.With("action_name", call.ActionName, "command_id", commandID)
	logger.Info("action started")
	defer func() {
		if workErr != nil {
			logger.Warn("action failed", "error_type", fmt.Sprintf("%T", workErr), "duration_ms", time.Since(startedAt).Milliseconds())
		} else {
			logger.Info("action completed", "duration_ms", time.Since(startedAt).Milliseconds(), "stored_output", output.OutputArtifact != nil, "artifact_count", len(output.Artifacts))
		}
	}()
	if err := call.Validate(); err != nil {
		return ActionOutput{}, err
	}
	started := false
	workErr = a.stateChange(ctx, func() error {
		if a.status != AgentReady || a.serving && !fromServer {
			return errors.New("agent must be ready and available before working")
		}
		a.status = AgentWorking
		started = true
		return nil
	}, nil)
	if started {
		defer func() {
			if err := a.finishState(ctx, AgentReady); err != nil {
				workErr = errors.Join(workErr, err)
				output = ActionOutput{}
			}
		}()
	}
	if workErr != nil {
		return ActionOutput{}, workErr
	}
	if err := ctx.Err(); err != nil {
		return ActionOutput{}, err
	}
	for _, t := range a.config.Actions {
		description := t.Action()
		if description.Name == call.ActionName {
			for _, permission := range description.RequiredPermissions {
				if !slices.Contains(a.config.GrantedPermissions, permission) {
					return ActionOutput{}, fmt.Errorf("missing permission: %s", permission)
				}
			}
			if description.Status != action.Ready {
				return ActionOutput{}, fmt.Errorf("action %q is not ready; run discovery first", call.ActionName)
			}
			input := append(json.RawMessage(nil), call.Input...)
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			result, err := t.Run(ctx, input, append([]string(nil), a.config.GrantedPermissions...))
			if err != nil {
				return ActionOutput{}, err
			}
			if err := ctx.Err(); err != nil {
				return ActionOutput{}, err
			}
			return a.packageOutput(ctx, call.ActionName, result, commandID)
		}
	}
	return ActionOutput{}, fmt.Errorf("unknown action: %s", call.ActionName)
}

func (a *Agent) packageOutput(ctx context.Context, name string, result action.Result, commandID string) (ActionOutput, error) {
	if err := domain.ValidateObject(result.Output); err != nil {
		return ActionOutput{}, fmt.Errorf("action output: %w", err)
	}
	// Match the transport's JSON encoding, including HTML character escapes.
	// Compacting alone could underestimate the actual inline payload size.
	encoded, err := json.Marshal(result.Output)
	if err != nil {
		return ActionOutput{}, err
	}
	if err := actionmodel.ValidateNextActions(result.NextActions); err != nil {
		return ActionOutput{}, err
	}
	output := ActionOutput{ActionName: name, NextActions: actionmodel.CloneNextActions(result.NextActions)}
	owner := artifact.Owner{AgentID: a.ID(), ActionName: name, CommandID: commandID}
	for _, attachment := range result.Artifacts {
		ref, err := a.config.ArtifactStore.Put(ctx, attachment, owner)
		if err != nil {
			return ActionOutput{}, fmt.Errorf("%w: store attachment: %w", errArtifactPersistence, err)
		}
		output.Artifacts = append(output.Artifacts, ref)
	}
	if len(encoded) <= a.inlineLimit {
		output.Output = encoded
	} else {
		ref, err := a.config.ArtifactStore.Put(ctx, artifact.Artifact{
			Name: "output.json", MediaType: "application/json", Data: encoded,
		}, owner)
		if err != nil {
			return ActionOutput{}, fmt.Errorf("%w: store output: %w", errArtifactPersistence, err)
		}
		output.OutputArtifact = &ref
	}
	if err := ctx.Err(); err != nil {
		return ActionOutput{}, err
	}
	if err := output.Validate(); err != nil {
		return ActionOutput{}, fmt.Errorf("invalid stored result: %w", err)
	}
	return output, nil
}
