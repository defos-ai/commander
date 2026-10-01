package consumers

import (
	"bytes"
	"context"
	"errors"

	"commander/framework/comms"
	agentevents "commander/framework/comms/clients/agent/events"
	"commander/framework/comms/messages"
	"commander/framework/domain"
)

// One loop owns command outcomes. Execution, publication, and receipt waits run
// outside it. Records survive Serve restarts on the same agent instance.
type Outcomes struct {
	requests chan func()
	done     chan struct{}
	records  map[string]*commandOutcome
	changed  chan struct{}
}

type commandOutcome struct {
	command           messages.Message
	result            *messages.Message
	accepted          bool
	commandAckAllowed bool
}

// Start begins a service lifetime. The previous loop and its consumers must
// have stopped before starting another lifetime on this instance.
func (o *Outcomes) Start(ctx context.Context) <-chan struct{} {
	o.requests = make(chan func())
	o.done = make(chan struct{})
	go func() {
		defer close(o.done)
		if o.records == nil {
			o.records = make(map[string]*commandOutcome)
		}
		for ctx.Err() == nil {
			select {
			case <-ctx.Done():
			case request := <-o.requests:
				request()
			}
		}
	}()
	return o.done
}

func outcomeRequest[T any](ctx context.Context, o *Outcomes, execute func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if o.requests == nil {
		return zero, errors.New("outcome consumer is not running")
	}
	type response struct {
		value T
		err   error
	}
	reply := make(chan response, 1)
	request := func() {
		if err := ctx.Err(); err != nil {
			reply <- response{err: err}
			return
		}
		value, err := execute()
		reply <- response{value, err}
	}
	select {
	case o.requests <- request:
	case <-ctx.Done():
		return zero, context.Cause(ctx)
	case <-o.done:
		return zero, outcomesStopped(ctx)
	}
	select {
	case result := <-reply:
		return result.value, result.err
	case <-ctx.Done():
		return zero, context.Cause(ctx)
	case <-o.done:
		return zero, outcomesStopped(ctx)
	}
}

func (o *Outcomes) claim(ctx context.Context, command messages.Message) (*messages.Message, bool, error) {
	command = copyMessage(command)
	type claim struct {
		result  *messages.Message
		claimed bool
	}
	result, err := outcomeRequest(ctx, o, func() (claim, error) {
		if record, exists := o.records[command.ID]; exists {
			previous := record.command
			if previous.Type != command.Type || previous.AgentID != command.AgentID || previous.Work != command.Work ||
				previous.CorrelationID != command.CorrelationID || !previous.CreatedAt.Equal(command.CreatedAt) || !previous.ExpiresAt.Equal(command.ExpiresAt) || !bytes.Equal(previous.Payload, command.Payload) {
				return claim{}, errors.New("command ID already received with different content")
			}
			if record.result == nil {
				return claim{}, errors.New("previous command execution has no known outcome")
			}
			// A replay still needs delivery confirmation if publication fails.
			record.accepted = false
			copy := copyMessage(*record.result)
			return claim{result: &copy}, nil
		}
		o.records[command.ID] = &commandOutcome{command: command}
		return claim{claimed: true}, nil
	})
	return result.result, result.claimed, err
}

func (o *Outcomes) complete(ctx context.Context, result messages.Message, acknowledgeCommand bool) error {
	result = copyMessage(result)
	_, err := outcomeRequest(ctx, o, func() (struct{}, error) {
		record := o.records[result.CorrelationID]
		if record == nil {
			return struct{}{}, errors.New("result has no claimed command")
		}
		record.result = &result
		record.commandAckAllowed = acknowledgeCommand
		return struct{}{}, nil
	})
	return err
}

func (o *Outcomes) canAcknowledge(ctx context.Context, commandID string) (bool, error) {
	return outcomeRequest(ctx, o, func() (bool, error) {
		record := o.records[commandID]
		return record != nil && record.commandAckAllowed, nil
	})
}

func (o *Outcomes) validateReceipt(ctx context.Context, message messages.Message) error {
	var receipt agentevents.ResultReceipt
	if err := domain.Decode(message.Payload, &receipt); err != nil {
		return err
	}
	_, err := outcomeRequest(ctx, o, func() (struct{}, error) {
		record, exists := o.records[message.CorrelationID]
		if !exists || record.result == nil || record.result.ID != receipt.ResultID ||
			record.result.AgentID != message.AgentID || record.result.Work != message.Work {
			return struct{}{}, errors.New("result receipt does not match a completed command")
		}
		return struct{}{}, nil
	})
	return err
}

func (o *Outcomes) acceptReceipt(ctx context.Context, commandID string) error {
	_, err := outcomeRequest(ctx, o, func() (struct{}, error) {
		record := o.records[commandID]
		if record == nil {
			return struct{}{}, errors.New("receipt has no claimed command")
		}
		record.accepted = true
		if o.changed != nil {
			close(o.changed)
			o.changed = nil
		}
		return struct{}{}, nil
	})
	return err
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

func (o *Outcomes) waitAccepted(ctx context.Context, commandID string) error {
	return o.wait(ctx, func() bool {
		record := o.records[commandID]
		return record != nil && record.accepted
	})
}

func (o *Outcomes) wait(ctx context.Context, ready func() bool) error {
	for {
		// Check and subscribe in one loop turn so an acceptance cannot be missed.
		changed, err := outcomeRequest(ctx, o, func() (<-chan struct{}, error) {
			if ready() {
				return nil, nil
			}
			if o.changed == nil {
				o.changed = make(chan struct{})
			}
			return o.changed, nil
		})
		if err != nil || changed == nil {
			return err
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-o.done:
			return outcomesStopped(ctx)
		case <-changed:
		}
	}
}

func (o *Outcomes) pending(ctx context.Context) ([]messages.Message, error) {
	return outcomeRequest(ctx, o, func() ([]messages.Message, error) {
		var pending []messages.Message
		for _, record := range o.records {
			if record.result != nil && !record.accepted {
				pending = append(pending, copyMessage(*record.result))
			}
		}
		return pending, nil
	})
}

func outcomesStopped(ctx context.Context) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	return comms.ErrClosed
}

func copyMessage(message messages.Message) messages.Message {
	message.Payload = bytes.Clone(message.Payload)
	return message
}
