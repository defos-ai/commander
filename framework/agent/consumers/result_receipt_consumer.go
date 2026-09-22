package consumers

import (
	"context"
	"log/slog"

	agentevents "commander/framework/comms/clients/agent/events"
	"commander/framework/comms/messages"
)

// ResultReceiptConsumer processes confirmations independently of action commands.
// Consume and AfterAck must run sequentially for each received message.
type ResultReceiptConsumer struct {
	AgentID  string
	Outcomes *Outcomes
	Logger   *slog.Logger

	accepted bool
}

func (c *ResultReceiptConsumer) Consume(ctx context.Context, receipt messages.Message) error {
	c.accepted = false
	if receipt.AgentID != c.AgentID || receipt.Type != agentevents.ActionResultAccepted {
		return nil
	}
	if err := c.Outcomes.validateReceipt(receipt); err != nil {
		logger := c.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.WarnContext(ctx, "result receipt rejected", "message_id", receipt.ID,
			"correlation_id", receipt.CorrelationID, "error", err)
		// Unrelated confirmations must not change result retry state.
		return nil
	}
	c.accepted = true
	return nil
}

// AfterAck releases waiting commands only once receipt processing is acknowledged.
func (c *ResultReceiptConsumer) AfterAck(receipt messages.Message) {
	if c.accepted {
		c.Outcomes.acceptReceipt(receipt.CorrelationID)
	}
}
