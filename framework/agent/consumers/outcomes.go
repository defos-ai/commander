package consumers

import (
	"bytes"
	"context"
	"errors"
	"sync"

	agentevents "commander/framework/comms/clients/agent/events"
	"commander/framework/comms/messages"
	"commander/framework/domain"
)

// Outcomes belongs to one agent runtime. It prevents repeated action work
// within that runtime and keeps results available until the controller confirms
// durable receipt. A new process has a new agent ID; it cannot claim continuity
// with an interrupted process using this transient state.
type Outcomes struct {
	mu      sync.Mutex
	records map[string]*commandOutcome
	changed chan struct{}
}

type commandOutcome struct {
	command           messages.Message
	result            *messages.Message
	accepted          bool
	commandAckAllowed bool
}

func (o *Outcomes) claim(command messages.Message) (result *messages.Message, claimed bool, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if record, exists := o.records[command.ID]; exists {
		previous := record.command
		if previous.Type != command.Type || previous.AgentID != command.AgentID || previous.Work != command.Work ||
			previous.CorrelationID != command.CorrelationID || !previous.CreatedAt.Equal(command.CreatedAt) || !bytes.Equal(previous.Payload, command.Payload) {
			return nil, false, errors.New("command ID already received with different content")
		}
		if record.result == nil {
			return nil, false, errors.New("previous command execution has no known outcome")
		}
		// This delivery needs its own acknowledgement. If publication of the
		// cached result fails, keep retrying even if an earlier delivery was
		// accepted. Its handler waits for a matching receipt when publication fails.
		record.accepted = false
		copy := copyMessage(*record.result)
		return &copy, false, nil
	}
	if o.records == nil {
		o.records = make(map[string]*commandOutcome)
	}
	o.records[command.ID] = &commandOutcome{command: copyMessage(command)}
	return nil, true, nil
}

func (o *Outcomes) complete(result messages.Message, acknowledgeCommand bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	copy := copyMessage(result)
	o.records[result.CorrelationID].result = &copy
	o.records[result.CorrelationID].commandAckAllowed = acknowledgeCommand
}

// canAcknowledge preserves retention policy across duplicate deliveries, even
// after a result receipt has arrived.
func (o *Outcomes) canAcknowledge(commandID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	record := o.records[commandID]
	return record != nil && record.commandAckAllowed
}

func (o *Outcomes) validateReceipt(message messages.Message) error {
	var receipt agentevents.ResultReceipt
	if err := domain.Decode(message.Payload, &receipt); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	record, exists := o.records[message.CorrelationID]
	if !exists || record.result == nil || record.result.ID != receipt.ResultID ||
		record.result.AgentID != message.AgentID || record.result.Work != message.Work {
		return errors.New("result receipt does not match a completed command")
	}
	return nil
}

func (o *Outcomes) acceptReceipt(commandID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.records[commandID].accepted = true
	if o.changed != nil {
		close(o.changed)
		o.changed = nil
	}
}

func (o *Outcomes) drain(ctx context.Context) error {
	return o.wait(ctx, func() bool {
		for _, record := range o.records {
			if record.result != nil && !record.accepted {
				return false
			}
		}
		return true
	})
}

// waitAccepted keeps completion with the original command handler while the
// independent publisher retries its saved result and receipts confirm delivery.
func (o *Outcomes) waitAccepted(ctx context.Context, commandID string) error {
	return o.wait(ctx, func() bool {
		record := o.records[commandID]
		return record != nil && record.accepted
	})
}

// wait evaluates ready while holding mu so a receipt cannot arrive between
// checking acceptance and subscribing to its notification.
func (o *Outcomes) wait(ctx context.Context, ready func() bool) error {
	for {
		o.mu.Lock()
		if ready() {
			o.mu.Unlock()
			return nil
		}
		if o.changed == nil {
			o.changed = make(chan struct{})
		}
		changed := o.changed
		o.mu.Unlock()
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-changed:
		}
	}
}

func (o *Outcomes) pending() []messages.Message {
	o.mu.Lock()
	defer o.mu.Unlock()
	var pending []messages.Message
	for _, record := range o.records {
		if record.result != nil && !record.accepted {
			pending = append(pending, copyMessage(*record.result))
		}
	}
	return pending
}

func copyMessage(message messages.Message) messages.Message {
	message.Payload = bytes.Clone(message.Payload)
	return message
}
