package fixtures

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
)

func NativeProgressObserver(t *testing.T, ctx context.Context, fixture NativeManager) comms.Subscription {
	t.Helper()
	observer, err := fixture.Bus.Subscribe(ctx, comms.Inbox{
		Topic: messages.ControllerEvents(messages.DefaultControllerType), Name: "native-progress-observer", Mode: comms.TransientExclusive,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := observer.Close(); err != nil {
			t.Error(err)
		}
	})
	return observer
}

func AnnounceNativeRegistration(t *testing.T, report ChildReport) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(report.Home, "register"), nil, 0600); err != nil {
		t.Fatal(err)
	}
}
