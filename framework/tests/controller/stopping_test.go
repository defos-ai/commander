package controller_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestControllerStopSelectorUsesOneExactMatchingInventorySnapshot(t *testing.T) {
	fixture := fixtures.NewStoppingController(t)
	fixture.Config.LeaseSweepInterval = time.Hour
	first := fixtures.StopSelectionRecord("example-agent-00000001", "example-agent", "v1.0.0", "k8s-prod")
	second := fixtures.StopSelectionRecord("example-agent-00000002", "example-agent", "v1.0.0", "k8s-prod")
	newer := fixtures.StopSelectionRecord("example-agent-00000003", "example-agent", "v2.0.0", "k8s-prod")
	other := fixtures.StopSelectionRecord("analyzer-agent-00000001", "analyzer-agent", "v1.0.0", "k8s-prod")
	native := fixtures.StopSelectionRecord("example-agent-00000004", "example-agent", "v1.0.0", "native-host")
	inventory := fakes.NewStopSelectionInventory(second, other, newer, native, first)
	fixture.Config.AgentStore = inventory
	c := fixture.Start(t)
	client := fixture.CommandClient(t)

	selected, err := client.StopAgents(fixture.Context, agents.StopSelector{Type: "example-agent", Version: "v1.0.0", Hostname: "k8s-prod"})

	if err != nil || selected.Validate() != nil || len(selected.Agents) != 2 || len(selected.Errors) != 0 ||
		selected.Agents[0].ID != first.ID || selected.Agents[1].ID != second.ID {
		t.Fatalf("controller did not apply exact AND criteria in stable order: %+v, %v", selected, err)
	}
	for _, stopped := range selected.Agents {
		if stopped.Status != agents.AgentStopRequested {
			t.Fatalf("batch acceptance claimed shutdown: %+v", stopped)
		}
	}
	for _, untouched := range []agents.Agent{newer, other, native} {
		saved, err := inventory.Get(fixture.Context, untouched.ID)
		if err != nil || saved.Status != agents.AgentReady || saved.StopCommandID != "" {
			t.Fatalf("selector stopped a nonmatching agent: %+v, %v", saved, err)
		}
	}
	for _, selector := range []agents.StopSelector{
		{ID: first.ID, Version: "v2.0.0", Hostname: first.Server.Hostname},
		{ID: first.ID, Hostname: native.Server.Hostname},
		{Hostname: "K8S-prod"},
		{Hostname: "k8s-prod."},
	} {
		empty, err := c.StopAgents(fixture.Context, selector)
		if err != nil || empty.Validate() != nil || len(empty.Agents) != 0 || len(empty.Errors) != 0 {
			t.Fatalf("nonmatching selector was not an explicit empty result: selector=%+v result=%+v, %v", selector, empty, err)
		}
	}
	repeated, err := c.StopAgents(fixture.Context, agents.StopSelector{ID: first.ID, Type: first.Type, Version: first.Version, Hostname: first.Server.Hostname})
	if err != nil || len(repeated.Agents) != 1 || !sameStopIdentity(repeated.Agents[0], selected.Agents[0]) {
		t.Fatalf("exact selector changed a retained stop identity: %+v, %v", repeated, err)
	}
	select {
	case publication := <-fixture.Bus.Commands():
		t.Fatalf("batch acceptance published instead of deferring to reconciliation: %+v", publication)
	default:
	}
	for _, raw := range []string{
		`{}`, `{"selector":null}`, `{"selector":{}}`, `{"selector":{"id":"invalid"}}`,
		`{"selector":{"type":"example-agent","version":""}}`, `{"selector":{"type":"example-agent","id":null}}`,
		`{"selector":{"version":"v1.0.0","all":true}}`, `{"selector":{"type":7}}`,
		`{"selector":{"hostname":null}}`, `{"selector":{"hostname":""}}`, `{"selector":{"hostname":7}}`,
		`{"selector":{"hostname":"k8s-prod "}}`, `{"selector":{"hostname":"k8s/prod"}}`,
		`{"selector":{"hostname":"k8s\\prod"}}`, `{"selector":{"hostname":"k8s\u0000prod"}}`,
		`{"selector":{"hostname":"k8s-prod","all":true}}`,
	} {
		var command controllercommands.StopAgentsCommand
		if err := domain.Decode(json.RawMessage(raw), &command); err == nil {
			t.Fatalf("malformed selector was accepted: %s", raw)
		}
	}
	for _, hostname := range []string{" k8s-prod", "k8s prod", "k8s/prod", `k8s\prod`, "k8s\nprod", string([]byte{0xff})} {
		if _, err := client.StopAgents(fixture.Context, agents.StopSelector{Type: "example-agent", Hostname: hostname}); err == nil {
			t.Fatalf("invalid hostname was accepted by the command client: %q", hostname)
		}
	}
	selectedNative, err := client.StopAgents(fixture.Context, agents.StopSelector{Hostname: native.Server.Hostname})
	if err != nil || selectedNative.Validate() != nil || len(selectedNative.Agents) != 1 || len(selectedNative.Errors) != 0 ||
		selectedNative.Agents[0].ID != native.ID || selectedNative.Agents[0].Status != agents.AgentStopRequested {
		t.Fatalf("hostname-only selector did not stop its exact target: %+v, %v", selectedNative, err)
	}
	for _, untouched := range []agents.Agent{newer, other} {
		saved, err := inventory.Get(fixture.Context, untouched.ID)
		if err != nil || saved.Status != agents.AgentReady || saved.StopCommandID != "" {
			t.Fatalf("invalid or hostname-only criteria stopped another agent: %+v, %v", saved, err)
		}
	}
}

func TestControllerStopSelectorReportsPartialFailureWithoutEnrollingReplacement(t *testing.T) {
	fixture := fixtures.NewStoppingController(t)
	fixture.Config.LeaseSweepInterval = time.Hour
	first := fixtures.StopSelectionRecord("example-agent-00000001", "example-agent", "v1.0.0", "native-host")
	disappeared := fixtures.StopSelectionRecord("example-agent-00000002", "example-agent", "v1.0.0", "native-host")
	replacement := fixtures.StopSelectionRecord("example-agent-00000003", "example-agent", "v1.0.0", "native-host")
	inventory := fakes.NewStopSelectionInventory(first, disappeared)
	inventory.ConfigureStopRace(disappeared.ID, replacement)
	fixture.Config.AgentStore = inventory
	c := fixture.Start(t)

	result, err := c.StopAgents(fixture.Context, agents.StopSelector{Version: first.Version})

	if err != nil || result.Validate() != nil || len(result.Agents) != 1 || result.Agents[0].ID != first.ID ||
		len(result.Errors) != 1 || result.Errors[0].ID != disappeared.ID || result.Errors[0].Error != "agent_not_found" {
		t.Fatalf("partial stop lost its accepted target or failure: %+v, %v", result, err)
	}
	registered, err := inventory.Get(fixture.Context, replacement.ID)
	if err != nil || registered.Status != agents.AgentReady || registered.StopCommandID != "" {
		t.Fatalf("a replacement registered after selection was stopped: %+v, %v", registered, err)
	}
}

func TestControllerStopFencesNewWorkWithoutLosingAcceptedResults(t *testing.T) {
	fixture := fixtures.NewStoppingController(t)
	fixture.Config.HeartbeatTimeout = 250 * time.Millisecond
	ctx, bus := fixture.Context, fixture.Bus
	c := fixture.Start(t)
	call := actions.ActionCall{ActionName: "inspect", Input: []byte(`{}`), OperationID: id.New(), TaskID: id.New()}
	type outcome struct {
		output actions.ActionOutput
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		output, err := c.RunAction(ctx, fixtures.AgentID, call)
		finished <- outcome{output: output, err: err}
	}()
	command := fixtures.Receive(t, ctx, bus.Commands())
	if command.Type != agentcommands.RunAction {
		t.Fatalf("expected the original action, got %s", command.Type)
	}
	working := fixtures.ObservationSnapshot()
	working.Status = agents.AgentWorking
	working.Work = &agents.Work{OperationID: call.OperationID, TaskID: call.TaskID, CommandID: command.ID, ActionName: call.ActionName}
	fixtures.ObserveSnapshot(t, ctx, bus, controllerevents.AgentHeartbeat, working, "")
	requestContext, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()

	requested, err := c.StopAgent(requestContext, fixtures.AgentID)
	cancelRequest()

	if err != nil || requested.StopCommandID == "" || requested.StopRequestedAt.IsZero() || requested.Status != agents.AgentStopRequested || !reflect.DeepEqual(requested.Work, working.Work) {
		t.Fatalf("stop did not retain the working observation and durable intent: %+v, %v", requested, err)
	}
	stop := nextStopPublication(t, ctx, bus, requested)
	if !stop.ExpiresAt.IsZero() {
		t.Fatalf("caller cancellation can expire the queued graceful stop: %+v", stop)
	}
	if _, err := c.DiscoverActions(ctx, fixtures.AgentID, nil); !errors.Is(err, agents.ErrAgentNotRunning) {
		t.Fatalf("stopping agent admitted discovery: %v", err)
	}
	newCall := call
	newCall.TaskID = id.New()
	if _, err := c.RunAction(ctx, fixtures.AgentID, newCall); !errors.Is(err, agents.ErrAgentNotRunning) {
		t.Fatalf("stopping agent admitted new work: %v", err)
	}
	fixtures.ObserveSnapshot(t, ctx, bus, controllerevents.AgentHeartbeat, working, "")
	observed, err := c.Agent(ctx, fixtures.AgentID)
	if err != nil || observed.Status != agents.AgentStopRequested || !reflect.DeepEqual(observed.Work, working.Work) || !sameStopIdentity(observed, requested) {
		t.Fatalf("fresh heartbeat or caller cancellation cleared the stop: %+v, %v", observed, err)
	}
	recovering := make(chan outcome, 1)
	go func() {
		output, err := c.RecoverAction(ctx, fixtures.AgentID, call)
		recovering <- outcome{output: output, err: err}
	}()
	heartbeat := time.NewTicker(50 * time.Millisecond)
	defer heartbeat.Stop()
	observation := time.NewTimer(750 * time.Millisecond)
	defer observation.Stop()

waitingForResult:
	for {
		select {
		case outcome := <-recovering:
			t.Fatalf("recovery ended while fresh stopping agent still owned pending work: %+v, %v", outcome.output, outcome.err)
		case <-heartbeat.C:
			fixtures.ObserveSnapshot(t, ctx, bus, controllerevents.AgentHeartbeat, working, "")
		case <-observation.C:
			break waitingForResult
		case <-ctx.Done():
			t.Fatal("pending recovery observation exceeded fixture lifetime")
		}
	}
	result := fixtures.SuccessfulOperationResult(t, command)

	if err := bus.Send(ctx, result); err != nil {
		t.Fatal(err)
	}
	if ack := fixtures.Receive(t, ctx, bus.Acknowledged()); ack != result.ID {
		t.Fatalf("accepted action result was not acknowledged: %s", ack)
	}
	completed := fixtures.Receive(t, ctx, finished)
	if completed.err != nil || !bytes.Equal(completed.output.Output, []byte(`{"ok":true}`)) {
		t.Fatalf("graceful stop lost the accepted action: %+v, %v", completed.output, completed.err)
	}
	for {
		receipt := fixtures.Receive(t, ctx, bus.Commands())
		if receipt.Type == messages.StopAgent {
			assertStopPublication(t, receipt, requested)
			continue
		}
		if receipt.Type != messages.ActionResultAccepted || receipt.CorrelationID != command.ID {
			t.Fatalf("result did not retain its durable acceptance receipt: %+v", receipt)
		}
		break
	}
	recovered := fixtures.Receive(t, ctx, recovering)
	if recovered.err != nil || !bytes.Equal(recovered.output.Output, completed.output.Output) {
		t.Fatalf("stopping agent made an accepted execution inaccessible: %+v, %v", recovered.output, recovered.err)
	}
	ready := fixtures.ObservationSnapshot()
	fixtures.ObserveSnapshot(t, ctx, bus, controllerevents.AgentHeartbeat, ready, "")

	repeated, err := c.StopAgent(ctx, fixtures.AgentID)

	if err != nil || repeated.Status != agents.AgentStopRequested || !sameStopIdentity(repeated, requested) {
		t.Fatalf("ready heartbeat or repeated stop changed the saved request: %+v, %v", repeated, err)
	}
	if _, err := c.RunAction(ctx, fixtures.AgentID, newCall); !errors.Is(err, agents.ErrAgentNotRunning) {
		t.Fatalf("ready heartbeat reopened admission before stop confirmation: %v", err)
	}
	stopped := ready
	stopped.Status = agents.AgentStopped

	fixtures.ObserveSnapshot(t, ctx, bus, controllerevents.AgentStopped, stopped, stop.ID)
	confirmed, err := c.StopAgent(ctx, fixtures.AgentID)

	if err != nil || confirmed.Status != agents.AgentStopped || !sameStopIdentity(confirmed, requested) {
		t.Fatalf("confirmed stop was not idempotent: %+v, %v", confirmed, err)
	}
	assertOnlyStopPublications(t, bus, requested)
}

func TestControllerStopRetainsItsIdentityAfterUncertainPublicationAndRestart(t *testing.T) {
	fixture := fixtures.NewStoppingController(t)
	ctx, bus := fixture.Context, fixture.Bus
	c := fixture.Start(t)
	bus.FailNextStopPublication(errors.New("publication confirmation lost"))
	requestContext, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()

	requested, err := c.StopAgent(requestContext, fixtures.AgentID)
	cancelRequest()

	if err != nil || requested.StopCommandID == "" || requested.StopRequestedAt.IsZero() || requested.Status != agents.AgentStopRequested {
		t.Fatalf("uncertain publication discarded the accepted stop or claimed shutdown: %+v, %v", requested, err)
	}
	first := nextStopPublication(t, ctx, bus, requested)
	retry := nextStopPublication(t, ctx, bus, requested)
	if !reflect.DeepEqual(first, retry) {
		t.Fatalf("publication retry changed the stop envelope: first=%+v retry=%+v", first, retry)
	}
	if err := c.Close(ctx); err != nil {
		t.Fatal(err)
	}
	retained := fixture.Inventory.Snapshot()
	retained.LastHeartbeat = time.Now().UTC().Add(-2 * time.Hour)
	retained.LeaseExpiresAt = retained.LastHeartbeat.Add(time.Hour)
	fixture.Inventory.Set(retained)
	restarted := fixture
	restarted.Bus = fakes.NewBus()

	nextController := restarted.Start(t)
	beforeHeartbeat, err := nextController.Agent(ctx, fixtures.AgentID)

	if err != nil || !beforeHeartbeat.Stale || beforeHeartbeat.Status == agents.AgentStopped || !sameStopIdentity(beforeHeartbeat, requested) {
		t.Fatalf("restart or lease expiry lost an unconfirmed stop: %+v, %v", beforeHeartbeat, err)
	}
	fixtures.ObserveSnapshot(t, ctx, restarted.Bus, controllerevents.AgentHeartbeat, fixtures.ObservationSnapshot(), "")
	replayed := nextStopPublication(t, ctx, restarted.Bus, requested)
	if !reflect.DeepEqual(replayed, first) {
		t.Fatalf("controller restart changed the saved stop envelope: first=%+v replay=%+v", first, replayed)
	}
	if _, err := nextController.DiscoverActions(ctx, fixtures.AgentID, nil); !errors.Is(err, agents.ErrAgentNotRunning) {
		t.Fatalf("a fresh heartbeat reopened admission after restart: %v", err)
	}
	stopped := fixtures.ObservationSnapshot()
	stopped.Status = agents.AgentStopped
	fixtures.ObserveSnapshot(t, ctx, restarted.Bus, controllerevents.AgentStopped, stopped, replayed.ID)
	confirmed, err := nextController.Agent(ctx, fixtures.AgentID)
	if err != nil || confirmed.Status != agents.AgentStopped {
		t.Fatalf("stop confirmation was not retained after restart: %+v, %v", confirmed, err)
	}
	assertOnlyStopPublications(t, restarted.Bus, requested)
}

func nextStopPublication(t *testing.T, ctx context.Context, bus *fakes.Bus, request agents.Agent) messages.Message {
	t.Helper()
	message := fixtures.Receive(t, ctx, bus.Commands())
	assertStopPublication(t, message, request)
	return message
}

func assertStopPublication(t *testing.T, message messages.Message, request agents.Agent) {
	t.Helper()
	if request.StopCommandID == "" || request.StopRequestedAt.IsZero() || message.Type != messages.StopAgent || message.AgentID != fixtures.AgentID || message.ID != request.StopCommandID || !message.CreatedAt.Equal(request.StopRequestedAt) || !message.ExpiresAt.IsZero() {
		t.Fatalf("stop publication changed its saved identity or admitted other work: %+v, request=%+v", message, request)
	}
}

func assertOnlyStopPublications(t *testing.T, bus *fakes.Bus, request agents.Agent) {
	t.Helper()
	for {
		select {
		case message := <-bus.Commands():
			assertStopPublication(t, message, request)
		default:
			return
		}
	}
}

func sameStopIdentity(left, right agents.Agent) bool {
	return left.StopCommandID == right.StopCommandID && left.StopRequestedAt.Equal(right.StopRequestedAt)
}
