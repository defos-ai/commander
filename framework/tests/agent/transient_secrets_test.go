package agent_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
)

func TestAgentTransientSecretsRetryWithoutCachingOrRepeatingHandoff(t *testing.T) {
	const firstValue, secondValue = "first-code-sentinel", "second-code-sentinel"
	received, release := make(chan struct{}), make(chan struct{})
	retained := make(chan context.Context, 1)
	type output struct {
		Matched bool `json:"matched"`
	}
	executor, err := action.NewFunc(action.FuncConfig[struct{}, output]{
		Action: actions.Action{Name: "verify"},
		Run: func(ctx context.Context, _ struct{}) (output, error) {
			retained <- context.WithoutCancel(ctx)
			first, err := action.RequestTransientSecret(ctx, "CODE")
			defer clear(first)
			if err != nil {
				return output{}, err
			}
			cached, cachedErr := action.Secret(ctx, "CODE")
			clear(cached)
			if !errors.Is(cachedErr, action.ErrSecretUnavailable) {
				return output{}, errors.New("transient value entered reusable cache")
			}
			close(received)
			select {
			case <-release:
			case <-ctx.Done():
				return output{}, ctx.Err()
			}
			second, err := action.RequestSecret(ctx, "CODE", action.SecretRequestOptions{Type: action.SecretTransient})
			defer clear(second)
			return output{Matched: bytes.Equal(first, []byte(firstValue)) && bytes.Equal(second, []byte(secondValue))}, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := fixtures.StartSecretAgentActions(t, 2*time.Second, time.Second, executor)
	work := messages.WorkContext{OperationID: id.New(), TaskID: id.New()}
	command := fixture.Command(t, "verify", `{}`, work)
	request := fixture.Request(t, "")
	if request.Type != secrets.SecretTransient || request.OperationID != work.OperationID || request.TaskID != work.TaskID || request.CommandID != command {
		t.Fatal("transient request lost its execution identity")
	}
	firstRequest, err := controllerevents.NewSecretSyncRequested(request)
	if err != nil {
		t.Fatal(err)
	}
	if retry := fixture.Next(t, controllerevents.SecretSyncRequested); !reflect.DeepEqual(firstRequest, retry) {
		t.Fatal("transient retry changed its identity or deadline")
	}
	delivery, err := agentevents.NewTransientSecretResponse(request, []byte(firstValue), "")
	if err != nil {
		t.Fatal(err)
	}
	wrongWork := delivery
	wrongWork.Work.TaskID = id.New()
	fixture.Deliver(t, wrongWork)
	assertSecretWorkRequirements(t, fixture, command, "CODE")
	select {
	case <-received:
		t.Fatal("wrong execution received a transient value")
	default:
	}

	fixture.FailNextAcknowledgement()
	fixture.Deliver(t, delivery)
	select {
	case <-received:
	case <-fixture.Context().Done():
		t.Fatal("transient wait did not receive its value")
	}
	ack := fixture.Next(t, controllerevents.SecretTransientReceived)
	if err := controllerevents.ValidateTransientSecretReceivedDelivery(ack, delivery); err != nil {
		t.Fatal(err)
	}
	fixture.Deliver(t, delivery)
	if retry := fixture.Next(t, controllerevents.SecretTransientReceived); !reflect.DeepEqual(ack, retry) {
		t.Fatal("transient receipt retry changed its identity")
	}
	assertSecretWorkRequirements(t, fixture, command)
	close(release)
	second := fixture.Request(t, request.ID)
	assertSecretWorkRequirements(t, fixture, command, "CODE")
	fixture.Deliver(t, delivery)
	assertSecretWorkRequirements(t, fixture, command, "CODE")
	next, err := agentevents.NewTransientSecretResponse(second, []byte(secondValue), "")
	if err != nil {
		t.Fatal(err)
	}
	fixture.Deliver(t, next)
	result := fixture.Outcome(t, command, controllerevents.ActionSucceeded)

	var accepted actions.ActionOutput
	var decoded output
	if json.Unmarshal(result.Payload, &accepted) != nil || json.Unmarshal(accepted.Output, &decoded) != nil || !decoded.Matched {
		t.Fatal("transient requests did not receive independent values")
	}
	value, err := action.RequestTransientSecret(<-retained, "CODE")
	clear(value)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ended execution accepted another transient wait: %v", err)
	}
	fixture.Stop(t)
	for _, private := range []string{firstValue, secondValue} {
		if strings.Contains(fixture.Logs(), private) {
			t.Fatal("transient value leaked into logs")
		}
		for _, message := range fixture.History() {
			if bytes.Contains(message.Payload, []byte(private)) || bytes.Contains(message.Payload, []byte(base64.StdEncoding.EncodeToString([]byte(private)))) {
				t.Fatal("agent published a transient value in observations or results")
			}
		}
	}
}

func TestAgentTransientWaitEndsWithoutRenewal(t *testing.T) {
	for _, stop := range []string{"cancel", "expire", "controller", "host"} {
		t.Run(stop, func(t *testing.T) {
			cancelRequest := make(chan context.CancelFunc, 1)
			returned := make(chan error, 1)
			executor, err := action.NewFunc(action.FuncConfig[struct{}, struct{}]{
				Action: actions.Action{Name: "verify"},
				Run: func(ctx context.Context, _ struct{}) (struct{}, error) {
					ctx, cancel := context.WithCancel(ctx)
					defer cancel()
					cancelRequest <- cancel
					value, err := action.RequestTransientSecret(ctx, "CODE")
					clear(value)
					returned <- err
					return struct{}{}, err
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			fixture := fixtures.StartSecretAgentActions(t, 250*time.Millisecond, 50*time.Millisecond, executor)
			command := fixture.Command(t, "verify", `{}`, messages.WorkContext{OperationID: id.New(), TaskID: id.New()})
			request := fixture.Request(t, "")
			switch stop {
			case "cancel":
				(<-cancelRequest)()
			case "controller":
				response, err := agentevents.NewTransientSecretResponse(request, nil, agentevents.TransientSecretInactive)
				if err != nil {
					t.Fatal(err)
				}
				fixture.Deliver(t, response)
			case "host":
				fixture.Stop(t)
			}

			select {
			case err := <-returned:
				want := context.Canceled
				if stop == "expire" {
					want = context.DeadlineExceeded
				}
				if stop == "controller" {
					want = action.ErrSecretUnavailable
				}
				if !errors.Is(err, want) {
					t.Fatalf("transient wait returned %v, want %v", err, want)
				}
			case <-time.After(time.Second):
				t.Fatal("transient wait did not end")
			}
			if stop != "host" {
				fixture.Outcome(t, command, controllerevents.CommandFailed)
				if stop == "cancel" {
					canceled := fixture.Next(t, controllerevents.SecretRequestCanceled)
					payload, err := controllerevents.DecodeSecretRequestCanceled(canceled)
					if err != nil || !reflect.DeepEqual(payload.Request, request) {
						t.Fatal("cancellation lost its original request")
					}
				}
				fixture.Stop(t)
			}
			for _, message := range fixture.History() {
				if message.Type != controllerevents.SecretSyncRequested {
					continue
				}
				payload, err := controllerevents.DecodeSecretSyncRequested(message)
				if err != nil || !reflect.DeepEqual(payload.Request, request) {
					t.Fatal("transient wait renewed its identity or deadline")
				}
			}
		})
	}
}
