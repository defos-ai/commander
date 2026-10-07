package comms

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/defos-ai/commander/framework/comms"
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	Exchange       = "commander"
	FailedExchange = "commander.failed"
	FailedQueue    = "commander.failed"
)

// QueueName identifies (topic, inbox name) without depending on an instance ID
// or inbox mode. The same identity cannot silently change its durability.
func QueueName(inbox comms.Inbox) string {
	identity, _ := json.Marshal([2]string{inbox.Topic, inbox.Name})
	digest := sha256.Sum256(identity)
	label := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, inbox.Topic)
	if len(label) > 80 {
		label = label[:80]
	}
	return "commander." + label + "." + hex.EncodeToString(digest[:])
}

func declareTopology(channel *amqp.Channel) error {
	if err := channel.ExchangeDeclare(Exchange, "direct", true, false, false, false, nil); err != nil {
		return err
	}
	if err := channel.ExchangeDeclare(FailedExchange, "fanout", true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := channel.QueueDeclare(FailedQueue, true, false, false, false, amqp.Table{"x-queue-type": "classic"}); err != nil {
		return err
	}
	return channel.QueueBind(FailedQueue, "", FailedExchange, false, nil)
}
