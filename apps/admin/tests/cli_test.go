package tests

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/admin/cli"
)

func TestAdminCLIHelpAndUsage(t *testing.T) {
	// Help must not need a valid API connection or expose retained credentials.
	t.Setenv("COMMANDER_API_URL", ":invalid")
	t.Setenv("COMMANDER_API_USERNAME", "invalid:username")
	t.Setenv("COMMANDER_API_PASSWORD", "private-password-fixture")
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"-h"}, {"serve", "--help"}, {"help", "serve"}} {
		var stdout, stderr bytes.Buffer
		if code := cli.Main(args, nil, &stdout, &stderr); code != 0 {
			t.Fatalf("help %v: exit %d, stderr %q", args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Usage: commander-admin") || stderr.Len() != 0 || strings.Contains(stdout.String(), "private-password-fixture") {
			t.Fatalf("help %v returned unexpected output", args)
		}
	}
	for _, args := range [][]string{
		{"unknown-command"}, {"--unknown"}, {"help", "extra"}, {"--api-url"}, {"--api-password=private-password-fixture"},
		{"serve", "--unknown"}, {"serve", "--listen-address=invalid"}, {"serve", "--static-dir="}, {"serve", "extra"},
		{"serve", "--api-password=private-password-fixture"},
	} {
		var stdout, stderr bytes.Buffer
		if code := cli.Main(args, nil, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("usage %v: exit %d, stdout %q, stderr %q", args, code, stdout.String(), stderr.String())
		}
		if strings.Contains(stderr.String(), "private-password-fixture") {
			t.Fatal("invalid arguments exposed the supplied password")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := cli.Run(ctx, nil, nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled command returned %v", err)
	}
}
