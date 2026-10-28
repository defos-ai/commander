package controller_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	adapterevents "github.com/defos-ai/commander/framework/comms/clients/adapter/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/agents"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestAdapterRetainsRepliesAndBackpressuresAtCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, bus, backend, consumer := fixtures.Adapter(t)
		fixtures.DrainAdapterReplyTopic(t, ctx, bus)
		backend.WaitForFinish()
		lifetime, cancel := context.WithCancel(ctx)
		defer cancel()
		running := fixtures.RunAdapter(t, lifetime, consumer)
		// A backend may finish after its request deadline. Release any such
		// work on a failed assertion as well as on the successful path.
		t.Cleanup(func() {
			cancel()
			synctest.Wait()
			for len(backend.Started()) > 0 {
				work := <-backend.Started()
				work.Finish <- agents.Agent{}
			}
		})

		first := fixtures.AdapterRequestWithDeadline(t, time.Now().Add(3*time.Second))
		original := finishAdapterRequest(t, ctx, bus, backend, first)
		// The admission limit includes active work as well as completed replies.
		for range 4094 {
			finishAdapterRequest(t, ctx, bus, backend, fixtures.AdapterRequest(t))
		}
		active := fixtures.AdapterRequestWithDeadline(t, time.Now().Add(2*time.Second))
		fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), active)
		activeWork := fixtures.Receive(t, ctx, backend.Started())
		t.Cleanup(func() {
			select {
			case activeWork.Finish <- agents.Agent{}:
			default:
			}
		})

		fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), first)
		if replay := adapterReply(t, ctx, bus, first); !reflect.DeepEqual(replay, original) {
			t.Fatalf("full ledger changed a retained reply: original=%+v replay=%+v", original, replay)
		}
		conflict := fixtures.AdapterRequest(t)
		conflict.ID = first.ID
		fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), conflict)
		if rejected := fixtures.Receive(t, ctx, bus.Rejected()); rejected != conflict.ID {
			t.Fatalf("conflicting request was not rejected: %s", rejected)
		}

		expiring := fixtures.AdapterRequestWithDeadline(t, time.Now().Add(time.Second))
		fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), expiring)
		synctest.Wait()
		if calls := backend.Calls(); calls != 4096 {
			t.Fatalf("request bypassed full ledger: calls=%d, want 4096", calls)
		}
		assertAdapterDeadlineReply(t, adapterReply(t, ctx, bus, expiring))
		fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), first)
		if replay := adapterReply(t, ctx, bus, first); !reflect.DeepEqual(replay, original) {
			t.Fatal("overflow request evicted or changed the original reply")
		}

		waiting := fixtures.AdapterRequest(t)
		fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), waiting)
		time.Sleep(time.Second)
		synctest.Wait()
		if calls := backend.Calls(); calls != 4096 {
			t.Fatalf("expired active request released capacity before completion: calls=%d", calls)
		}
		fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), active)
		synctest.Wait()
		select {
		case reply := <-bus.Replies():
			t.Fatalf("active duplicate did not wait for its original outcome: %+v", reply)
		default:
		}
		activeWork.Finish <- agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: activeWork.AgentID}}
		completed := adapterReply(t, ctx, bus, active)
		if replay := adapterReply(t, ctx, bus, active); !reflect.DeepEqual(replay, completed) {
			t.Fatal("active duplicate received a different outcome")
		}
		finishAdapterWork(t, ctx, bus, backend, waiting)

		// With no active work left, admission must wake when the earliest
		// completed request reaches its original envelope deadline.
		afterExpiry := fixtures.AdapterRequest(t)
		fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), afterExpiry)
		synctest.Wait()
		if calls := backend.Calls(); calls != 4097 {
			t.Fatalf("completed reply expired too early: calls=%d", calls)
		}
		time.Sleep(time.Second)
		finishAdapterWork(t, ctx, bus, backend, afterExpiry)

		fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), fixtures.AdapterRequest(t))
		synctest.Wait()
		if calls := backend.Calls(); calls != 4098 {
			t.Fatalf("request bypassed the refilled ledger: calls=%d", calls)
		}
		cancel()
		fixtures.Receive(t, ctx, running.Done())
		if !errors.Is(running.Err(), context.Canceled) {
			t.Fatalf("cancellation did not interrupt admission backpressure: %v", running.Err())
		}
	})
}

func TestAdapterExpiredRequestDoesNotReserveIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, bus, backend, consumer := fixtures.Adapter(t)
		fixtures.DrainAdapterReplyTopic(t, ctx, bus)
		fixtures.RunAdapter(t, ctx, consumer)
		expired := fixtures.AdapterRequestWithDeadline(t, time.Now().Add(-time.Second))
		fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), expired)
		assertAdapterDeadlineReply(t, adapterReply(t, ctx, bus, expired))
		if calls := backend.Calls(); calls != 0 {
			t.Fatalf("expired request executed: calls=%d", calls)
		}
		live := fixtures.AdapterRequest(t)
		live.ID = expired.ID
		finishAdapterRequest(t, ctx, bus, backend, live)
		if calls := backend.Calls(); calls != 1 {
			t.Fatalf("expired request retained its identity: calls=%d", calls)
		}
	})
}

func finishAdapterRequest(t *testing.T, ctx context.Context, bus *fakes.AdapterBus, backend *fakes.AdapterBackend, request messages.Message) messages.Message {
	t.Helper()
	fixtures.Publish(t, ctx, bus, messages.ControllerCommands("default"), request)
	return finishAdapterWork(t, ctx, bus, backend, request)
}

func finishAdapterWork(t *testing.T, ctx context.Context, bus *fakes.AdapterBus, backend *fakes.AdapterBackend, request messages.Message) messages.Message {
	t.Helper()
	work := fixtures.Receive(t, ctx, backend.Started())
	work.Finish <- agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: work.AgentID}}
	return adapterReply(t, ctx, bus, request)
}

func adapterReply(t *testing.T, ctx context.Context, bus *fakes.AdapterBus, request messages.Message) messages.Message {
	t.Helper()
	reply := fixtures.Receive(t, ctx, bus.Replies())
	if reply.CorrelationID != request.ID {
		t.Fatalf("reply correlation = %s, want %s", reply.CorrelationID, request.ID)
	}
	if acked := fixtures.Receive(t, ctx, bus.Acknowledged()); acked != request.ID {
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
