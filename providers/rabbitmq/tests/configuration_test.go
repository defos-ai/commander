package rabbitmq_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	rabbitcomms "github.com/defos-ai/commander/providers/rabbitmq/comms"
)

func TestRabbitMQRejectsInvalidConfigurationWithoutLeakingCredentials(t *testing.T) {
	const secret = "credential-secret-marker"
	for _, scenario := range []struct {
		name    string
		url     string
		options rabbitcomms.Options
	}{
		{name: "empty"},
		{name: "wrong_scheme", url: "https://user:" + secret + "@localhost/"},
		{name: "malformed_escape", url: "amqp://user:" + secret + "%zz@localhost/"},
		{name: "invalid_port", url: "amqp://user:" + secret + "@localhost:invalid/"},
		{name: "negative_prefetch", url: "amqp://user:" + secret + "@localhost/", options: rabbitcomms.Options{Prefetch: -1}},
		{name: "overflow_prefetch", url: "amqp://user:" + secret + "@localhost/", options: rabbitcomms.Options{Prefetch: 65536}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			bus, err := rabbitcomms.NewRabbitMQBus(t.Context(), scenario.url, scenario.options)
			if bus != nil {
				_ = bus.Close()
			}
			if err == nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("invalid configuration accepted or leaked credentials: %v", err)
			}
		})
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := rabbitcomms.NewRabbitMQBus(canceled, "amqp://localhost/", rabbitcomms.Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled constructor: %v", err)
	}
}

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
