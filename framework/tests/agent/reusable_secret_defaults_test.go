package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/secrets"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
)

func TestAgentReusableDefaultRetainsItsRequestAndReusesInstalledValue(t *testing.T) {
	for _, withDefault := range []bool{false, true} {
		t.Run(map[bool]string{false: "without default", true: "with default"}[withDefault], func(t *testing.T) {
			var defaultValue []byte
			if withDefault {
				defaultValue = []byte("offered-default")
			}
			type output struct {
				Matched bool `json:"matched"`
			}
			executor, err := action.NewFunc(action.FuncConfig[struct{}, output]{
				Action: actions.Action{Name: "use-default"},
				Run: func(ctx context.Context, _ struct{}) (output, error) {
					value, err := action.RequestReusableSecret(ctx, "TOKEN", action.SecretRequestOptions{Default: defaultValue})
					defer clear(value)
					return output{Matched: bytes.Equal(value, []byte("managed-winner"))}, err
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			fixture := fixtures.StartSecretAgentActions(t, time.Second, 40*time.Millisecond, executor)
			command := fixture.Command(t, "use-default", `{}`)
			first := fixture.Next(t, controllerevents.SecretSyncRequested)
			payload, err := controllerevents.DecodeSecretSyncRequested(first)
			if err != nil || !bytes.Equal(payload.Default, defaultValue) {
				t.Fatal("reusable request did not carry its default")
			}
			clear(payload.Default)
			request := payload.Request
			observed := time.NewTimer(100 * time.Millisecond)
			defer observed.Stop()

		retrying:
			for {
				select {
				case message := <-fixture.Outgoing():
					if message.Type == controllerevents.SecretSyncRequested && (message.ID != first.ID || !bytes.Equal(message.Payload, first.Payload) || !message.ExpiresAt.Equal(first.ExpiresAt)) {
						t.Fatal("default refresh changed the request identity, bytes or original expiry")
					}
				case <-observed.C:
					break retrying
				case <-fixture.Context().Done():
					t.Fatal("default wait stopped before its retry was observed")
				}
			}
			delivery, err := agentevents.NewSecretDelivery(request, fixture.SecretMetadata(request, time.Time{}), []byte("managed-winner"))
			if err != nil {
				t.Fatal(err)
			}
			fixture.Deliver(t, delivery)
			fixture.Outcome(t, command, controllerevents.ActionSucceeded)
			second := fixture.Command(t, "use-default", `{}`)
			result := fixture.Outcome(t, second, controllerevents.ActionSucceeded)

			var resultOutput actions.ActionOutput
			if json.Unmarshal(result.Payload, &resultOutput) != nil || !bytes.Equal(resultOutput.Output, []byte(`{"matched":true}`)) {
				t.Fatal("another default displaced the installed managed value")
			}
			for _, message := range fixture.History() {
				if message.Type == controllerevents.SecretSyncRequested && message.ID != request.ID {
					t.Fatal("cached reuse created another default request")
				}
			}
		})
	}
}

func TestAgentReusableDefaultEndsWithoutLeakingIntoLaterRequests(t *testing.T) {
	for _, reason := range []string{"cancel", "expiry", "host"} {
		t.Run(reason, func(t *testing.T) {
			cancelRequest := make(chan context.CancelFunc, 1)
			returned := make(chan error, 2)
			type input struct {
				Default bool `json:"default"`
			}
			executor, err := action.NewFunc(action.FuncConfig[input, struct{}]{
				Action: actions.Action{Name: "wait-default"},
				Run: func(ctx context.Context, input input) (struct{}, error) {
					var value []byte
					var err error
					if input.Default {
						request, cancel := context.WithCancel(ctx)
						defer cancel()
						cancelRequest <- cancel
						value, err = action.RequestReusableSecret(request, "TOKEN", action.SecretRequestOptions{Default: []byte("expired-default")})
					} else {
						value, err = action.RequestReusableSecret(ctx, "TOKEN")
					}
					clear(value)
					returned <- err
					return struct{}{}, err
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			fixture := fixtures.StartSecretAgentActions(t, 180*time.Millisecond, 40*time.Millisecond, executor)
			command := fixture.Command(t, "wait-default", `{"default":true}`)
			original := fixture.Request(t, "")
			if reason == "expiry" {
				request := nextOrdinarySecretRequest(t, fixture, original.ID)
				select {
				case <-returned:
					t.Fatal("default expiry ended the reusable caller instead of waiting for a managed value")
				default:
				}
				delivery, err := agentevents.NewSecretDelivery(request, fixture.SecretMetadata(request, time.Time{}), []byte("operator-value"))
				if err != nil {
					t.Fatal(err)
				}
				fixture.Deliver(t, delivery)
				fixture.Outcome(t, command, controllerevents.ActionSucceeded)
				if err := <-returned; err != nil {
					t.Fatal("ordinary request after default expiry did not resume its caller")
				}
				return
			}

			switch reason {
			case "cancel":
				(<-cancelRequest)()
			case "host":
				fixture.Stop(t)
			}
			select {
			case err := <-returned:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("ended default wait returned %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("ended default wait did not return")
			}
			if reason == "host" {
				return
			}
			fixture.Outcome(t, command, controllerevents.CommandFailed)
			second := fixture.Command(t, "wait-default", `{"default":false}`)
			request := nextOrdinarySecretRequest(t, fixture, original.ID)
			delivery, err := agentevents.NewSecretDelivery(request, fixture.SecretMetadata(request, time.Time{}), []byte("operator-value"))
			if err != nil {
				t.Fatal(err)
			}
			fixture.Deliver(t, delivery)
			fixture.Outcome(t, second, controllerevents.ActionSucceeded)
		})
	}
}

func TestConcurrentReusableCallersKeepFirstDefaultWhenItsCallerCancels(t *testing.T) {
	executor := fakes.NewConcurrentReusableDefaults()
	fixture := fixtures.StartSecretAgentActions(t, time.Second, 40*time.Millisecond, executor)
	command := fixture.Command(t, "join-default", `{}`)
	awaitSecretWaitSnapshot(t, fixture)
	first := fixture.Next(t, controllerevents.SecretSyncRequested)
	payload, err := controllerevents.DecodeSecretSyncRequested(first)
	if err != nil || string(payload.Default) != "first-default" {
		t.Fatal("first caller's default was not attached to its pending request")
	}
	clear(payload.Default)
	executor.StartSecond()
	awaitSecretWaitSnapshot(t, fixture)

	if err := executor.CancelFirst(fixture.Context()); err != nil {
		t.Fatal("could not cancel the first reusable caller")
	}
	select {
	case err := <-executor.FirstReturned():
		if !errors.Is(err, context.Canceled) {
			t.Fatal("first reusable caller did not observe cancellation")
		}
	case <-fixture.Context().Done():
		t.Fatal("first reusable caller did not finish")
	}
	retry := fixture.Next(t, controllerevents.SecretSyncRequested)
	if retry.ID != first.ID || !bytes.Equal(retry.Payload, first.Payload) || !retry.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatal("cancelling a caller promoted another default or replaced the pending request")
	}
	delivery, err := agentevents.NewSecretDelivery(payload.Request, fixture.SecretMetadata(payload.Request, time.Time{}), []byte("managed-winner"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.Deliver(t, delivery)
	result := fixture.Outcome(t, command, controllerevents.ActionSucceeded)

	var output actions.ActionOutput
	if json.Unmarshal(result.Payload, &output) != nil || !bytes.Equal(output.Output, []byte(`{"first_cancelled":true,"second_matched":true}`)) {
		t.Fatal("remaining reusable caller did not receive the first request's managed delivery")
	}
	for _, message := range fixture.History() {
		if message.Type == controllerevents.SecretSyncRequested && message.ID != first.ID {
			t.Fatal("same-name callers created competing pending requests")
		}
	}
}

func awaitSecretWaitSnapshot(t *testing.T, fixture *fixtures.SecretAgent) {
	t.Helper()
	for {
		message := fixture.Next(t, controllerevents.AgentUpdated)
		var snapshot struct {
			Work *struct {
				MissingRequirements []actions.Requirement `json:"missing_requirements"`
			} `json:"work"`
		}
		if json.Unmarshal(message.Payload, &snapshot) != nil {
			t.Fatal("agent returned an invalid work snapshot")
		}
		if snapshot.Work != nil && len(snapshot.Work.MissingRequirements) != 0 {
			return
		}
	}
}

func nextOrdinarySecretRequest(t *testing.T, fixture *fixtures.SecretAgent, previous string) secrets.Request {
	t.Helper()
	for {
		message := fixture.Next(t, controllerevents.SecretSyncRequested)
		if message.ID == previous {
			continue
		}
		payload, err := controllerevents.DecodeSecretSyncRequested(message)
		if err != nil || payload.Default != nil {
			clear(payload.Default)
			t.Fatal("ended default was reused by another request")
		}
		return payload.Request
	}
}
