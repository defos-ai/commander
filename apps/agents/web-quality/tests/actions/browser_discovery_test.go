package actions_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	web "commander/apps/agents/web-quality/actions"
	"commander/framework/agent/action"
	"commander/framework/agent/environment"
)

func TestConfiguredBrowserSymlink(t *testing.T) {
	// Arrange: Chrome packages link a PATH entry to an executable elsewhere.
	installation, bin := t.TempDir(), t.TempDir()
	executable := filepath.Join(installation, "chrome")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bin, "google-chrome")
	if err := os.Symlink(executable, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHROMIUM_PATH", link)
	t.Setenv("CHROMIUM_NO_SANDBOX", "")
	for _, test := range []struct {
		name  string
		scope *environment.DiscoveryScope
		want  action.Status
	}{
		{"default discovery", nil, action.Ready},
		{"default paths", &environment.DiscoveryScope{}, action.Ready},
		{"explicit empty paths", &environment.DiscoveryScope{Paths: []string{}}, action.Unavailable},
		{"symlink outside explicit scope", &environment.DiscoveryScope{Paths: []string{bin}}, action.Unavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, a := range []action.Executor{web.NewCapturePage(), web.NewCheckAccessibility()} {
				// Act.
				status, err := a.Discover(context.Background(), &environment.Local{}, test.scope)
				// Assert: configured symlinks work without weakening explicit constraints.
				if err != nil || status != test.want {
					t.Errorf("%s: status=%s, err=%v; want %s", a.Action().Name, status, err, test.want)
				}
			}
		})
	}
}
