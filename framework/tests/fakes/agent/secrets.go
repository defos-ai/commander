package fakes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/comms"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
)

type ContextSecretAction struct {
	*action.Base
	mu                 sync.Mutex
	ctx                context.Context
	discoveryStarted   chan struct{}
	discoveryRelease   chan struct{}
	calls              atomic.Int32
	requireOnDiscovery bool
}

func (a *ContextSecretAction) Discover(ctx context.Context, env environment.Environment, scope *environment.DiscoveryScope) (action.Status, error) {
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
func (a *ContextSecretAction) Context() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ctx
}
func (a *ContextSecretAction) Run(ctx context.Context, _ json.RawMessage) (action.Result, error) {
	value, err := action.Secret(ctx, "TOKEN")
	clear(value)
	if err != nil {
		return action.Result{}, err
	}
	a.calls.Add(1)
	return action.JSONResult(struct{}{})
}

type SecretBus struct {
	outgoing            chan messages.Message
	incoming            chan *SecretDelivery
	commands            chan *SecretDelivery
	failAcknowledgement atomic.Bool
	mu                  sync.Mutex
	published           []messages.Message
}

func (b *SecretBus) Publish(ctx context.Context, _ string, message messages.Message) error {
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

func (b *SecretBus) History() []messages.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	snapshot := make([]messages.Message, len(b.published))
	for i, message := range b.published {
		message.Payload = bytes.Clone(message.Payload)
		snapshot[i] = message
	}
	return snapshot
}
func (b *SecretBus) Subscribe(_ context.Context, inbox comms.Inbox) (comms.Subscription, error) {
	if strings.HasSuffix(inbox.Topic, ".events") {
		return secretSubscription{b.incoming}, nil
	}
	return secretSubscription{b.commands}, nil
}
func (*SecretBus) Close() error { return nil }

type secretSubscription struct{ incoming <-chan *SecretDelivery }

func (s secretSubscription) Next(ctx context.Context) (comms.Delivery, error) {
	select {
	case delivery := <-s.incoming:
		return delivery, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (secretSubscription) Close() error { return nil }

type SecretDelivery struct {
	message      messages.Message
	acknowledged chan struct{}
}

func (d *SecretDelivery) Message() messages.Message { return d.message }
func (d *SecretDelivery) Ack(context.Context) error { close(d.acknowledged); return nil }
func (*SecretDelivery) Reject(context.Context) error {
	return errors.New("unexpected secret delivery rejection")
}

type SecretAction struct{ *action.Base }

func (*SecretAction) Run(context.Context, json.RawMessage) (action.Result, error) {
	return action.JSONResult(struct{}{})
}

type UnusedArtifacts struct{}

func (UnusedArtifacts) Put(context.Context, string, artifacts.FileSource) (artifacts.ArtifactFile, error) {
	return artifacts.ArtifactFile{}, errors.New("unexpected artifact write")
}
func (UnusedArtifacts) Get(context.Context, string, artifacts.ArtifactFile) ([]byte, error) {
	return nil, errors.New("unexpected artifact read")
}

func NewContextSecretAction(requireOnDiscovery bool) *ContextSecretAction {
	return &ContextSecretAction{Base: action.NewBase(actions.Action{Name: "build", RequiredSecrets: []string{"TOKEN"}, InputSchema: []byte(`{}`), OutputSchema: []byte(`{}`)}), requireOnDiscovery: requireOnDiscovery}
}
func (a *ContextSecretAction) PauseDiscovery(started, release chan struct{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.discoveryStarted, a.discoveryRelease = started, release
}
func (a *ContextSecretAction) Calls() int32 { return a.calls.Load() }
func NewSecretBus() *SecretBus {
	return &SecretBus{outgoing: make(chan messages.Message, 256), incoming: make(chan *SecretDelivery, 32), commands: make(chan *SecretDelivery, 8)}
}
func (b *SecretBus) Outgoing() <-chan messages.Message { return b.outgoing }
func (b *SecretBus) SendCommand(ctx context.Context, delivery *SecretDelivery) error {
	select {
	case b.commands <- delivery:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *SecretBus) SendIncoming(ctx context.Context, delivery *SecretDelivery) error {
	select {
	case b.incoming <- delivery:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *SecretBus) FailNextAcknowledgement() { b.failAcknowledgement.Store(true) }
func NewSecretDelivery(message messages.Message) *SecretDelivery {
	message.Payload = bytes.Clone(message.Payload)
	return &SecretDelivery{message: message, acknowledged: make(chan struct{})}
}
func (d *SecretDelivery) Acknowledged() <-chan struct{} { return d.acknowledged }
