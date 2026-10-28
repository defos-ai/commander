package comms

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/fs/internal/jsonfile"
)

const (
	fsPollInterval  = 10 * time.Millisecond
	fsLeaseLifetime = time.Minute
	fsLeaseRefresh  = 15 * time.Second
	fsCloseTimeout  = time.Second
)

// Subscribers start at the committed tail; reopening never replays commands.
// Remove a message only after every recipient registered at publication ACKs it.
// Rejection or subscription loss retains files without redelivery or recovery.
// Subscriber leases exclude crashed processes from publications within one minute.
// Uncommitted message files remain invisible and are replaced by the next write.
type FsBus struct {
	root   string
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	subs   map[*fsSubscription]struct{}
}

type fsTopic struct {
	name      string
	directory string
	state     *jsonfile.Collection
}

type fsState struct {
	Topic       string               `json:"topic"`
	Sequence    uint64               `json:"sequence"`
	Subscribers map[string]time.Time `json:"subscribers,omitempty"`
	Inboxes     map[string]string    `json:"inboxes,omitempty"`
}

type fsMessage struct {
	Topic    string           `json:"topic"`
	Sequence uint64           `json:"sequence"`
	Message  messages.Message `json:"message"`
	Pending  []string         `json:"pending"`
	Rejected []string         `json:"rejected,omitempty"`
}

type fsSubscription struct {
	bus          *FsBus
	topic        *fsTopic
	ctx          context.Context
	cancel       context.CancelFunc
	gate         chan struct{}
	cursor       uint64 // protected by gate
	id           string
	inbox        string
	closeMu      sync.Mutex
	unregistered bool
}

var _ comms.Bus = (*FsBus)(nil)

func NewFsBus(root string) (*FsBus, error) {
	if root == "" {
		root = filepath.Join("state", "bus")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &FsBus{root: absolute, ctx: ctx, cancel: cancel, subs: make(map[*fsSubscription]struct{})}, nil
}

func (b *FsBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	if err := fsValidateTopic(ctx, topic); err != nil {
		return err
	}
	if err := message.Validate(); err != nil {
		return err
	}
	if !utf8.ValidString(string(message.Type)) || !utf8.ValidString(message.Work.StepID) || !utf8.Valid(message.Payload) {
		return errors.New("message text must contain valid UTF-8")
	}
	// Encoding before filesystem access catches invalid timestamps and payloads
	// without changing a queue. The copied JSON also isolates caller-owned bytes.
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	var snapshot messages.Message
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return err
	}
	operation, finish := fsOperationContext(ctx, b.ctx)
	defer finish()
	queue, err := b.openTopic(operation, topic)
	if err != nil {
		return fsOperationError(ctx, b.ctx, err)
	}
	err = queue.state.Update(operation, func(data []byte) ([]byte, error) {
		state, err := fsDecodeState(data, topic)
		if err != nil {
			return nil, err
		}
		if state.Sequence == math.MaxUint64 {
			return nil, errors.New("filesystem topic sequence is exhausted")
		}
		state.Sequence++
		state.pruneExpired(time.Now())
		pending := make([]string, 0, len(state.Subscribers))
		for subscriber := range state.Subscribers {
			pending = append(pending, subscriber)
		}
		slices.Sort(pending)
		// With no recipients there is no work to retain; new subscribers start
		// after this sequence, so the absent file is never read by a live cursor.
		if len(pending) == 0 {
			return json.Marshal([]fsState{state})
		}
		data, err = json.Marshal([]fsMessage{{Topic: topic, Sequence: state.Sequence, Message: snapshot, Pending: pending}})
		if err != nil {
			return nil, err
		}
		if err := fsWriteMessage(operation, queue.directory, state.Sequence, data); err != nil {
			return nil, err
		}
		return json.Marshal([]fsState{state})
	})
	return fsOperationError(ctx, b.ctx, err)
}

func (b *FsBus) Subscribe(ctx context.Context, inbox comms.Inbox) (comms.Subscription, error) {
	if err := fsValidateTopic(ctx, inbox.Topic); err != nil {
		return nil, err
	}
	if err := inbox.Validate(); err != nil {
		return nil, err
	}
	if inbox.Mode != comms.TransientExclusive {
		return nil, fmt.Errorf("%w: filesystem bus supports only transient-exclusive inboxes", comms.ErrUnsupportedInbox)
	}
	topic := inbox.Topic
	operation, finish := fsOperationContext(ctx, b.ctx)
	defer finish()
	queue, err := b.openTopic(operation, topic)
	if err != nil {
		return nil, fsOperationError(ctx, b.ctx, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.ctx.Err() != nil {
		return nil, comms.ErrClosed
	}
	lifetime, cancel := context.WithCancel(b.ctx)
	s := &fsSubscription{bus: b, topic: queue, ctx: lifetime, cancel: cancel, gate: make(chan struct{}, 1), id: id.New(), inbox: inbox.Name}
	err = queue.state.Update(operation, func(data []byte) ([]byte, error) {
		state, err := fsDecodeState(data, topic)
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		state.pruneExpired(now)
		if _, occupied := state.Inboxes[inbox.Name]; occupied {
			return nil, comms.ErrInboxInUse
		}
		s.cursor = state.Sequence
		if state.Subscribers == nil {
			state.Subscribers = make(map[string]time.Time)
		}
		if state.Inboxes == nil {
			state.Inboxes = make(map[string]string)
		}
		state.Subscribers[s.id] = now.Add(fsLeaseLifetime)
		state.Inboxes[inbox.Name] = s.id
		return json.Marshal([]fsState{state})
	})
	if err != nil {
		cancel()
		// Only an uncertain publication may have registered this generation.
		// Retain it so Close can clean up without removing a successor's claim.
		if errors.Is(err, jsonfile.ErrWriteUncertain) {
			b.subs[s] = struct{}{}
		}
		return nil, fsOperationError(ctx, b.ctx, err)
	}
	b.subs[s] = struct{}{}
	go s.renewLease()
	return s, nil
}

func (b *FsBus) Close() error {
	// Cancel before taking mu so an in-progress Subscribe stops waiting for a
	// topic lock while Close waits for its local registration bookkeeping.
	b.cancel()
	b.mu.Lock()
	subscriptions := make([]*fsSubscription, 0, len(b.subs))
	for s := range b.subs {
		s.cancel()
		subscriptions = append(subscriptions, s)
	}
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), fsCloseTimeout)
	defer cancel()
	var result error
	for _, s := range subscriptions {
		result = errors.Join(result, s.unregister(ctx))
	}
	return result
}

func (s *fsSubscription) Close() error {
	s.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), fsCloseTimeout)
	defer cancel()
	return s.unregister(ctx)
}

func (s *fsSubscription) Next(ctx context.Context) (comms.Delivery, error) {
	if ctx == nil {
		return nil, errors.New("filesystem bus requires a context")
	}
	operation, finish := fsOperationContext(ctx, s.ctx)
	defer finish()
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-operation.Done():
		return nil, fsOperationError(ctx, s.ctx, operation.Err())
	}
	for {
		if err := operation.Err(); err != nil {
			return nil, fsOperationError(ctx, s.ctx, err)
		}
		message, available, err := s.readNext(operation)
		if err != nil {
			return nil, fsOperationError(ctx, s.ctx, err)
		}
		if available {
			if err := operation.Err(); err != nil {
				return nil, fsOperationError(ctx, s.ctx, err)
			}
			s.cursor++
			return &fsDelivery{subscription: s, sequence: s.cursor, message: message, gate: make(chan struct{}, 1)}, nil
		}
		timer := time.NewTimer(fsPollInterval)
		select {
		case <-operation.Done():
			timer.Stop()
			return nil, fsOperationError(ctx, s.ctx, operation.Err())
		case <-timer.C:
		}
	}
}

func (s *fsSubscription) readNext(ctx context.Context) (messages.Message, bool, error) {
	if err := fsRealDirectory(s.bus.root, false); err != nil {
		return messages.Message{}, false, err
	}
	if err := fsRealDirectory(s.topic.directory, false); err != nil {
		return messages.Message{}, false, err
	}
	data, err := jsonfile.Read(ctx, s.topic.state.Path())
	if err != nil {
		return messages.Message{}, false, err
	}
	state, err := fsDecodeState(data, s.topic.name)
	if err != nil {
		return messages.Message{}, false, err
	}
	if !s.ownsInbox(state, time.Now()) {
		s.cancel()
		return messages.Message{}, false, comms.ErrClosed
	}
	if state.Sequence < s.cursor {
		return messages.Message{}, false, errors.New("filesystem topic sequence moved backwards")
	}
	if state.Sequence == s.cursor {
		return messages.Message{}, false, nil
	}
	sequence := s.cursor + 1
	data, err = jsonfile.Read(ctx, fsMessagePath(s.topic.directory, sequence))
	if err != nil {
		return messages.Message{}, false, fmt.Errorf("read committed message %d: %w", sequence, err)
	}
	var records []fsMessage
	if err := fsDecode(data, &records); err != nil {
		return messages.Message{}, false, err
	}
	if len(records) != 1 || records[0].Sequence != sequence || records[0].Topic != s.topic.name {
		return messages.Message{}, false, errors.New("filesystem message has mismatched sequence or topic")
	}
	if err := records[0].Message.Validate(); err != nil {
		return messages.Message{}, false, fmt.Errorf("invalid stored message: %w", err)
	}
	if len(records[0].Pending) == 0 {
		return messages.Message{}, false, errors.New("message has no recipient metadata")
	}
	if !slices.Contains(records[0].Pending, s.id) {
		s.cancel()
		return messages.Message{}, false, comms.ErrClosed
	}
	return records[0].Message, true, nil
}

func (b *FsBus) openTopic(ctx context.Context, topic string) (*fsTopic, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := fsRealDirectory(b.root, true); err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(topic))
	directory := filepath.Join(b.root, hex.EncodeToString(digest[:]))
	if err := fsRealDirectory(directory, true); err != nil {
		return nil, err
	}
	state, err := jsonfile.NewContext(ctx, filepath.Join(directory, "state.json"))
	if err != nil {
		return nil, err
	}
	return &fsTopic{name: topic, directory: directory, state: state}, nil
}

func fsDecodeState(data []byte, topic string) (fsState, error) {
	var records []fsState
	if err := fsDecode(data, &records); err != nil {
		return fsState{}, err
	}
	if len(records) == 0 {
		return fsState{Topic: topic}, nil
	}
	if len(records) != 1 || records[0].Topic != topic {
		return fsState{}, errors.New("filesystem queue has invalid topic state")
	}
	for subscriber, expiry := range records[0].Subscribers {
		if !id.Valid(subscriber) || expiry.IsZero() {
			return fsState{}, errors.New("filesystem queue has invalid subscriber lease")
		}
	}
	for name, subscriber := range records[0].Inboxes {
		inbox := comms.Inbox{Topic: topic, Name: name, Mode: comms.TransientExclusive}
		if inbox.Validate() != nil || !id.Valid(subscriber) {
			return fsState{}, errors.New("filesystem queue has invalid inbox ownership")
		}
	}
	return records[0], nil
}

func (state *fsState) pruneExpired(now time.Time) {
	for subscriber, expiry := range state.Subscribers {
		if !expiry.After(now) {
			delete(state.Subscribers, subscriber)
		}
	}
	for name, subscriber := range state.Inboxes {
		if _, live := state.Subscribers[subscriber]; !live {
			delete(state.Inboxes, name)
		}
	}
}

func (s *fsSubscription) ownsInbox(state fsState, now time.Time) bool {
	return state.Inboxes[s.inbox] == s.id && state.Subscribers[s.id].After(now)
}

func fsDecode(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode filesystem bus record: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("filesystem bus record must contain one JSON value")
	}
	return nil
}

func fsWriteMessage(ctx context.Context, directory string, sequence uint64, data []byte) error {
	if err := fsReplaceMessage(ctx, directory, sequence, data); err != nil {
		return err
	}
	return fsSyncDirectory(directory)
}

// fsReplaceMessage returns after publication so delivery settlement can remember
// the committed outcome even if the following directory sync fails.
func fsReplaceMessage(ctx context.Context, directory string, sequence uint64, data []byte) error {
	file, err := os.CreateTemp(directory, ".message-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := min(len(data), 64*1024)
		written, err := file.Write(data[:chunk])
		if err != nil {
			return err
		}
		data = data[written:]
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	// Like JSON collections, permit replacement while a Windows subscriber
	// still holds a read handle to the previous recipient metadata.
	return root.Rename(filepath.Base(file.Name()), filepath.Base(fsMessagePath(directory, sequence)))
}

func fsSyncDirectory(directory string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	folder, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer folder.Close()
	return folder.Sync()
}

func fsMessagePath(directory string, sequence uint64) string {
	return filepath.Join(directory, fmt.Sprintf("%020d.json", sequence))
}

func fsRealDirectory(path string, create bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("filesystem bus paths must be real directories")
	}
	return nil
}

func fsValidateTopic(ctx context.Context, topic string) error {
	if ctx == nil {
		return errors.New("filesystem bus requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if topic == "" || topic != strings.TrimSpace(topic) || !utf8.ValidString(topic) {
		return errors.New("topic must be nonblank UTF-8 text without surrounding whitespace")
	}
	return nil
}

func fsOperationContext(ctx, lifetime context.Context) (context.Context, func()) {
	operation, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lifetime, cancel)
	if lifetime.Err() != nil {
		cancel()
	}
	return operation, func() { stop(); cancel() }
}

func fsOperationError(ctx, lifetime context.Context, err error) error {
	if err == nil { // A committed write remains successful after cancellation.
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if lifetime.Err() != nil {
		return comms.ErrClosed
	}
	return err
}
