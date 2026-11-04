package agent

import (
	"context"
	"slices"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/consumers"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

type reusableSecretDefault struct {
	request   secrets.Request
	message   messages.Message
	caller    context.Context
	execution context.Context
}

func (r *secretRuntime) newReusableDefault(ctx context.Context, waits *workSecretWaits, name string, value []byte) (*reusableSecretDefault, error) {
	if len(value) > controllerevents.MaxSecretDefaultBytes {
		return nil, action.ErrSecretRequestUnsupported
	}
	now := time.Now().UTC()
	deadline := now.Add(r.config.RequestTTL)
	for _, limit := range []time.Time{consumers.CommandDeadline(waits.context), contextDeadline(ctx), contextDeadline(waits.context)} {
		if !limit.IsZero() && limit.Before(deadline) {
			deadline = limit
		}
	}
	if !now.Before(deadline) {
		return nil, context.DeadlineExceeded
	}
	request := secrets.Request{ID: id.New(), AgentID: r.agentID, AgentType: r.typeName,
		Key: secrets.Key{Name: name}, Status: secrets.Pending, RequestedAt: now, ExpiresAt: deadline}
	message, err := controllerevents.NewSecretSyncRequested(request, value)
	if err != nil {
		return nil, action.ErrSecretRequestUnsupported
	}
	return &reusableSecretDefault{request: request, message: message, caller: ctx, execution: waits.context}, nil
}

func discardReusableDefault(entry *secretEntry, offered *reusableSecretDefault) {
	clear(offered.message.Payload)
	offered.message = messages.Message{}
	entry.defaults = slices.DeleteFunc(entry.defaults, func(candidate *reusableSecretDefault) bool { return candidate == offered })
	if entry.defaultOwner == offered {
		entry.defaultOwner = nil
		entry.defaultRequest = false
		entry.request, entry.message = secrets.Request{}, messages.Message{}
	}
}

func clearReusableDefaults(entry *secretEntry) {
	for _, offered := range entry.defaults {
		clear(offered.message.Payload)
		offered.message = messages.Message{}
	}
	if entry.defaultRequest {
		entry.message = messages.Message{}
	}
	entry.defaultOwner = nil
}

// A default belongs to a waiting call, not a periodic refresh. Select another
// live caller only after the original owner ends, retaining each request's ID.
func prepareReusableDefault(entry *secretEntry, now time.Time) {
	if entry.installed {
		clearReusableDefaults(entry)
		return
	}
	for index := len(entry.defaults) - 1; index >= 0; index-- {
		offered := entry.defaults[index]
		if !now.Before(offered.request.ExpiresAt) || requestCanceled(offered.caller, offered.execution) != nil {
			discardReusableDefault(entry, offered)
		}
	}
	if entry.defaultOwner != nil {
		return
	}
	for _, offered := range entry.defaults {
		if offered.message.ID == "" {
			continue
		}
		clear(entry.message.Payload)
		clear(entry.delivery.Payload)
		entry.request, entry.message = offered.request, offered.message
		entry.delivery, entry.ack = messages.Message{}, messages.Message{}
		entry.defaultOwner, entry.defaultRequest = offered, true
		return
	}
}
