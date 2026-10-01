package comms

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type InboxMode string

const (
	// TransientExclusive has one active consumer. Closing or losing that owner
	// ends the inbox lifetime; reopening starts with new publications only.
	TransientExclusive InboxMode = "transient-exclusive"

	// DurableShared retains work from its first declaration through consumer
	// downtime. Consumers of the same inbox compete for deliveries; unsettled
	// work can be redelivered after owner loss. Processing order is not guaranteed.
	DurableShared InboxMode = "durable-shared"
)

// Inbox identity is (Topic, Name). Name is a consumer group within that topic,
// independent of routing. A durable inbox must be redeclared with the same mode.
type Inbox struct {
	Topic string
	Name  string
	Mode  InboxMode
}

func (i Inbox) Validate() error {
	for _, field := range []struct{ name, value string }{
		{"topic", i.Topic}, {"name", i.Name},
	} {
		if field.value == "" || field.value != strings.TrimSpace(field.value) || !utf8.ValidString(field.value) {
			return fmt.Errorf("inbox %s must be nonblank UTF-8 text without surrounding whitespace", field.name)
		}
	}
	switch i.Mode {
	case TransientExclusive, DurableShared:
		return nil
	default:
		return fmt.Errorf("unknown inbox mode %q", i.Mode)
	}
}
