package controller_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	adapterevents "commander/framework/comms/clients/adapter/events"
	commands "commander/framework/comms/clients/controller/commands"
	"commander/framework/comms/messages"
	"commander/framework/domain/agents"
)

func TestAdapterRetainsRepliesAndBackpressuresAtCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, bus, backend, consumer := adapterFixture(t)
		drainAdapterReplyTopic(ctx, bus)
		backend.waitForFinish = true
		lifetime, cancel := context.WithCancel(ctx)
		defer cancel()
		running := runAdapter(t, lifetime, consumer)
		// A backend may finish after its request deadline. Release any such
		// work on a failed assertion as well as on the successful path.
		t.Cleanup(func() {
			cancel()
			synctest.Wait()
			for len(backend.started) > 0 {
				work := <-backend.started
				work.finish <- agents.Agent{}
			}
		})

		first := adapterRequestWithDeadline(t, time.Now().Add(3*time.Second))
		original := finishAdapterRequest(t, ctx, bus, backend, first)
		// The admission limit includes active work as well as completed replies.
		for range 4094 {
			finishAdapterRequest(t, ctx, bus, backend, adapterRequest(t))
		}
		active := adapterRequestWithDeadline(t, time.Now().Add(2*time.Second))
		publish(t, ctx, bus, messages.ControllerCommands("default"), active)
		activeWork := receive(t, ctx, backend.started)
		t.Cleanup(func() {
			select {
			case activeWork.finish <- agents.Agent{}:
			default:
			}
		})

		publish(t, ctx, bus, messages.ControllerCommands("default"), first)
		if replay := adapterReply(t, ctx, bus, first); !reflect.DeepEqual(replay, original) {
			t.Fatalf("full ledger changed a retained reply: original=%+v replay=%+v", original, replay)
		}
		conflict := adapterRequest(t)
		conflict.ID = first.ID
		publish(t, ctx, bus, messages.ControllerCommands("default"), conflict)
		if rejected := receive(t, ctx, bus.rejected); rejected != conflict.ID {
			t.Fatalf("conflicting request was not rejected: %s", rejected)
		}

		expiring := adapterRequestWithDeadline(t, time.Now().Add(time.Second))
		publish(t, ctx, bus, messages.ControllerCommands("default"), expiring)
		synctest.Wait()
		if calls := backend.calls.Load(); calls != 4096 {
			t.Fatalf("request bypassed full ledger: calls=%d, want 4096", calls)
		}
		assertAdapterDeadlineReply(t, adapterReply(t, ctx, bus, expiring))
		publish(t, ctx, bus, messages.ControllerCommands("default"), first)
		if replay := adapterReply(t, ctx, bus, first); !reflect.DeepEqual(replay, original) {
			t.Fatal("overflow request evicted or changed the original reply")
		}

		waiting := adapterRequest(t)
		publish(t, ctx, bus, messages.ControllerCommands("default"), waiting)
		time.Sleep(time.Second)
		synctest.Wait()
		if calls := backend.calls.Load(); calls != 4096 {
			t.Fatalf("expired active request released capacity before completion: calls=%d", calls)
		}
		publish(t, ctx, bus, messages.ControllerCommands("default"), active)
		synctest.Wait()
		select {
		case reply := <-bus.replies:
			t.Fatalf("active duplicate did not wait for its original outcome: %+v", reply)
		default:
		}
		activeWork.finish <- agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: activeWork.agentID}}
		completed := adapterReply(t, ctx, bus, active)
		if replay := adapterReply(t, ctx, bus, active); !reflect.DeepEqual(replay, completed) {
			t.Fatal("active duplicate received a different outcome")
		}
		finishAdapterWork(t, ctx, bus, backend, waiting)

		// With no active work left, admission must wake when the earliest
		// completed request reaches its original envelope deadline.
		afterExpiry := adapterRequest(t)
		publish(t, ctx, bus, messages.ControllerCommands("default"), afterExpiry)
		synctest.Wait()
		if calls := backend.calls.Load(); calls != 4097 {
			t.Fatalf("completed reply expired too early: calls=%d", calls)
		}
		time.Sleep(time.Second)
		finishAdapterWork(t, ctx, bus, backend, afterExpiry)

		publish(t, ctx, bus, messages.ControllerCommands("default"), adapterRequest(t))
		synctest.Wait()
		if calls := backend.calls.Load(); calls != 4098 {
			t.Fatalf("request bypassed the refilled ledger: calls=%d", calls)
		}
		cancel()
		receive(t, ctx, running.done)
		if !errors.Is(running.err, context.Canceled) {
			t.Fatalf("cancellation did not interrupt admission backpressure: %v", running.err)
		}
	})
}

func TestAdapterExpiredRequestDoesNotReserveIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, bus, backend, consumer := adapterFixture(t)
		drainAdapterReplyTopic(ctx, bus)
		runAdapter(t, ctx, consumer)
		expired := adapterRequestWithDeadline(t, time.Now().Add(-time.Second))
		publish(t, ctx, bus, messages.ControllerCommands("default"), expired)
		assertAdapterDeadlineReply(t, adapterReply(t, ctx, bus, expired))
		if calls := backend.calls.Load(); calls != 0 {
			t.Fatalf("expired request executed: calls=%d", calls)
		}
		// No retained state should reject or replay this fresh envelope.
		live := adapterRequest(t)
		live.ID = expired.ID
		finishAdapterRequest(t, ctx, bus, backend, live)
		if calls := backend.calls.Load(); calls != 1 {
			t.Fatalf("expired request retained its identity: calls=%d", calls)
		}
	})
}

func adapterRequestWithDeadline(t *testing.T, deadline time.Time) messages.Message {
	t.Helper()
	request := adapterRequest(t)
	var envelope commands.CommandEnvelope
	if err := json.Unmarshal(request.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Deadline = deadline
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	request.Payload = payload
	return request
}

func finishAdapterRequest(t *testing.T, ctx context.Context, bus *adapterBus, backend *adapterBackend, request messages.Message) messages.Message {
	t.Helper()
	publish(t, ctx, bus, messages.ControllerCommands("default"), request)
	return finishAdapterWork(t, ctx, bus, backend, request)
}

func finishAdapterWork(t *testing.T, ctx context.Context, bus *adapterBus, backend *adapterBackend, request messages.Message) messages.Message {
	t.Helper()
	work := receive(t, ctx, backend.started)
	work.finish <- agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: work.agentID}}
	return adapterReply(t, ctx, bus, request)
}

func adapterReply(t *testing.T, ctx context.Context, bus *adapterBus, request messages.Message) messages.Message {
	t.Helper()
	reply := receive(t, ctx, bus.replies)
	if reply.CorrelationID != request.ID {
		t.Fatalf("reply correlation = %s, want %s", reply.CorrelationID, request.ID)
	}
	if acked := receive(t, ctx, bus.acked); acked != request.ID {
		t.Fatalf("acknowledgement = %s, want %s", acked, request.ID)
	}
	return reply
}

func assertAdapterDeadlineReply(t *testing.T, response messages.Message) {
	t.Helper()
	var reply adapterevents.ControllerReply
	if err := json.Unmarshal(response.Payload, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Error == nil || !errors.Is(adapterevents.DecodeError(reply.Error), context.DeadlineExceeded) {
		t.Fatalf("request deadline did not produce an ordinary timeout reply: %+v", reply)
	}
}

func drainAdapterReplyTopic(ctx context.Context, bus *adapterBus) {
	go func() {
		for {
			select {
			case <-bus.topics[messages.AdapterReplies(adapterID)]:
			case <-ctx.Done():
				return
			}
		}
	}()
}
