package rabbitmq_test

import (
	"context"
	"errors"
	rabbitcomms "github.com/defos-ai/commander/providers/rabbitmq/comms"
	"net"
	"testing"
	"time"
)

func TestRabbitMQConstructorDeadlineInterruptsAStalledAMQPHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)

	go func() {
		bus, err := rabbitcomms.NewRabbitMQBus(ctx, "amqp://guest:guest@"+listener.Addr().String()+"/", rabbitcomms.Options{})
		if bus != nil {
			_ = bus.Close()
		}
		result <- err
	}()

	select {
	case conn := <-accepted:
		defer conn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("constructor did not connect to the stalled peer")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stalled handshake returned %v, want DeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("constructor did not return after its deadline")
	}
}
