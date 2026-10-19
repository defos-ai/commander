package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/consumers"
	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/domain"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/id"
)

type AgentConfig struct {
	// ID is optional. Missing IDs use the type, -agent if absent, and eight lowercase hex characters.
	ID   string
	Type AgentType
	// Server overrides host information. Missing fields use SERVER_IP_ADDRESS
	// and SERVER_HOSTNAME, then local discovery, once at construction.
	Server           ServerInfo
	Responsibilities []string
	Actions          []action.Executor
	PublicConfig     json.RawMessage
	Environment      environment.Environment
	// Secrets enables encrypted synchronization of declared and requested names.
	Secrets *SecretConfig
	// ArtifactFileStore is required and is created by the host.
	ArtifactFileStore artifacts.ArtifactFileStore
	// Logger receives runtime events without action inputs or results. Nil is silent.
	Logger *slog.Logger
	// Nil uses thirty seconds; zero disables periodic announcements.
	HeartbeatInterval *time.Duration
}

// Agent owns an explicit action catalog and executes controller instructions.
// Give each agent its own action instances; actions contain discovery state.
type Agent struct {
	config            AgentConfig
	state             atomic.Pointer[agentState]
	heartbeatInterval time.Duration
	stateGate         chan struct{}
	actionGate        chan struct{}
	outcomes          consumers.Outcomes
	secrets           *secretRuntime
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
	if !id.ValidType(string(config.Type)) {
		return nil, errors.New("agent type must be valid")
	}
	if config.ID == "" {
		var err error
		config.ID, err = id.AgentID(string(config.Type))
		if err != nil {
			return nil, err
		}
	}
	idType := strings.TrimSuffix(string(config.Type), "-agent")
	if kind, ok := id.RuntimeType(config.ID, id.Agent); !ok || kind != idType {
		return nil, fmt.Errorf("agent ID must match %s-agent-<8 lowercase hex characters>", idType)
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
		definition := t.Action()
		name := definition.Name
		if strings.TrimSpace(name) == "" || seen[name] {
			return nil, errors.New("action names must be nonblank and unique within an agent")
		}
		seen[name] = true
		if err := definition.ValidateRequiredSecrets(); err != nil {
			return nil, fmt.Errorf("action %q: %w", name, err)
		}
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
	if config.ArtifactFileStore == nil {
		return nil, errors.New("artifact file store is required")
	}
	config.Actions = append([]action.Executor(nil), config.Actions...)
	config.Responsibilities = append([]string(nil), config.Responsibilities...)
	config.PublicConfig = append(json.RawMessage(nil), config.PublicConfig...)
	secretRuntime, err := newSecretRuntime(config.Secrets, config.ID, string(config.Type), config.Actions)
	if err != nil {
		return nil, err
	}
	config.Secrets = nil
	a := &Agent{config: config, heartbeatInterval: heartbeatInterval, stateGate: make(chan struct{}, 1), actionGate: make(chan struct{}, 1), secrets: secretRuntime}
	a.state.Store(&agentState{status: AgentIdle})
	return a, nil
}

func (a *Agent) ID() string { return a.config.ID }

func (a *Agent) Status() AgentStatus {
	return a.state.Load().status
}

func (a *Agent) ArtifactFileStore() artifacts.ArtifactFileStore {
	return a.config.ArtifactFileStore
}

func (a *Agent) Describe() AgentSnapshot {
	state := a.state.Load()
	description := AgentSnapshot{
		ID: a.ID(), Type: a.config.Type, Status: state.status,
		Work:             state.work.Clone(),
		Server:           a.config.Server,
		Responsibilities: append([]string(nil), a.config.Responsibilities...),
		DiscoveryScope:   state.scope.Clone(), Config: append(json.RawMessage(nil), a.config.PublicConfig...),
		Actions: make([]actionmodel.Action, 0, len(a.config.Actions)),
	}
	for _, instance := range a.config.Actions {
		description.Actions = append(description.Actions, a.describeAction(instance.Action()))
	}

	return description
}

// DiscoverActions refreshes presence. Missing actions do not block readiness.
func (a *Agent) DiscoverActions(ctx context.Context, scope *environment.DiscoveryScope) error {
	return a.discover(ctx, scope, false)
}

func (a *Agent) discover(ctx context.Context, scope *environment.DiscoveryScope, fromServer bool) (discoveryErr error) {
	if err := a.claimAction(ctx, fromServer); err != nil {
		return err
	}
	defer func() { <-a.actionGate }()
	if err := consumers.CheckCommandDeadline(ctx); err != nil {
		return err
	}
	return a.discoverActions(ctx, scope, fromServer)
}

func (a *Agent) discoverActions(ctx context.Context, scope *environment.DiscoveryScope, fromServer bool) (discoveryErr error) {
	started := false
	discoveryErr = a.stateChange(ctx, func(next *agentState) error {
		if next.status == AgentDiscovering || next.status == AgentWorking || next.serving && !fromServer {
			return errors.New("agent is busy")
		}
		next.status, next.scope = AgentDiscovering, scope.Clone()
		started = true
		return nil
	}, nil)
	if started {
		defer func() {
			status := AgentReady
			if discoveryErr != nil {
				status = AgentUnavailable
			}
			discoveryErr = errors.Join(discoveryErr, a.finishState(ctx, status, nil))
		}()
	}
	if discoveryErr != nil {
		return discoveryErr
	}
	for _, t := range a.config.Actions {
		if discoveryErr = ctx.Err(); discoveryErr != nil {
			break
		}
		status, err := t.Discover(a.secretContext(ctx, t.Action(), false), a.config.Environment, scope.Clone())
		if errors.Is(err, action.ErrSecretUnavailable) && a.missingSecrets(t.Action()) {
			// Missing managed values are an action blocker. Keep serving so the
			// synchronization request can obtain them and rediscover this action.
			status, err = action.Unavailable, nil
		}
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
	return a.work(ctx, call, "", false, nil)
}

func (a *Agent) work(ctx context.Context, call ActionCall, commandID string, fromServer bool, announce func(context.Context, AgentSnapshot) error) (output ActionOutput, workErr error) {
	if err := a.claimAction(ctx, fromServer); err != nil {
		return ActionOutput{}, err
	}
	defer func() { <-a.actionGate }()
	if err := consumers.CheckCommandDeadline(ctx); err != nil {
		return ActionOutput{}, err
	}
	startedAt := time.Now()
	logger := a.config.Logger.With("action_name", call.ActionName, "command_id", commandID)
	logger.Info("action started")
	defer func() {
		if workErr != nil {
			logger.Warn("action failed", "error_type", fmt.Sprintf("%T", workErr), "duration_ms", time.Since(startedAt).Milliseconds())
		} else {
			logger.Info("action completed", "duration_ms", time.Since(startedAt).Milliseconds(), "artifact_count", len(output.Artifacts))
		}
	}()
	if err := call.Validate(); err != nil {
		return ActionOutput{}, err
	}
	started := false
	workErr = a.stateChange(ctx, func(next *agentState) error {
		if next.status != AgentReady || next.serving && !fromServer {
			return errors.New("agent must be ready and available before working")
		}
		next.status = AgentWorking
		next.work = &Work{OperationID: call.Work.OperationID, TaskID: call.Work.TaskID,
			StepID: call.Work.StepID, ActionName: call.ActionName, CommandID: commandID}
		started = true
		return nil
	}, announce)
	if started {
		defer func() {
			if err := a.finishState(ctx, AgentReady, announce); err != nil {
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
		description := a.describeAction(t.Action())
		if description.Name == call.ActionName {
			if description.Status != action.Ready {
				return ActionOutput{}, fmt.Errorf("action %q is not ready; run discovery first", call.ActionName)
			}
			input := append(json.RawMessage(nil), call.Input...)
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			executionContext := action.WithExecution(ctx, action.Execution{CommandID: commandID, Work: call.Work})
			executionContext, endExecution := context.WithCancel(executionContext)
			defer endExecution()
			result, err := t.Run(a.secretContext(executionContext, description, fromServer), input)
			endExecution()
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
	if err := actionmodel.ValidateNextActions(result.NextActions); err != nil {
		return ActionOutput{}, err
	}
	output := ActionOutput{ActionName: name, Output: bytes.Clone(result.Output), NextActions: actionmodel.CloneNextActions(result.NextActions)}
	owner := artifacts.Owner{AgentID: a.ID(), ActionName: name, CommandID: commandID}
	for _, attachment := range result.Artifacts {
		ref, err := a.publishArtifact(ctx, attachment, owner)
		if err != nil {
			return ActionOutput{}, fmt.Errorf("%w: store attachment: %w", errArtifactPersistence, err)
		}
		output.Artifacts = append(output.Artifacts, ref)
	}
	if err := ctx.Err(); err != nil {
		return ActionOutput{}, err
	}
	if err := output.Validate(); err != nil {
		return ActionOutput{}, fmt.Errorf("invalid stored result: %w", err)
	}
	return output, nil
}

func (a *Agent) publishArtifact(ctx context.Context, source action.Artifact, owner artifacts.Owner) (artifacts.Artifact, error) {
	record := artifacts.Artifact{ID: id.ArtifactID(), Name: source.Name, AgentID: owner.AgentID,
		ActionName: owner.ActionName, CommandID: owner.CommandID, CreatedAt: time.Now().UTC(), Files: []artifacts.ArtifactFile{}}
	if err := record.Validate(); err != nil {
		return artifacts.Artifact{}, err
	}
	for _, file := range source.Files {
		if err := file.Validate(); err != nil {
			return artifacts.Artifact{}, err
		}
	}
	for _, sourceFile := range source.Files {
		file, err := a.config.ArtifactFileStore.Put(ctx, record.ID, sourceFile)
		if err != nil {
			return artifacts.Artifact{}, err
		}
		record.Files = append(record.Files, file)
	}
	if err := record.Validate(); err != nil {
		return artifacts.Artifact{}, err
	}
	return record, nil
}
