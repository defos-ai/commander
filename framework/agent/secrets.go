package agent

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

// SecretDecryptor authenticates encrypted records. Hosts inject their configured
// cipher; agents never receive storage access or generate encryption keys.
type SecretDecryptor interface {
	Decrypt(secretmodel.Secret) ([]byte, error)
}

// SecretConfig enables declared and on-demand secrets for this agent type.
// Zero durations use a one-minute lease, thirty-second refresh and five-second retry.
type SecretConfig struct {
	Decryptor       SecretDecryptor
	RequestTTL      time.Duration
	RefreshInterval time.Duration
	RetryInterval   time.Duration
}

type secretRuntime struct {
	mu       sync.Mutex
	config   SecretConfig
	agentID  string
	typeName string
	entries  map[string]*secretEntry
	dirty    uint64
	updated  uint64
	wake     chan struct{}
	changed  chan struct{}
	service  context.Context
}

type secretEntry struct {
	declared  bool
	waiters   int
	request   secretmodel.Request
	message   messages.Message
	delivery  messages.Message
	ack       messages.Message
	value     []byte
	metadata  secretmodel.Metadata
	installed bool
	lease     time.Time
}

func newSecretRuntime(config *SecretConfig, agentID, typeName string, actions []action.Executor) (*secretRuntime, error) {
	if config == nil {
		return nil, nil
	}
	settings := *config
	if settings.Decryptor == nil {
		return nil, errors.New("secret synchronization requires a decryptor")
	}
	if settings.RequestTTL == 0 {
		settings.RequestTTL = time.Minute
	}
	if settings.RefreshInterval == 0 {
		settings.RefreshInterval = 30 * time.Second
	}
	if settings.RetryInterval == 0 {
		settings.RetryInterval = 5 * time.Second
	}
	if settings.RequestTTL <= 0 || settings.RefreshInterval <= 0 || settings.RetryInterval <= 0 || settings.RefreshInterval >= settings.RequestTTL {
		return nil, errors.New("secret synchronization intervals must be positive and refresh must precede request expiry")
	}
	runtime := &secretRuntime{config: settings, agentID: agentID, typeName: typeName,
		entries: make(map[string]*secretEntry), wake: make(chan struct{}, 1), changed: make(chan struct{})}
	for _, executor := range actions {
		for _, name := range executor.Action().RequiredSecrets {
			runtime.entries[name] = &secretEntry{declared: true}
		}
	}
	return runtime, nil
}

// Formatting runtime internals must not traverse retained plaintext values.
func (*secretRuntime) String() string   { return "agent secrets (values redacted)" }
func (*secretRuntime) GoString() string { return "agent secrets (values redacted)" }

func (r *secretRuntime) clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, entry := range r.entries {
		clear(entry.value)
		if entry.declared {
			r.entries[name] = &secretEntry{declared: true}
		} else {
			delete(r.entries, name)
		}
	}
	r.dirty, r.updated = 0, 0
	r.service = nil
	r.broadcast()
}

func (r *secretRuntime) start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.service = ctx
}

func (r *secretRuntime) request(ctx context.Context, waits *workSecretWaits, name string) ([]byte, error) {
	if r == nil || waits == nil {
		return nil, action.ErrSecretUnavailable
	}
	execution := waits.context
	if err := secretmodel.ValidateName(name); err != nil {
		return nil, action.ErrSecretUnavailable
	}
	r.mu.Lock()
	service := r.service
	if service == nil || service.Err() != nil || execution.Err() != nil || ctx.Err() != nil {
		r.mu.Unlock()
		if err := requestCanceled(ctx, execution, service); err != nil {
			return nil, err
		}
		return nil, action.ErrSecretUnavailable
	}
	entry := r.entries[name]
	if entry == nil {
		entry = &secretEntry{}
		r.entries[name] = entry
	}
	if entry.installed && time.Now().Before(entry.lease) {
		value := bytes.Clone(entry.value)
		r.mu.Unlock()
		if err := requestCanceled(ctx, execution, service); err != nil {
			clear(value)
			return nil, err
		}
		return value, nil
	}
	entry.waiters++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		entry.waiters--
		r.mu.Unlock()
	}()
	// Publishing a snapshot reads the secret cache; release its mutex first.
	done, err := waits.begin(ctx, name)
	if err != nil {
		return nil, err
	}
	defer done()
	r.notify()
	for {
		r.mu.Lock()
		if err := requestCanceled(ctx, execution, service); err != nil {
			r.mu.Unlock()
			return nil, err
		}
		if entry.installed && time.Now().Before(entry.lease) {
			value := bytes.Clone(entry.value)
			r.mu.Unlock()
			if err := requestCanceled(ctx, execution, service); err != nil {
				clear(value)
				return nil, err
			}
			return value, nil
		}
		changed := r.changed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-execution.Done():
		case <-service.Done():
		case <-changed:
		}
	}
}

func requestCanceled(contexts ...context.Context) error {
	for _, ctx := range contexts {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

// broadcast wakes every waiter without stealing the synchronization loop's wake.
// The caller holds mu so checking a value and subscribing cannot miss a delivery.
func (r *secretRuntime) broadcast() {
	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *secretRuntime) value(name string) ([]byte, error) {
	if r == nil {
		return nil, action.ErrSecretUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[name]
	if entry == nil || !entry.installed || !time.Now().Before(entry.lease) {
		return nil, action.ErrSecretUnavailable
	}
	return bytes.Clone(entry.value), nil
}

func (r *secretRuntime) available(name string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[name]
	return entry != nil && entry.installed && time.Now().Before(entry.lease)
}

func (a *Agent) missingSecrets(definition actionmodel.Action) bool {
	for _, name := range definition.RequiredSecrets {
		if !a.secrets.available(name) {
			return true
		}
	}
	return false
}

type scopedSecrets struct {
	runtime   *secretRuntime
	names     []string
	execution *workSecretWaits
}

func (provider scopedSecrets) Secret(name string) ([]byte, error) {
	if !slices.Contains(provider.names, name) {
		return nil, action.ErrSecretUnavailable
	}
	return provider.runtime.value(name)
}

func (provider scopedSecrets) RequestSecret(ctx context.Context, name string) ([]byte, error) {
	return provider.runtime.request(ctx, provider.execution, name)
}

func (a *Agent) secretContext(ctx context.Context, definition actionmodel.Action, waits *workSecretWaits) context.Context {
	provider := scopedSecrets{runtime: a.secrets, names: slices.Clone(definition.RequiredSecrets), execution: waits}
	return action.WithSecrets(ctx, provider)
}

func (a *Agent) describeAction(definition actionmodel.Action) actionmodel.Action {
	definition = definition.Clone()
	for _, name := range definition.RequiredSecrets {
		if a.secrets.available(name) {
			continue
		}
		definition.Status = action.Unavailable
		if definition.StatusReason == "" {
			definition.StatusReason = "Required secrets are unavailable"
		}
		requirement := actionmodel.Requirement{Kind: actionmodel.SecretRequirement, Name: name}
		if !slices.Contains(definition.MissingRequirements, requirement) {
			definition.MissingRequirements = append(definition.MissingRequirements, requirement)
		}
	}
	return definition
}

// pending returns immutable retries. A fresh request periodically checks rotation;
// an earlier installed value remains usable only until its original lease ends.
func (r *secretRuntime) pending(now time.Time) ([]messages.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var outgoing []messages.Message
	for name, entry := range r.entries {
		if entry.installed && !now.Before(entry.lease) {
			clear(entry.value)
			entry.value, entry.installed = nil, false
			if entry.declared {
				r.dirty++
			}
		}
		if !entry.declared && (entry.waiters == 0 || entry.installed) {
			// Delivery acknowledgements outlive the caller that received a value.
			// Keep the installed request while its waiters wake; refreshing it here
			// could discard the acknowledgement before its first publication.
			if entry.ack.ID != "" && now.Before(entry.request.ExpiresAt) {
				outgoing = append(outgoing, entry.ack)
			}
			if !now.Before(entry.request.ExpiresAt) && !now.Before(entry.lease) {
				clear(entry.value)
				delete(r.entries, name)
			}
			continue
		}
		if entry.request.ID == "" || !now.Before(entry.request.ExpiresAt) || !now.Before(entry.request.RequestedAt.Add(r.config.RefreshInterval)) {
			request := secretmodel.Request{ID: id.New(), AgentID: r.agentID, AgentType: r.typeName, Key: secretmodel.Key{Name: name}, Status: secretmodel.Pending, RequestedAt: now, ExpiresAt: now.Add(r.config.RequestTTL)}
			message, err := controllerevents.NewSecretSyncRequested(request)
			if err != nil {
				return nil, err
			}
			entry.request, entry.message, entry.delivery, entry.ack = request, message, messages.Message{}, messages.Message{}
		}
		outgoing = append(outgoing, entry.message)
		if entry.ack.ID != "" {
			outgoing = append(outgoing, entry.ack)
		}
	}
	return outgoing, nil
}

func (r *secretRuntime) install(message messages.Message, now time.Time) error {
	payload, err := agentevents.DecodeSecretDelivery(message)
	if err != nil {
		return errors.New("invalid secret delivery")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[payload.Secret.Metadata.Key.Name]
	if entry == nil || message.AgentID != r.agentID ||
		message.CorrelationID != entry.request.ID || now.Before(message.CreatedAt) || !now.Before(message.ExpiresAt) {
		return errors.New("secret delivery does not match a current valid request")
	}
	if err := agentevents.ValidateSecretDeliveryRequest(message, entry.request); err != nil {
		return errors.New("secret delivery does not match its request")
	}
	if entry.delivery.ID != "" {
		if entry.delivery.ID != message.ID || !bytes.Equal(entry.delivery.Payload, message.Payload) || !entry.delivery.CreatedAt.Equal(message.CreatedAt) || !entry.delivery.ExpiresAt.Equal(message.ExpiresAt) {
			return errors.New("conflicting secret delivery")
		}
		return nil
	}
	if entry.metadata.ID == payload.Secret.Metadata.ID && payload.Secret.Metadata.Version < entry.metadata.Version {
		return errors.New("secret delivery would restore an older version")
	}
	value, err := r.config.Decryptor.Decrypt(payload.Secret)
	defer clear(value)
	if err != nil {
		return errors.New("secret delivery authentication failed")
	}
	ack, err := controllerevents.NewSecretInstalled(message)
	if err != nil {
		return errors.New("secret delivery expired before installation")
	}
	changed := !entry.installed || entry.metadata.ID != payload.Secret.Metadata.ID || entry.metadata.Version != payload.Secret.Metadata.Version || !bytes.Equal(entry.value, value)
	clear(entry.value)
	entry.value, entry.metadata, entry.installed, entry.lease = bytes.Clone(value), payload.Secret.Metadata, true, message.ExpiresAt
	message.Payload = bytes.Clone(message.Payload)
	entry.delivery, entry.ack = message, ack
	if changed && entry.declared {
		r.dirty++
	}
	r.broadcast()
	r.notify()
	return nil
}

func (r *secretRuntime) notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (a *Agent) synchronizeSecrets(ctx context.Context, events controllerevents.Client) {
	ticker := time.NewTicker(a.secrets.config.RetryInterval)
	defer ticker.Stop()
	for {
		outgoing, err := a.secrets.pending(time.Now().UTC())
		if err != nil {
			a.config.Logger.Warn("secret synchronization could not prepare requests")
		}
		for _, message := range outgoing {
			publication, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := events.Publish(publication, message)
			cancel()
			if err != nil && ctx.Err() == nil {
				a.config.Logger.Warn("secret synchronization publication failed")
			}
		}
		a.refreshSecretActions(ctx, events)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-a.secrets.wake:
		}
	}
}

func (a *Agent) refreshSecretActions(ctx context.Context, events controllerevents.Client) {
	a.secrets.mu.Lock()
	version, updated := a.secrets.dirty, a.secrets.updated
	a.secrets.mu.Unlock()
	if version == updated || (a.Status() != AgentReady && a.Status() != AgentUnavailable) {
		return
	}
	if err := a.claimAction(ctx, false); err != nil {
		return
	}
	defer func() { <-a.actionGate }()
	if err := a.discoverActions(ctx, a.state.Load().scope, true); err != nil {
		return
	}
	if err := a.publishState(ctx, events, controllerevents.AgentUpdated, ""); err != nil {
		return
	}
	a.secrets.mu.Lock()
	a.secrets.updated = version
	a.secrets.mu.Unlock()
}
