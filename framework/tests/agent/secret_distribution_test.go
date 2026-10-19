package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/comms"
	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

func TestAgentSecretDeliveryChecksBindingAndRetriesExactAcknowledgement(t *testing.T) {
	fixture := startSecretAgent(t, 2*time.Second, time.Second)
	request := fixture.request(t, "")
	retry := fixture.next(t, controllerevents.SecretSyncRequested)
	firstMessage, err := controllerevents.NewSecretSyncRequested(request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstMessage, retry) {
		t.Fatal("retry changed the pending request")
	}
	if fixture.runtime.Describe().Actions[0].Status != actions.Unavailable {
		t.Fatal("missing secret advertised ready")
	}
	fixture.assertMissing(t)

	secret := fixture.secret(t, request, time.Time{}, "fixture-value")
	delivery, err := agentevents.NewSecretDelivery(request, secret)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(messages.Message) messages.Message{
		func(m messages.Message) messages.Message { m.AgentID = "other-agent-12345678"; return m },
		func(m messages.Message) messages.Message { m.CorrelationID = id.New(); return m },
		func(m messages.Message) messages.Message {
			copy := secret.Clone()
			copy.Metadata.Key.Scope.AgentType = "other"
			m.Payload, _ = json.Marshal(agentevents.SecretDelivery{Secret: copy})
			return m
		},
		func(m messages.Message) messages.Message {
			copy := secret.Clone()
			copy.Value.Ciphertext[0] ^= 1
			m.Payload, _ = json.Marshal(agentevents.SecretDelivery{Secret: copy})
			return m
		},
	} {
		fixture.deliver(t, mutate(delivery))
		fixture.assertMissing(t)
	}
	fixture.bus.failAcknowledgement.Store(true)
	fixture.deliver(t, delivery)
	ack := fixture.next(t, controllerevents.SecretInstalled)
	if err := controllerevents.ValidateSecretInstalledDelivery(ack, delivery); err != nil {
		t.Fatal(err)
	}
	fixture.assertValue(t, "fixture-value")
	if _, err := action.Secret(fixture.executor.context(), "UNDECLARED"); !errors.Is(err, action.ErrSecretUnavailable) {
		t.Fatal("undeclared lookup was allowed")
	}
	if bytes.Contains(ack.Payload, []byte("fixture-value")) || bytes.Contains(firstMessage.Payload, []byte("fixture-value")) {
		t.Fatal("plaintext was published")
	}
	fixture.deliver(t, delivery)
	if replay := fixture.next(t, controllerevents.SecretInstalled); !reflect.DeepEqual(ack, replay) {
		t.Fatal("installation retry changed identity or timestamps")
	}
	copy, err := action.Secret(fixture.executor.context(), "TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	clear(copy)
	fixture.assertValue(t, "fixture-value")
	fixture.stop(t)
	fixture.assertMissing(t)
}

func TestAgentSecretRenewalRejectsStaleDeliveryAndExpiresWithoutReplacement(t *testing.T) {
	fixture := startSecretAgent(t, time.Second, 120*time.Millisecond)
	first := fixture.request(t, "")
	secret := fixture.secret(t, first, time.Now().UTC().Add(450*time.Millisecond), "version-one")
	old, err := agentevents.NewSecretDelivery(first, secret)
	if err != nil {
		t.Fatal(err)
	}
	fixture.deliver(t, old)
	fixture.next(t, controllerevents.SecretInstalled)
	renewed := fixture.request(t, first.ID)
	if !renewed.ExpiresAt.After(first.ExpiresAt) {
		t.Fatal("renewal did not use a fresh lease")
	}
	metadata := secret.Metadata
	metadata.Version++
	metadata.UpdatedAt = time.Now().UTC()
	rotated, err := fixture.cipher.Encrypt(metadata, []byte("version-two"))
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := agentevents.NewSecretDelivery(renewed, rotated)
	if err != nil {
		t.Fatal(err)
	}
	fixture.deliver(t, delivery)
	for {
		ack := fixture.next(t, controllerevents.SecretInstalled)
		if ack.CorrelationID == delivery.ID {
			break
		}
	}
	fixture.deliver(t, old)
	fixture.assertValue(t, "version-two")
	deadline := time.NewTimer(time.Until(secret.Metadata.ExpiresAt.Add(10 * time.Millisecond)))
	defer deadline.Stop()
	select {
	case <-deadline.C:
	case <-fixture.ctx.Done():
		t.Fatal("lease never expired")
	}
	fixture.assertMissing(t)
	if fixture.runtime.Describe().Actions[0].Status != actions.Unavailable {
		t.Fatal("expired installed secret advertised ready")
	}
}

func TestSecretContextAndConfigurationValidation(t *testing.T) {
	if _, err := action.Secret(t.Context(), "TOKEN"); !errors.Is(err, action.ErrSecretUnavailable) {
		t.Fatal("context without provider returned a value")
	}
	if _, err := action.RequestSecret(t.Context(), "TOKEN"); !errors.Is(err, action.ErrSecretUnavailable) {
		t.Fatal("context without a requester accepted a secret request")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := action.RequestSecret(canceled, "TOKEN"); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled secret request lost its cancellation")
	}
	cipher, err := crypto.NewSecretCipher(strings.Repeat("27", 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, config := range []agent.SecretConfig{
		{},
		{Decryptor: cipher, RequestTTL: -time.Second},
		{Decryptor: cipher, RefreshInterval: -time.Second},
		{Decryptor: cipher, RetryInterval: -time.Second},
		{Decryptor: cipher, RequestTTL: time.Second, RefreshInterval: time.Second},
	} {
		if _, err := agent.NewAgent(agent.AgentConfig{Type: "fixture", Secrets: &config, ArtifactFileStore: unusedArtifacts{}, Server: agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"}}); err == nil {
			t.Fatal("invalid secret runtime configuration accepted")
		}
	}
}

func TestMissingSecretInDiscoveryKeepsSynchronizationAndUnrelatedActionsAvailable(t *testing.T) {
	fixture := startSecretAgent(t, 2*time.Second, time.Second, true)
	request := fixture.request(t, "")
	snapshot := fixture.runtime.Describe()
	if snapshot.Actions[0].Status != actions.Unavailable || snapshot.Actions[1].Status != actions.Ready {
		t.Fatal("missing secret blocked unrelated actions or allowed the affected action")
	}
	secret := fixture.secret(t, request, time.Time{}, "fixture-value")
	delivery, err := agentevents.NewSecretDelivery(request, secret)
	if err != nil {
		t.Fatal(err)
	}
	fixture.deliver(t, delivery)
	for {
		fixture.next(t, controllerevents.AgentUpdated)
		if fixture.runtime.Describe().Actions[0].Status == actions.Ready {
			break
		}
	}
	fixture.assertValue(t, "fixture-value")
}

func TestAgentWaitsForSecretRediscoveryBeforeStartingQueuedWork(t *testing.T) {
	for _, expires := range []bool{false, true} {
		t.Run(map[bool]string{false: "waits", true: "rechecks deadline"}[expires], func(t *testing.T) {
			fixture := startSecretAgent(t, 2*time.Second, time.Second)
			request := fixture.request(t, "")
			started, release := make(chan struct{}), make(chan struct{})
			fixture.executor.mu.Lock()
			fixture.executor.discoveryStarted, fixture.executor.discoveryRelease = started, release
			fixture.executor.mu.Unlock()
			secret := fixture.secret(t, request, time.Time{}, "fixture-value")
			delivery, err := agentevents.NewSecretDelivery(request, secret)
			if err != nil {
				t.Fatal(err)
			}
			fixture.deliver(t, delivery)
			select {
			case <-started:
			case <-fixture.ctx.Done():
				t.Fatal("secret rediscovery did not start")
			}
			command, err := agentcommands.NewRunAction(fixture.runtime.ID(), actions.ActionCall{ActionName: "build", Input: []byte(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			if expires {
				command.ExpiresAt = time.Now().Add(30 * time.Millisecond)
			}
			queued := &secretTestDelivery{message: command, acknowledged: make(chan struct{})}
			fixture.bus.commands <- queued
			if expires {
				timer := time.NewTimer(time.Until(command.ExpiresAt.Add(10 * time.Millisecond)))
				select {
				case <-timer.C:
				case <-fixture.ctx.Done():
					t.Fatal("fixture expired")
				}
				timer.Stop()
			}
			close(release)
			kind := controllerevents.ActionSucceeded
			if expires {
				kind = controllerevents.CommandFailed
			}
			result := fixture.next(t, kind)
			if result.CorrelationID != command.ID {
				t.Fatal("queued command lost its identity")
			}
			wantCalls := int32(1)
			if expires {
				wantCalls = 0
			}
			if fixture.executor.calls.Load() != wantCalls {
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
			}
			return output{Matched: bytes.Equal(value, []byte(plaintext))}, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := startSecretAgentActions(t, 10*time.Second, 8*time.Second, executor)
	if snapshot := fixture.runtime.Describe(); snapshot.Actions[0].Status != action.Ready || len(snapshot.Actions[0].RequiredSecrets) != 0 {
		t.Fatal("on-demand secret changed startup readiness or declarations")
	}
	public := fixture.command(t, "inspect", `{"private":false}`)
	fixture.outcome(t, public, controllerevents.ActionSucceeded)
	for _, message := range fixture.bus.history() {
		if message.Type == controllerevents.SecretSyncRequested {
			t.Fatal("startup or public work requested a dynamic secret")
		}
	}
	command := fixture.command(t, "inspect", `{"private":true}`)
	request := fixture.request(t, "")
	if request.Key.Scope.AgentType != "fixture" || request.Key.Name != "TOKEN" || request.AgentID != fixture.runtime.ID() || request.OperationID != "" {
		t.Fatal("dynamic request lost its agent scope")
	}
	if fixture.runtime.Status() != agent.AgentWorking || privateCalls.Load() != 1 {
		t.Fatal("request did not suspend the active action")
	}
	for _, message := range fixture.bus.history() {
		if message.CorrelationID == command && (message.Type == controllerevents.ActionSucceeded || message.Type == controllerevents.CommandFailed) {
			t.Fatal("action finished before receiving the requested secret")
		}
	}
	delivery, err := agentevents.NewSecretDelivery(request, fixture.secret(t, request, time.Time{}, plaintext))
	if err != nil {
		t.Fatal(err)
	}
	fixture.deliver(t, delivery)
	result := fixture.outcome(t, command, controllerevents.ActionSucceeded)
	var accepted actions.ActionOutput
	var decoded output
	if json.Unmarshal(result.Payload, &accepted) != nil || json.Unmarshal(accepted.Output, &decoded) != nil || !decoded.Matched {
		t.Fatal("encrypted delivery did not resume the original action with its value")
	}
	// Installation acknowledgement must survive the last waiter returning.
	acknowledged := false
	for !acknowledged {
		for _, message := range fixture.bus.history() {
			if message.Type == controllerevents.SecretInstalled && message.CorrelationID == delivery.ID {
				if err := controllerevents.ValidateSecretInstalledDelivery(message, delivery); err != nil {
					t.Fatal(err)
				}
				acknowledged = true
			}
		}
		if !acknowledged {
			fixture.next(t, controllerevents.SecretInstalled)
		}
	}
	second := fixture.command(t, "inspect", `{"private":true}`)
	result = fixture.outcome(t, second, controllerevents.ActionSucceeded)
	if json.Unmarshal(result.Payload, &accepted) != nil || json.Unmarshal(accepted.Output, &decoded) != nil || !decoded.Matched {
		t.Fatal("cached secret reuse returned a different value")
	}
	if privateCalls.Load() != 2 {
		t.Fatal("secret delivery repeated an execution or prevented cached reuse")
	}
	for _, message := range fixture.bus.history() {
		if message.Type == controllerevents.SecretSyncRequested {
			payload, err := controllerevents.DecodeSecretSyncRequested(message)
			if err != nil || payload.Request.ID != request.ID {
				t.Fatal("cached secret lookup created another request")
			}
		}
	}
	fixture.stop(t)
	if strings.Contains(fixture.logs.String(), plaintext) {
		t.Fatal("agent logged the requested secret")
	}
	for _, message := range fixture.bus.history() {
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
			fixture := startSecretAgentActions(t, time.Second, 100*time.Millisecond, executor)
			command := fixture.command(t, "wait", `{}`)
			request := fixture.request(t, "")
			if stopHost {
				fixture.stop(t)
			} else {
				(<-cancelRequest)()
				fixture.outcome(t, command, controllerevents.CommandFailed)
				if fixture.runtime.Status() != agent.AgentReady {
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
			if stopHost {
				return
			}
			// Observe beyond the refresh interval: a canceled last waiter must not
			// keep requesting fresh leases for an unused dynamic secret.
			quiet := time.NewTimer(250 * time.Millisecond)
			defer quiet.Stop()
			for {
				select {
				case message := <-fixture.bus.outgoing:
					if message.Type == controllerevents.SecretSyncRequested {
						payload, err := controllerevents.DecodeSecretSyncRequested(message)
						if err != nil || payload.Request.ID != request.ID {
							t.Fatal("canceled last waiter kept refreshing its request")
						}
					}
				case <-quiet.C:
					return
				case <-fixture.ctx.Done():
					t.Fatal("agent stopped after canceling only the request")
				}
			}
		})
	}
}

func (f *secretFixture) command(t *testing.T, name, input string) string {
	t.Helper()
	message, err := agentcommands.NewRunAction(f.runtime.ID(), actions.ActionCall{ActionName: name, Input: json.RawMessage(input)})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case f.bus.commands <- &secretTestDelivery{message: message, acknowledged: make(chan struct{})}:
	case <-f.ctx.Done():
		t.Fatal("agent stopped before accepting command")
	}
	return message.ID
}

func (f *secretFixture) outcome(t *testing.T, commandID string, kind messages.MessageType) messages.Message {
	t.Helper()
	for {
		select {
		case message := <-f.bus.outgoing:
			if message.CorrelationID != commandID || (message.Type != controllerevents.ActionSucceeded && message.Type != controllerevents.CommandFailed) {
				continue
			}
			if message.Type != kind {
				t.Fatalf("unexpected command outcome: %s", message.Type)
			}
			return message
		case <-f.ctx.Done():
			t.Fatal("agent did not finish command")
			return messages.Message{}
		}
	}
}

type secretFixture struct {
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan error
	runtime  *agent.Agent
	executor *contextSecretAction
	cipher   *crypto.SecretCipher
	bus      *secretBus
	stopped  bool
	logs     bytes.Buffer
}

func startSecretAgent(t *testing.T, ttl, refresh time.Duration, requireOnDiscovery ...bool) *secretFixture {
	t.Helper()
	executor := &contextSecretAction{Base: action.NewBase(actions.Action{Name: "build", RequiredSecrets: []string{"TOKEN"}, InputSchema: []byte(`{}`), OutputSchema: []byte(`{}`)})}
	if len(requireOnDiscovery) > 0 {
		executor.requireOnDiscovery = requireOnDiscovery[0]
	}
	unrelated := &secretAction{Base: action.NewBase(actions.Action{Name: "ping", InputSchema: []byte(`{}`), OutputSchema: []byte(`{}`)})}
	fixture := startSecretAgentActions(t, ttl, refresh, executor, unrelated)
	fixture.executor = executor
	return fixture
}

func startSecretAgentActions(t *testing.T, ttl, refresh time.Duration, executors ...action.Executor) *secretFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	cipher, err := crypto.NewSecretCipher(strings.Repeat("27", 32))
	if err != nil {
		t.Fatal(err)
	}
	fixture := &secretFixture{ctx: ctx, cancel: cancel, cipher: cipher,
		bus: &secretBus{outgoing: make(chan messages.Message, 256), incoming: make(chan *secretTestDelivery, 32), commands: make(chan *secretTestDelivery, 8)}, done: make(chan error, 1)}
	noHeartbeat := time.Duration(0)
	runtime, err := agent.NewAgent(agent.AgentConfig{Type: "fixture", Actions: executors, ArtifactFileStore: unusedArtifacts{},
		Server: agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"}, HeartbeatInterval: &noHeartbeat,
		Logger:  slog.New(slog.NewTextHandler(&fixture.logs, nil)),
		Secrets: &agent.SecretConfig{Decryptor: cipher, RequestTTL: ttl, RefreshInterval: refresh, RetryInterval: 20 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	fixture.runtime = runtime
	go func() { fixture.done <- runtime.Serve(ctx, fixture.bus, nil) }()
	t.Cleanup(func() { fixture.stop(t) })
	fixture.next(t, controllerevents.AgentStarted)
	return fixture
}

func (f *secretFixture) stop(t *testing.T) {
	t.Helper()
	if f.stopped {
		return
	}
	f.stopped = true
	f.cancel()
	select {
	case err := <-f.done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("secret agent shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("secret agent did not stop")
	}
}

func (f *secretFixture) next(t *testing.T, kind messages.MessageType) messages.Message {
	t.Helper()
	for {
		select {
		case message := <-f.bus.outgoing:
			if message.Type == kind {
				return message
			}
		case <-f.ctx.Done():
			t.Fatalf("no %s event", kind)
			return messages.Message{}
		}
	}
}

func (f *secretFixture) request(t *testing.T, previous string) secrets.Request {
	t.Helper()
	for {
		message := f.next(t, controllerevents.SecretSyncRequested)
		payload, err := controllerevents.DecodeSecretSyncRequested(message)
		if err != nil {
			t.Fatal(err)
		}
		if payload.Request.ID != previous {
			return payload.Request
		}
	}
}

func (f *secretFixture) secret(t *testing.T, request secrets.Request, expiry time.Time, value string) secrets.Secret {
	t.Helper()
	now := time.Now().UTC()
	secret, err := f.cipher.Encrypt(secrets.Metadata{ID: id.New(), Key: request.Key, Version: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: expiry}, []byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func (f *secretFixture) deliver(t *testing.T, message messages.Message) {
	t.Helper()
	delivery := &secretTestDelivery{message: message, acknowledged: make(chan struct{})}
	select {
	case f.bus.incoming <- delivery:
	case <-f.ctx.Done():
		t.Fatal("agent stopped before delivery")
	}
	select {
	case <-delivery.acknowledged:
	case <-f.ctx.Done():
		t.Fatal("agent did not process delivery")
	}
}

func (f *secretFixture) assertMissing(t *testing.T) {
	t.Helper()
	if value, err := action.Secret(f.executor.context(), "TOKEN"); !errors.Is(err, action.ErrSecretUnavailable) || len(value) != 0 {
		t.Fatal("unavailable secret was readable")
	}
}

func (f *secretFixture) assertValue(t *testing.T, expected string) {
	t.Helper()
	value, err := action.Secret(f.executor.context(), "TOKEN")
	defer clear(value)
	if err != nil || !bytes.Equal(value, []byte(expected)) {
		t.Fatal("installed secret did not match expected version")
	}
}

type contextSecretAction struct {
	*action.Base
	mu                 sync.Mutex
	ctx                context.Context
	discoveryStarted   chan struct{}
	discoveryRelease   chan struct{}
	calls              atomic.Int32
	requireOnDiscovery bool
}

func (a *contextSecretAction) Discover(ctx context.Context, env environment.Environment, scope *environment.DiscoveryScope) (action.Status, error) {
	a.mu.Lock()
	a.ctx = context.WithoutCancel(ctx)
	started, release := a.discoveryStarted, a.discoveryRelease
	a.discoveryStarted, a.discoveryRelease = nil, nil
	a.mu.Unlock()
	if started != nil {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return action.Unknown, ctx.Err()
		}
	}
	if a.requireOnDiscovery {
		value, err := action.Secret(ctx, "TOKEN")
		clear(value)
		if err != nil {
			a.SetStatus(action.Unknown)
			return action.Unknown, fmt.Errorf("discover build: %w", err)
		}
	}
	return a.Base.Discover(ctx, env, scope)
}
func (a *contextSecretAction) context() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ctx
}
func (a *contextSecretAction) Run(ctx context.Context, _ json.RawMessage) (action.Result, error) {
	value, err := action.Secret(ctx, "TOKEN")
	clear(value)
	if err != nil {
		return action.Result{}, err
	}
	a.calls.Add(1)
	return action.JSONResult(struct{}{})
}

type secretBus struct {
	outgoing            chan messages.Message
	incoming            chan *secretTestDelivery
	commands            chan *secretTestDelivery
	failAcknowledgement atomic.Bool
	mu                  sync.Mutex
	published           []messages.Message
}

func (b *secretBus) Publish(ctx context.Context, _ string, message messages.Message) error {
	b.mu.Lock()
	observed := message
	observed.Payload = bytes.Clone(message.Payload)
	b.published = append(b.published, observed)
	b.mu.Unlock()
	select {
	case b.outgoing <- message:
		if message.Type == controllerevents.SecretInstalled && b.failAcknowledgement.CompareAndSwap(true, false) {
			return errors.New("uncertain acknowledgement publication")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *secretBus) history() []messages.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]messages.Message{}, b.published...)
}
func (b *secretBus) Subscribe(_ context.Context, inbox comms.Inbox) (comms.Subscription, error) {
	if strings.HasSuffix(inbox.Topic, ".events") {
		return secretSubscription{b.incoming}, nil
	}
	return secretSubscription{b.commands}, nil
}
func (*secretBus) Close() error { return nil }

type secretSubscription struct{ incoming <-chan *secretTestDelivery }

func (s secretSubscription) Next(ctx context.Context) (comms.Delivery, error) {
	select {
	case delivery := <-s.incoming:
		return delivery, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (secretSubscription) Close() error { return nil }

type secretTestDelivery struct {
	message      messages.Message
	acknowledged chan struct{}
}

func (d *secretTestDelivery) Message() messages.Message { return d.message }
func (d *secretTestDelivery) Ack(context.Context) error { close(d.acknowledged); return nil }
func (*secretTestDelivery) Reject(context.Context) error {
	return errors.New("unexpected secret delivery rejection")
}
