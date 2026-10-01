package comms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"commander/framework/comms"
	"commander/framework/comms/messages"
	"commander/providers/fs/internal/jsonfile"
)

type fsSettlement uint8

const (
	fsUnsettled fsSettlement = iota
	fsAcknowledged
	fsRejected
)

type fsDelivery struct {
	subscription *fsSubscription
	sequence     uint64
	message      messages.Message
	gate         chan struct{}
	settlement   fsSettlement // protected by gate
	durable      bool         // protected by gate
}

var _ comms.Delivery = (*fsDelivery)(nil)

func (d *fsDelivery) Message() messages.Message {
	message := d.message
	message.Payload = slices.Clone(message.Payload)
	return message
}

func (d *fsDelivery) Ack(ctx context.Context) error { return d.settle(ctx, fsAcknowledged) }

func (d *fsDelivery) Reject(ctx context.Context) error { return d.settle(ctx, fsRejected) }

func (d *fsDelivery) settle(ctx context.Context, outcome fsSettlement) error {
	if ctx == nil {
		return errors.New("delivery settlement requires a context")
	}
	select {
	case d.gate <- struct{}{}:
		defer func() { <-d.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.settlement != fsUnsettled {
		if d.settlement != outcome {
			return comms.ErrDeliverySettled
		}
		if d.durable {
			return nil
		}
		// A failed directory sync does not undo publication. Retry only the
		// durability check, even if the subscription has since closed.
		return d.syncSettlement()
	}

	s := d.subscription
	operation, finish := fsOperationContext(ctx, s.ctx)
	defer finish()
	// Use the topic lock, shared with registration and publication, rather
	// than a lock on the message file that the last successful ACK removes.
	err := s.topic.state.View(operation, func([]byte) error {
		path := fsMessagePath(s.topic.directory, d.sequence)
		data, err := jsonfile.Read(operation, path)
		if err != nil {
			return err
		}
		var records []fsMessage
		if err := fsDecode(data, &records); err != nil {
			return err
		}
		if len(records) != 1 || records[0].Sequence != d.sequence || records[0].Topic != s.topic.name {
			return errors.New("delivery does not match stored message")
		}
		record := records[0]
		if !slices.Contains(record.Pending, s.id) {
			return errors.New("delivery has no pending recipient")
		}
		record.Pending = slices.DeleteFunc(record.Pending, func(recipient string) bool { return recipient == s.id })
		if outcome == fsRejected {
			record.Rejected = append(record.Rejected, s.id)
			slices.Sort(record.Rejected)
		}
		if len(record.Pending) == 0 && len(record.Rejected) == 0 {
			if err := operation.Err(); err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove acknowledged message: %w", err)
			}
		} else {
			data, err := json.Marshal([]fsMessage{record})
			if err != nil {
				return err
			}
			if err := fsReplaceMessage(operation, s.topic.directory, d.sequence, data); err != nil {
				return err
			}
		}
		// Remember the outcome immediately after rename/removal. A cancellation
		// or sync failure must never allow an opposite settlement afterward.
		d.settlement = outcome
		return d.syncSettlement()
	})
	if d.durable {
		// View checks cancellation after its callback; a committed, synced
		// settlement remains successful if cancellation races that final check.
		return nil
	}
	return fsOperationError(ctx, s.ctx, err)
}

func (d *fsDelivery) syncSettlement() error {
	if err := fsSyncDirectory(d.subscription.topic.directory); err != nil {
		return err
	}
	d.durable = true
	return nil
}

func (s *fsSubscription) unregister(ctx context.Context) error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.unregistered {
		return nil
	}
	err := s.topic.state.Update(ctx, func(data []byte) ([]byte, error) {
		state, err := fsDecodeState(data, s.topic.name)
		if err != nil {
			return nil, err
		}
		delete(state.Subscribers, s.id)
		return json.Marshal([]fsState{state})
	})
	if err != nil {
		return fmt.Errorf("unregister filesystem subscription: %w", err)
	}
	s.unregistered = true
	s.bus.mu.Lock()
	delete(s.bus.subs, s)
	s.bus.mu.Unlock()
	return nil
}

func (s *fsSubscription) renewLease() {
	ticker := time.NewTicker(fsLeaseRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(s.ctx, fsLeaseRefresh)
		err := s.topic.state.Update(ctx, func(data []byte) ([]byte, error) {
			state, err := fsDecodeState(data, s.topic.name)
			if err != nil {
				return nil, err
			}
			now := time.Now().UTC()
			if !state.Subscribers[s.id].After(now) {
				// Expiry can leave gaps in the recipient snapshot. Never rejoin a
				// cursor that may have missed publications while its lease lapsed.
				return nil, comms.ErrClosed
			}
			state.Subscribers[s.id] = now.Add(fsLeaseLifetime)
			return json.Marshal([]fsState{state})
		})
		cancel()
		if err != nil {
			s.cancel()
			return
		}
	}
}
