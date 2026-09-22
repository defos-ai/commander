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
	"commander/providers/internal/jsonfile"
)

func (s *fsSubscription) Ack(ctx context.Context, messageID string) error {
	if ctx == nil {
		return errors.New("message acknowledgement requires a context")
	}
	operation, finish := fsOperationContext(ctx, s.ctx)
	defer finish()
	select {
	case s.ackGate <- struct{}{}:
		defer func() { <-s.ackGate }()
	case <-operation.Done():
		return fsOperationError(ctx, s.ctx, operation.Err())
	}
	if err := operation.Err(); err != nil {
		return fsOperationError(ctx, s.ctx, err)
	}
	s.ackMu.Lock()
	sequences, delivered := s.delivered[messageID]
	sequences = slices.Clone(sequences)
	acknowledged := s.acknowledged.Contains(messageID)
	s.ackMu.Unlock()
	if !delivered {
		if acknowledged {
			return nil
		}
		return comms.ErrNotDelivered
	}
	for _, sequence := range sequences {
		// Use the topic lock, shared with registration and publication, rather
		// than a lock on the message file that the final ACK will remove.
		err := s.topic.state.View(operation, func([]byte) error {
			path := fsMessagePath(s.topic.directory, sequence)
			data, err := jsonfile.Read(operation, path)
			if err != nil {
				return err
			}
			var records []fsMessage
			if err := fsDecode(data, &records); err != nil {
				return err
			}
			if len(records) != 1 || records[0].Sequence != sequence || records[0].Topic != s.topic.name || records[0].Message.ID != messageID {
				return errors.New("acknowledgement does not match stored message")
			}
			record := records[0]
			removed := false
			if len(record.Pending) == 0 {
				return errors.New("message has no recipient metadata")
			}
			record.Pending = slices.DeleteFunc(record.Pending, func(recipient string) bool { return recipient == s.id })
			if len(record.Pending) == 0 {
				if err := operation.Err(); err != nil {
					return err
				}
				if err := os.Remove(path); err != nil {
					return fmt.Errorf("remove acknowledged message: %w", err)
				}
				removed = true
			} else {
				data, err := json.Marshal([]fsMessage{record})
				if err != nil {
					return err
				}
				if err := fsWriteMessage(operation, s.topic.directory, sequence, data); err != nil {
					return err
				}
			}
			// Remember each committed ACK before View checks cancellation. A retry
			// then succeeds even if the final recipient already deleted the file.
			s.ackMu.Lock()
			remaining := slices.DeleteFunc(s.delivered[messageID], func(value uint64) bool { return value == sequence })
			if len(remaining) == 0 {
				delete(s.delivered, messageID)
				s.acknowledged.Remember(messageID)
			} else {
				s.delivered[messageID] = remaining
			}
			s.ackMu.Unlock()
			if removed {
				return fsSyncDirectory(s.topic.directory)
			}
			return nil
		})
		if err != nil {
			return fsOperationError(ctx, s.ctx, err)
		}
	}
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
