package fakes

import (
	"bytes"
	"context"
	"sort"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

type SecretRecipients struct {
	agents.AgentStore
	records map[string]agents.Agent
}

type SecretDecryptor struct {
	value   []byte
	failure error
	after   func()
}

func NewSecretDecryptor(value []byte, failure error) *SecretDecryptor {
	return &SecretDecryptor{value: bytes.Clone(value), failure: failure}
}

func (decryptor *SecretDecryptor) Decrypt(secret secrets.Secret) ([]byte, error) {
	clear(secret.Value.Nonce)
	clear(secret.Value.Ciphertext)
	if decryptor.after != nil {
		decryptor.after()
	}
	return decryptor.value, decryptor.failure
}

func (decryptor *SecretDecryptor) Cleared() bool {
	return bytes.Count(decryptor.value, []byte{0}) == len(decryptor.value)
}

func (decryptor *SecretDecryptor) AfterDecrypt(callback func()) { decryptor.after = callback }

func NewSecretRecipients(records ...agents.Agent) *SecretRecipients {
	store := &SecretRecipients{records: make(map[string]agents.Agent, len(records))}
	for _, record := range records {
		store.records[record.ID] = record.Clone()
	}
	return store
}

func (store *SecretRecipients) Get(ctx context.Context, agentID string) (agents.Agent, error) {
	if err := ctx.Err(); err != nil {
		return agents.Agent{}, err
	}
	record, ok := store.records[agentID]
	if !ok {
		return agents.Agent{}, agents.ErrUnknownAgent
	}
	return record.Clone(), nil
}

type SecretPublishBus struct {
	comms.Bus
	messages []messages.Message
	topics   []string
	before   func(messages.Message)
	failure  error
}

func (bus *SecretPublishBus) Publish(_ context.Context, topic string, message messages.Message) error {
	if bus.before != nil {
		bus.before(message)
	}
	if bus.failure != nil {
		return bus.failure
	}
	message.Payload = bytes.Clone(message.Payload)
	bus.messages, bus.topics = append(bus.messages, message), append(bus.topics, topic)
	return nil
}

type SecretValues struct {
	secrets.SecretStore
	record atomic.Pointer[secrets.Secret]
}

func (store *SecretValues) Get(ctx context.Context, key secrets.Key) (secrets.Secret, error) {
	if err := ctx.Err(); err != nil {
		return secrets.Secret{}, err
	}
	if record := store.record.Load(); record != nil && record.Metadata.Key == key {
		return record.Clone(), nil
	}
	return secrets.Secret{}, secrets.ErrUnknownSecret
}

type SecretRequests struct {
	records   map[string]secrets.RequestRecord
	saveError error
	listError error
	afterSave func()
}

func (store *SecretRequests) Create(ctx context.Context, request secrets.Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if saved, ok := store.records[request.ID]; ok {
		if !saved.MatchesRequest(request) {
			return secrets.ErrRequestConflict
		}
		return nil
	}
	record, err := secrets.NewRequestRecord(request)
	if err == nil {
		store.records[request.ID] = record
	}
	return err
}

func (store *SecretRequests) Get(ctx context.Context, requestID string) (secrets.RequestRecord, error) {
	if err := ctx.Err(); err != nil {
		return secrets.RequestRecord{}, err
	}
	record, ok := store.records[requestID]
	if !ok {
		return secrets.RequestRecord{}, secrets.ErrUnknownRequest
	}
	return record.Clone(), nil
}

func (store *SecretRequests) ListPending(ctx context.Context) ([]secrets.RequestRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store.listError != nil {
		return nil, store.listError
	}
	var records []secrets.RequestRecord
	for _, record := range store.records {
		if record.Request.Status == secrets.Pending {
			records = append(records, record.Clone())
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Request.ID < records[j].Request.ID })
	return records, nil
}

func (store *SecretRequests) SaveDelivery(ctx context.Context, delivery secrets.Delivery) error {
	err := store.mutate(ctx, delivery.RequestID, func(record secrets.RequestRecord) (secrets.RequestRecord, error) {
		for requestID, saved := range store.records {
			if requestID != delivery.RequestID && saved.Delivery != nil && saved.Delivery.ID == delivery.ID {
				return secrets.RequestRecord{}, secrets.ErrRequestConflict
			}
		}
		return record.WithDelivery(delivery)
	})
	if err != nil {
		return err
	}
	afterSave, failure := store.afterSave, store.saveError
	if afterSave != nil {
		afterSave()
	}
	return failure
}

func (store *SecretRequests) Acknowledge(ctx context.Context, installation secrets.Installation, now time.Time) error {
	return store.mutate(ctx, installation.RequestID, func(record secrets.RequestRecord) (secrets.RequestRecord, error) {
		return record.Acknowledge(installation, now)
	})
}

func (store *SecretRequests) Cancel(ctx context.Context, requestID string, now time.Time) error {
	return store.mutate(ctx, requestID, func(record secrets.RequestRecord) (secrets.RequestRecord, error) {
		return record.Cancel(now)
	})
}

func (store *SecretRequests) Expire(ctx context.Context, requestID string, now time.Time) error {
	return store.mutate(ctx, requestID, func(record secrets.RequestRecord) (secrets.RequestRecord, error) {
		return record.Expire(now)
	})
}

func (store *SecretRequests) mutate(ctx context.Context, requestID string, apply func(secrets.RequestRecord) (secrets.RequestRecord, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	record, ok := store.records[requestID]
	if !ok {
		return secrets.ErrUnknownRequest
	}
	next, err := apply(record.Clone())
	if err == nil {
		store.records[requestID] = next.Clone()
	}
	return err
}

func NewSecretRequests() *SecretRequests {
	return &SecretRequests{records: make(map[string]secrets.RequestRecord)}
}
func (store *SecretRequests) SetSaveError(err error) {
	store.saveError = err
}
func (store *SecretRequests) SetListError(err error) {
	store.listError = err
}
func (store *SecretRequests) AfterSave(callback func()) {
	store.afterSave = callback
}
func (store *SecretValues) Snapshot() *secrets.Secret {
	if record := store.record.Load(); record != nil {
		copy := record.Clone()
		return &copy
	}
	return nil
}
func (store *SecretValues) Set(record *secrets.Secret) {
	if record == nil {
		store.record.Store(nil)
		return
	}
	copy := record.Clone()
	store.record.Store(&copy)
}
func (store *SecretValues) Swap(record *secrets.Secret) *secrets.Secret {
	var next *secrets.Secret
	if record != nil {
		copy := record.Clone()
		next = &copy
	}
	previous := store.record.Swap(next)
	if previous == nil {
		return nil
	}
	copy := previous.Clone()
	return &copy
}
func (bus *SecretPublishBus) BeforePublish(callback func(messages.Message)) { bus.before = callback }
func (bus *SecretPublishBus) FailPublish(err error)                         { bus.failure = err }
func (bus *SecretPublishBus) Messages() []messages.Message {
	snapshot := make([]messages.Message, len(bus.messages))
	for i, message := range bus.messages {
		message.Payload = bytes.Clone(message.Payload)
		snapshot[i] = message
	}
	return snapshot
}
func (bus *SecretPublishBus) Topics() []string { return append([]string{}, bus.topics...) }
func (bus *SecretPublishBus) Reset()           { bus.messages = nil; bus.topics = nil }
