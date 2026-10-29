package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
)

func TestAgentSecretDeliveryChecksBindingAndRetriesExactAcknowledgement(t *testing.T) {
	fixture := fixtures.StartSecretAgent(t, 2*time.Second, time.Second)
	request := fixture.Request(t, "")
	retry := fixture.Next(t, controllerevents.SecretSyncRequested)
	firstMessage, err := controllerevents.NewSecretSyncRequested(request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstMessage, retry) {
		t.Fatal("retry changed the pending request")
	}
	if fixture.Runtime().Describe().Actions[0].Status != actions.Unavailable {
		t.Fatal("missing secret advertised ready")
	}
	assertMissing(t, fixture)

	metadata := fixture.SecretMetadata(request, time.Time{})
	delivery, err := agentevents.NewSecretDelivery(request, metadata, []byte("fixture-value"))
	if err != nil {
		t.Fatal(err)
	}
	originalPayload := bytes.Clone(delivery.Payload)
	for _, mutate := range []func(messages.Message) messages.Message{
		func(m messages.Message) messages.Message { m.AgentID = "other-agent-12345678"; return m },
		func(m messages.Message) messages.Message { m.CorrelationID = id.New(); return m },
		func(m messages.Message) messages.Message {
			copy := metadata
			copy.Key.Name = "OTHER_TOKEN"
			m.Payload, _ = json.Marshal(agentevents.SecretDelivery{Metadata: copy, Value: []byte("fixture-value")})
			return m
		},
		func(m messages.Message) messages.Message {
			m.Payload, _ = json.Marshal(agentevents.SecretDelivery{Metadata: metadata})
			return m
		},
	} {
		fixture.Deliver(t, mutate(delivery))
		assertMissing(t, fixture)
	}
	fixture.FailNextAcknowledgement()
	fixture.Deliver(t, delivery)
	ack := fixture.Next(t, controllerevents.SecretInstalled)
	if err := controllerevents.ValidateSecretInstalledDelivery(ack, delivery); err != nil {
		t.Fatal(err)
	}
	assertValue(t, fixture, "fixture-value")
	conflicting := delivery
	conflicting.Payload, err = json.Marshal(agentevents.SecretDelivery{Metadata: metadata, Value: []byte("conflicting-value")})
	if err != nil {
		t.Fatal(err)
	}

	fixture.Deliver(t, conflicting)

	assertValue(t, fixture, "fixture-value")
	if _, err := action.Secret(fixture.SecretContext(), "UNDECLARED"); !errors.Is(err, action.ErrSecretUnavailable) {
		t.Fatal("undeclared lookup was allowed")
	}
	if bytes.Contains(ack.Payload, []byte("fixture-value")) || bytes.Contains(firstMessage.Payload, []byte("fixture-value")) {
		t.Fatal("request or acknowledgement echoed the secret value")
	}
	fixture.Deliver(t, delivery)
	if replay := fixture.Next(t, controllerevents.SecretInstalled); !reflect.DeepEqual(ack, replay) {
		t.Fatal("installation retry changed identity or timestamps")
	}
	copy, err := action.Secret(fixture.SecretContext(), "TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	clear(copy)
	assertValue(t, fixture, "fixture-value")
	fixture.Stop(t)
	assertMissing(t, fixture)
	if !bytes.Equal(delivery.Payload, originalPayload) {
		t.Fatal("agent installation or shutdown cleared caller-owned delivery bytes")
	}
}

func TestAgentSecretRenewalRejectsStaleDeliveryAndExpiresWithoutReplacement(t *testing.T) {
	fixture := fixtures.StartSecretAgent(t, time.Second, 120*time.Millisecond)
	first := fixture.Request(t, "")
	metadata := fixture.SecretMetadata(first, time.Now().UTC().Add(450*time.Millisecond))
	old, err := agentevents.NewSecretDelivery(first, metadata, []byte("version-one"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.Deliver(t, old)
	fixture.Next(t, controllerevents.SecretInstalled)
	renewed := fixture.Request(t, first.ID)
	if !renewed.ExpiresAt.After(first.ExpiresAt) {
		t.Fatal("renewal did not use a fresh lease")
	}
	metadata.Version++
	metadata.UpdatedAt = time.Now().UTC()
	delivery, err := agentevents.NewSecretDelivery(renewed, metadata, []byte("version-two"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.Deliver(t, delivery)
	for {
		ack := fixture.Next(t, controllerevents.SecretInstalled)
		if ack.CorrelationID == delivery.ID {
			break
		}
	}
	fixture.Deliver(t, old)
	assertValue(t, fixture, "version-two")
	deadline := time.NewTimer(time.Until(metadata.ExpiresAt.Add(10 * time.Millisecond)))
	defer deadline.Stop()
	select {
	case <-deadline.C:
	case <-fixture.Context().Done():
		t.Fatal("lease never expired")
	}
	assertMissing(t, fixture)
	if fixture.Runtime().Describe().Actions[0].Status != actions.Unavailable {
		t.Fatal("expired installed secret advertised ready")
	}
}

func TestMissingSecretInDiscoveryKeepsSynchronizationAndUnrelatedActionsAvailable(t *testing.T) {
	fixture := fixtures.StartSecretAgent(t, 2*time.Second, time.Second, true)
	request := fixture.Request(t, "")
	snapshot := fixture.Runtime().Describe()
	if snapshot.Actions[0].Status != actions.Unavailable || snapshot.Actions[1].Status != actions.Ready {
		t.Fatal("missing secret blocked unrelated actions or allowed the affected action")
	}
	metadata := fixture.SecretMetadata(request, time.Time{})
	delivery, err := agentevents.NewSecretDelivery(request, metadata, []byte("fixture-value"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.Deliver(t, delivery)
	for {
		fixture.Next(t, controllerevents.AgentUpdated)
		if fixture.Runtime().Describe().Actions[0].Status == actions.Ready {
			break
		}
	}
	assertValue(t, fixture, "fixture-value")
}

func TestAgentWaitsForSecretRediscoveryBeforeStartingQueuedWork(t *testing.T) {
	for _, expires := range []bool{false, true} {
		t.Run(map[bool]string{false: "waits", true: "rechecks deadline"}[expires], func(t *testing.T) {
			fixture := fixtures.StartSecretAgent(t, 2*time.Second, time.Second)
			request := fixture.Request(t, "")
			started, release := make(chan struct{}), make(chan struct{})
			fixture.PauseDiscovery(started, release)
			metadata := fixture.SecretMetadata(request, time.Time{})
			delivery, err := agentevents.NewSecretDelivery(request, metadata, []byte("fixture-value"))
			if err != nil {
				t.Fatal(err)
			}
			fixture.Deliver(t, delivery)
			select {
			case <-started:
			case <-fixture.Context().Done():
				t.Fatal("secret rediscovery did not start")
			}
			command, err := agentcommands.NewRunAction(fixture.Runtime().ID(), actions.ActionCall{ActionName: "build", Input: []byte(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			if expires {
				command.ExpiresAt = time.Now().Add(30 * time.Millisecond)
			}
			queued := fakes.NewSecretDelivery(command)
			fixture.SendCommand(t, queued)
			if expires {
				timer := time.NewTimer(time.Until(command.ExpiresAt.Add(10 * time.Millisecond)))
				select {
				case <-timer.C:
				case <-fixture.Context().Done():
					t.Fatal("fixture expired")
				}
				timer.Stop()
			}
			close(release)
			kind := controllerevents.ActionSucceeded
			if expires {
				kind = controllerevents.CommandFailed
			}
			result := fixture.Next(t, kind)
			if result.CorrelationID != command.ID {
				t.Fatal("queued command lost its identity")
			}
			wantCalls := int32(1)
			if expires {
				wantCalls = 0
			}
			if fixture.ExecutionCount() != wantCalls {
				t.Fatal("queued command executed incorrectly")
			}
		})
	}
}

func TestAgentRequestsDynamicSecretOnlyWhenUsedAndReusesInstalledValue(t *testing.T) {
	const plaintext = "dynamic-secret-value-sentinel"
	type input struct {
		Private bool `json:"private"`
	}
	type output struct {
		Matched bool `json:"matched"`
	}
	var privateCalls atomic.Int32
	resumed, release := make(chan struct{}), make(chan struct{})
	executor, err := action.NewFunc(action.FuncConfig[input, output]{
		Action: actions.Action{Name: "inspect"},
		Run: func(ctx context.Context, in input) (output, error) {
			if !in.Private {
				return output{Matched: true}, nil
			}
			privateCalls.Add(1)
			value, err := action.RequestSecret(ctx, "TOKEN")
			defer clear(value)
			if err == nil {
				undeclared, lookupErr := action.Secret(ctx, "TOKEN")
				clear(undeclared)
				if !errors.Is(lookupErr, action.ErrSecretUnavailable) {
					return output{}, errors.New("dynamic request granted undeclared static lookup")
				}
				if privateCalls.Load() == 1 {
					close(resumed)
					select {
					case <-release:
					case <-ctx.Done():
						return output{}, ctx.Err()
					}
				}
			}
			return output{Matched: bytes.Equal(value, []byte(plaintext))}, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := fixtures.StartSecretAgentActions(t, 10*time.Second, 8*time.Second, executor)
	if snapshot := fixture.Runtime().Describe(); snapshot.Actions[0].Status != action.Ready || len(snapshot.Actions[0].RequiredSecrets) != 0 {
		t.Fatal("on-demand secret changed startup readiness or declarations")
	}
	public := fixture.Command(t, "inspect", `{"private":false}`)
	fixture.Outcome(t, public, controllerevents.ActionSucceeded)
	for _, message := range fixture.History() {
		if message.Type == controllerevents.SecretSyncRequested {
			t.Fatal("startup or public work requested a dynamic secret")
		}
	}
	work := messages.WorkContext{OperationID: id.New(), TaskID: id.New()}
	command := fixture.Command(t, "inspect", `{"private":true}`, work)
	request := fixture.Request(t, "")
	if request.AgentType != "fixture" || request.Key.Name != "TOKEN" || request.AgentID != fixture.Runtime().ID() {
		t.Fatal("dynamic request lost its recipient or shared name")
	}
	if fixture.Runtime().Status() != agent.AgentWorking || privateCalls.Load() != 1 {
		t.Fatal("request did not suspend the active action")
	}
	assertSecretWorkRequirements(t, fixture, command, "TOKEN")
	snapshot := fixture.Runtime().Describe()
	if snapshot.Work.OperationID != work.OperationID || snapshot.Work.TaskID != work.TaskID {
		t.Fatal("secret wait lost the active operation work identity")
	}
	snapshot.Work.MissingRequirements[0].Name = "CHANGED"
	assertSecretWorkRequirements(t, fixture, command, "TOKEN")
	for _, message := range fixture.History() {
		if message.CorrelationID == command && (message.Type == controllerevents.ActionSucceeded || message.Type == controllerevents.CommandFailed) {
			t.Fatal("action finished before receiving the requested secret")
		}
	}
	delivery, err := agentevents.NewSecretDelivery(request, fixture.SecretMetadata(request, time.Time{}), []byte(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	fixture.Deliver(t, delivery)
	select {
	case <-resumed:
	case <-fixture.Context().Done():
		t.Fatal("installed secret did not wake the active action")
	}
	assertSecretWorkRequirements(t, fixture, command)
	close(release)
	result := fixture.Outcome(t, command, controllerevents.ActionSucceeded)
	var accepted actions.ActionOutput
	var decoded output
	if json.Unmarshal(result.Payload, &accepted) != nil || json.Unmarshal(accepted.Output, &decoded) != nil || !decoded.Matched {
		t.Fatal("secret delivery did not resume the original action with its value")
	}
	// Installation acknowledgement must survive the last waiter returning.
	acknowledged := false
	for !acknowledged {
		for _, message := range fixture.History() {
			if message.Type == controllerevents.SecretInstalled && message.CorrelationID == delivery.ID {
				if err := controllerevents.ValidateSecretInstalledDelivery(message, delivery); err != nil {
					t.Fatal(err)
				}
				acknowledged = true
			}
		}
		if !acknowledged {
			fixture.Next(t, controllerevents.SecretInstalled)
		}
	}
	second := fixture.Command(t, "inspect", `{"private":true}`)
	result = fixture.Outcome(t, second, controllerevents.ActionSucceeded)
	if json.Unmarshal(result.Payload, &accepted) != nil || json.Unmarshal(accepted.Output, &decoded) != nil || !decoded.Matched {
		t.Fatal("cached secret reuse returned a different value")
	}
	if privateCalls.Load() != 2 {
		t.Fatal("secret delivery repeated an execution or prevented cached reuse")
	}
	for _, message := range fixture.History() {
		if message.Type == controllerevents.SecretSyncRequested {
			payload, err := controllerevents.DecodeSecretSyncRequested(message)
			if err != nil || payload.Request.ID != request.ID {
				t.Fatal("cached secret lookup created another request")
			}
		}
		if message.Type == controllerevents.AgentUpdated {
			var snapshot agent.AgentSnapshot
			if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
				t.Fatal(err)
			}
			if snapshot.Work != nil && (snapshot.Work.CommandID == public || snapshot.Work.CommandID == second) && len(snapshot.Work.MissingRequirements) != 0 {
				t.Fatal("public work or cached secret reuse advertised a wait")
			}
		}
	}
	fixture.Stop(t)
	if strings.Contains(fixture.Logs(), plaintext) {
		t.Fatal("agent logged the requested secret")
	}
	for _, message := range fixture.History() {
		if bytes.Contains(message.Payload, []byte(plaintext)) {
			t.Fatal("agent published the requested secret in metadata or output")
		}
	}
}

func TestAgentDynamicSecretWaitEndsOnCancellation(t *testing.T) {
	for _, stopHost := range []bool{false, true} {
		t.Run(map[bool]string{false: "request", true: "host"}[stopHost], func(t *testing.T) {
			cancelRequest := make(chan context.CancelFunc, 1)
			returned := make(chan error, 1)
			executor, err := action.NewFunc(action.FuncConfig[struct{}, struct{}]{
				Action: actions.Action{Name: "wait"},
				Run: func(ctx context.Context, _ struct{}) (struct{}, error) {
					ctx, cancel := context.WithCancel(ctx)
					defer cancel()
					cancelRequest <- cancel
					value, err := action.RequestSecret(ctx, "TOKEN")
					clear(value)
					returned <- err
					return struct{}{}, err
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			fixture := fixtures.StartSecretAgentActions(t, time.Second, 100*time.Millisecond, executor)
			command := fixture.Command(t, "wait", `{}`)
			request := fixture.Request(t, "")
			assertSecretWorkRequirements(t, fixture, command, "TOKEN")
			if stopHost {
				fixture.Stop(t)
			} else {
				(<-cancelRequest)()
				fixture.Outcome(t, command, controllerevents.CommandFailed)
				if fixture.Runtime().Status() != agent.AgentReady {
					t.Fatal("canceling the request did not release the agent for other work")
				}
			}
			select {
			case err := <-returned:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled secret wait returned %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled secret waiter did not return")
			}
			if snapshot := fixture.Runtime().Describe(); snapshot.Work != nil {
				t.Fatal("canceled execution retained a runtime secret requirement")
			}
			if stopHost {
				return
			}
			// Observe beyond the refresh interval: a canceled last waiter must not
			// keep requesting fresh leases for an unused dynamic secret.
			quiet := time.NewTimer(250 * time.Millisecond)
			defer quiet.Stop()
			for {
				select {
				case message := <-fixture.Outgoing():
					if message.Type == controllerevents.SecretSyncRequested {
						payload, err := controllerevents.DecodeSecretSyncRequested(message)
						if err != nil || payload.Request.ID != request.ID {
							t.Fatal("canceled last waiter kept refreshing its request")
						}
					}
				case <-quiet.C:
					return
				case <-fixture.Context().Done():
					t.Fatal("agent stopped after canceling only the request")
				}
			}
		})
	}
}

func assertMissing(t *testing.T, f *fixtures.SecretAgent) {
	t.Helper()
	if value, err := action.Secret(f.SecretContext(), "TOKEN"); !errors.Is(err, action.ErrSecretUnavailable) || len(value) != 0 {
		t.Fatal("unavailable secret was readable")
	}
}

func assertValue(t *testing.T, f *fixtures.SecretAgent, expected string) {
	t.Helper()
	value, err := action.Secret(f.SecretContext(), "TOKEN")
	defer clear(value)
	if err != nil || !bytes.Equal(value, []byte(expected)) {
		t.Fatal("installed secret did not match expected version")
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
