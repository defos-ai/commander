package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
)

func TestSecretDeclarationsSurviveDiscoveryAndIndependentSnapshots(t *testing.T) {
	definition := actions.Action{Name: "build", RequiredSecrets: []string{"OPENAI_API_KEY", "GITHUB_TOKEN"},
		InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	executor := &fakes.SecretAction{Base: action.NewBase(definition)}
	definition.RequiredSecrets[0] = "CHANGED"
	runtime, err := agent.NewAgent(agent.AgentConfig{Type: "builder-agent",
		Actions: []action.Executor{executor}, ArtifactFileStore: fakes.UnusedArtifacts{},
		Server: agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.DiscoverActions(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	snapshot := runtime.Describe()
	want := []string{"OPENAI_API_KEY", "GITHUB_TOKEN"}
	wantMissing := []actions.Requirement{
		{Kind: actions.SecretRequirement, Name: "OPENAI_API_KEY"},
		{Kind: actions.SecretRequirement, Name: "GITHUB_TOKEN"},
	}
	if snapshot.Type != "builder-agent" ||
		snapshot.Actions[0].Status != actions.Unavailable || !slices.Equal(snapshot.Actions[0].MissingRequirements, wantMissing) ||
		!slices.Equal(snapshot.Actions[0].RequiredSecrets, want) {
		t.Fatalf("discovery lost stable secret declarations: %+v", snapshot)
	}
	snapshot.Actions[0].RequiredSecrets[0] = "CHANGED"
	if got := runtime.Describe().Actions[0].RequiredSecrets; !slices.Equal(got, want) {
		t.Fatalf("reader mutated agent secret declarations: %v", got)
	}
	readAction := executor.Action()
	readAction.RequiredSecrets[0] = "CHANGED"
	if got := executor.Action().RequiredSecrets; !slices.Equal(got, want) {
		t.Fatalf("reader mutated action secret declarations: %v", got)
	}
	record := agents.Agent{AgentSnapshot: runtime.Describe()}
	cloned := record.Clone()
	cloned.Actions[0].RequiredSecrets[0] = "CHANGED"
	if !slices.Equal(record.Actions[0].RequiredSecrets, want) {
		t.Fatal("cloned inventory shares secret declarations")
	}
	// Host configuration and a managed secret can share a name without sharing
	// a source or resolution path.
	hostRequirement := actions.Requirement{Kind: actions.ConfigurationRequirement, Name: "OPENAI_API_KEY", Sensitive: true}
	executor.SetUnavailable("Missing configuration", hostRequirement)
	mixed := runtime.Describe().Actions[0]
	if !slices.Equal(mixed.MissingRequirements, append([]actions.Requirement{hostRequirement}, wantMissing...)) {
		t.Fatalf("sensitive host configuration was conflated with managed secrets: %+v", mixed)
	}
	if _, err := runtime.Work(t.Context(), actions.ActionCall{ActionName: "build", Input: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("action with required secrets executed without secret configuration")
	}
}

func TestActionValidatesManagedSecretRequirements(t *testing.T) {
	for _, test := range []struct {
		name        string
		requirement actions.Requirement
		declared    []string
		valid       bool
	}{
		{name: "managed secret", requirement: actions.Requirement{Kind: actions.SecretRequirement, Name: "TOKEN"}, declared: []string{"TOKEN"}, valid: true},
		{name: "undeclared secret", requirement: actions.Requirement{Kind: actions.SecretRequirement, Name: "TOKEN"}},
		{name: "invalid secret name", requirement: actions.Requirement{Kind: actions.SecretRequirement, Name: "../TOKEN"}, declared: []string{"TOKEN"}},
		{name: "sensitive host configuration", requirement: actions.Requirement{Kind: actions.ConfigurationRequirement, Name: "TOKEN", Sensitive: true}, valid: true},
		{name: "sensitivity flag on managed secret", requirement: actions.Requirement{Kind: actions.SecretRequirement, Name: "TOKEN", Sensitive: true}, declared: []string{"TOKEN"}},
		{name: "sensitivity flag on executable", requirement: actions.Requirement{Kind: actions.ExecutableRequirement, Name: "git", Sensitive: true}},
		{name: "unknown kind", requirement: actions.Requirement{Kind: "other", Name: "TOKEN"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			definition := actions.Action{Name: "build", Status: actions.Unavailable,
				RequiredSecrets: test.declared, MissingRequirements: []actions.Requirement{test.requirement},
				InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
			if err := definition.Validate(); (err == nil) != test.valid {
				t.Fatalf("unexpected requirement validation: %v", err)
			}
		})
	}
}

func TestInvalidSecretDeclarationsRejectedBeforeDiscovery(t *testing.T) {
	for _, names := range [][]string{{""}, {"API_TOKEN", "API_TOKEN"}, {"../TOKEN"}, {"API TOKEN"}, {"*"}, {"ТOKEN"}, {strings.Repeat("A", 129)}} {
		t.Run(strings.Join(names, ","), func(t *testing.T) {
			definition := actions.Action{Name: "build", RequiredSecrets: names, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
			if err := definition.Validate(); err == nil {
				t.Fatal("invalid action secret declarations accepted")
			}
			_, err := agent.NewAgent(agent.AgentConfig{Type: "builder-agent",
				Actions: []action.Executor{&fakes.SecretAction{Base: action.NewBase(definition)}}, ArtifactFileStore: fakes.UnusedArtifacts{},
				Server: agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"},
			})
			if err == nil {
				t.Fatal("agent accepted invalid declarations")
			}
			definition.InputSchema, definition.OutputSchema = nil, nil
			_, err = action.NewFunc(action.FuncConfig[struct{}, struct{}]{Action: definition,
				Run: func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil },
			})
			if err == nil {
				t.Fatal("function action accepted invalid declarations")
			}
		})
	}
}

func TestAgentReportsOverlappingSecretWaitsPerExecution(t *testing.T) {
	type waiter struct {
		name    string
		start   chan struct{}
		cancel  chan context.CancelFunc
		outcome chan error
	}
	waiters := []waiter{
		{name: "TOKEN"}, {name: "TOKEN"}, {name: "OTHER_TOKEN"},
	}
	for i := range waiters {
		waiters[i].start = make(chan struct{})
		waiters[i].cancel = make(chan context.CancelFunc, 1)
		waiters[i].outcome = make(chan error, 1)
	}
	finish := make(chan struct{})
	executor, err := action.NewFunc(action.FuncConfig[struct{}, struct{}]{
		Action: actions.Action{Name: "wait"},
		Run: func(ctx context.Context, _ struct{}) (struct{}, error) {
			var workers sync.WaitGroup
			defer workers.Wait()
			for _, waiter := range waiters {
				workers.Add(1)
				go func() {
					defer workers.Done()
					select {
					case <-waiter.start:
					case <-ctx.Done():
						waiter.outcome <- ctx.Err()
						return
					}
					requestCtx, cancel := context.WithCancel(ctx)
					defer cancel()
					waiter.cancel <- cancel
					value, err := action.RequestSecret(requestCtx, waiter.name)
					clear(value)
					waiter.outcome <- err
				}()
			}
			select {
			case <-finish:
				return struct{}{}, nil
			case <-ctx.Done():
				return struct{}{}, ctx.Err()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := fixtures.StartSecretAgentActions(t, 10*time.Second, 8*time.Second, executor)
	command := fixture.Command(t, "wait", `{}`)
	waitSecretWorkRequirements(t, fixture, command)
	close(waiters[0].start)
	waitSecretWorkRequirements(t, fixture, command, "TOKEN")
	request := fixture.Request(t, "")
	close(waiters[1].start)
	waitSecretWorkRequirements(t, fixture, command, "TOKEN")
	(<-waiters[0].cancel)()
	waitSecretWorkRequirements(t, fixture, command, "TOKEN")
	select {
	case err := <-waiters[0].outcome:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled overlapping waiter returned %v", err)
		}
	case <-fixture.Context().Done():
		t.Fatal("canceled overlapping waiter did not return")
	}
	close(waiters[2].start)
	waitSecretWorkRequirements(t, fixture, command, "OTHER_TOKEN", "TOKEN")
	if request.Key.Name != "TOKEN" {
		t.Fatal("first request was not for the shared waiter name")
	}
	delivery, err := agentevents.NewSecretDelivery(request, fixture.Secret(t, request, time.Time{}, "fixture-value"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.Deliver(t, delivery)
	waitSecretWorkRequirements(t, fixture, command, "OTHER_TOKEN")
	select {
	case err := <-waiters[1].outcome:
		if err != nil {
			t.Fatalf("remaining overlapping waiter did not receive the value: %v", err)
		}
	case <-fixture.Context().Done():
		t.Fatal("remaining overlapping waiter did not return")
	}
	(<-waiters[2].cancel)()
	waitSecretWorkRequirements(t, fixture, command)
	select {
	case err := <-waiters[2].outcome:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("last canceled waiter returned %v", err)
		}
	case <-fixture.Context().Done():
		t.Fatal("last canceled waiter did not return")
	}
	close(finish)
	fixture.Outcome(t, command, controllerevents.ActionSucceeded)
}

func TestAgentFinishedExecutionCannotReportSecretWaits(t *testing.T) {
	contexts := make(chan context.Context, 1)
	finish := make(chan struct{}, 1)
	executor, err := action.NewFunc(action.FuncConfig[struct{}, struct{}]{
		Action: actions.Action{Name: "wait"},
		Run: func(ctx context.Context, _ struct{}) (struct{}, error) {
			contexts <- context.WithoutCancel(ctx)
			select {
			case <-finish:
				return struct{}{}, nil
			case <-ctx.Done():
				return struct{}{}, ctx.Err()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := fixtures.StartSecretAgentActions(t, 10*time.Second, 8*time.Second, executor)
	first := fixture.Command(t, "wait", `{}`)
	var detached context.Context
	select {
	case detached = <-contexts:
	case <-fixture.Context().Done():
		t.Fatal("first execution did not start")
	}
	returned := make(chan error, 1)
	go func() {
		value, err := action.RequestSecret(detached, "TOKEN")
		clear(value)
		returned <- err
	}()
	waitSecretWorkRequirements(t, fixture, first, "TOKEN")
	finish <- struct{}{}
	fixture.Outcome(t, first, controllerevents.ActionSucceeded)
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("finished execution's detached waiter returned %v", err)
		}
	case <-fixture.Context().Done():
		t.Fatal("finished execution retained its detached waiter")
	}
	second := fixture.Command(t, "wait", `{}`)
	waitSecretWorkRequirements(t, fixture, second)
	if value, err := action.RequestSecret(detached, "OTHER_TOKEN"); !errors.Is(err, context.Canceled) || len(value) != 0 {
		clear(value)
		t.Fatalf("finished execution accepted another request: %v", err)
	}
	assertSecretWorkRequirements(t, fixture, second)
	finish <- struct{}{}
	fixture.Outcome(t, second, controllerevents.ActionSucceeded)
	if fixture.Runtime().Describe().Work != nil {
		t.Fatal("finished execution retained missing requirements")
	}
}

func waitSecretWorkRequirements(t *testing.T, fixture *fixtures.SecretAgent, commandID string, names ...string) {
	t.Helper()
	for {
		message := fixture.Next(t, controllerevents.AgentUpdated)
		var snapshot agents.AgentSnapshot
		if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Work == nil || snapshot.Work.CommandID != commandID || !slices.Equal(snapshot.Work.MissingRequirements, secretRequirements(names)) {
			continue
		}
		assertSecretWorkRequirements(t, fixture, commandID, names...)
		return
	}
}

func assertSecretWorkRequirements(t *testing.T, fixture *fixtures.SecretAgent, commandID string, names ...string) {
	t.Helper()
	want := secretRequirements(names)
	snapshot := fixture.Runtime().Describe()
	if snapshot.Status != agents.AgentWorking || snapshot.Work == nil || snapshot.Work.CommandID != commandID || !slices.Equal(snapshot.Work.MissingRequirements, want) {
		t.Fatalf("unexpected execution requirements: %+v; want %v", snapshot.Work, want)
	}
	if snapshot.Actions[0].Status != actions.Ready || len(snapshot.Actions[0].RequiredSecrets) != 0 || len(snapshot.Actions[0].MissingRequirements) != 0 {
		t.Fatal("execution secret wait changed the action's readiness or declarations")
	}
	if len(want) > 0 {
		record := agents.Agent{AgentSnapshot: snapshot}
		cloned := record.Clone()
		cloned.Work.MissingRequirements[0].Name = "CHANGED"
		if !slices.Equal(record.Work.MissingRequirements, want) {
			t.Fatal("inventory clone shared execution missing requirements")
		}
	}
	for _, message := range fixture.History() {
		if message.Type != controllerevents.AgentUpdated {
			continue
		}
		var announced agents.AgentSnapshot
		if err := json.Unmarshal(message.Payload, &announced); err != nil {
			t.Fatal(err)
		}
		if announced.Work != nil && announced.Work.CommandID == commandID && slices.Equal(announced.Work.MissingRequirements, want) {
			if message.CorrelationID != "" {
				t.Fatal("secret wait announcement claimed a command outcome")
			}
			return
		}
	}
	t.Fatalf("execution requirements were not announced: %v", want)
}

func secretRequirements(names []string) []actions.Requirement {
	var requirements []actions.Requirement
	for _, name := range names {
		requirements = append(requirements, actions.Requirement{Kind: actions.SecretRequirement, Name: name})
	}
	return requirements
}
