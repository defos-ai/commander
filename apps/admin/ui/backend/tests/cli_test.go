package tests

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"commander/admin/ui/cli"
)

func TestCLI(t *testing.T) {
	t.Run("help does not require a built frontend", func(t *testing.T) {
		t.Setenv("UI_STATIC_DIR", t.TempDir())
		var output bytes.Buffer
		if code := cli.Main([]string{"--help"}, &output); code != 0 {
			t.Fatalf("help exit = %d: %s", code, output.String())
		}
		if !strings.Contains(output.String(), "UI_LISTEN_ADDRESS") || !strings.Contains(output.String(), "UI_STATIC_DIR") {
			t.Fatalf("missing configuration help: %s", output.String())
		}
	})

	t.Run("missing frontend fails before listening", func(t *testing.T) {
		ready := false
		err := cli.RunWithOptions(context.Background(), []string{"--listen-address=127.0.0.1:0", "--static-dir=" + t.TempDir()}, io.Discard, cli.Options{
			Ready: func(net.Addr) { ready = true },
		})
		if err == nil || !strings.Contains(err.Error(), "index.html") || ready {
			t.Fatalf("missing build: ready=%v error=%v", ready, err)
		}
	})

	for _, args := range [][]string{{"--unknown"}, {"--listen-address=invalid"}, {"--listen-address=:70000"}, {"--static-dir="}, {"unexpected"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if err := cli.Run(context.Background(), args, io.Discard); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestCLIServesAndShutsDown(t *testing.T) {
	for _, useFlags := range []bool{false, true} {
		name := "environment overrides PORT"
		if useFlags {
			name = "flags override environment"
		}
		t.Run(name, func(t *testing.T) {
			directory := builtFrontend(t)
			t.Setenv("PORT", "invalid")
			t.Setenv("UI_LISTEN_ADDRESS", "127.0.0.1:0")
			t.Setenv("UI_STATIC_DIR", directory)
			var args []string
			if useFlags {
				t.Setenv("UI_LISTEN_ADDRESS", "invalid")
				t.Setenv("UI_STATIC_DIR", "missing-directory")
				args = []string{"--listen-address=127.0.0.1:0", "--static-dir=" + directory}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ready := make(chan net.Addr, 1)
			finished := make(chan error, 1)
			var output bytes.Buffer
			go func() {
				finished <- cli.RunWithOptions(ctx, args, &output, cli.Options{Ready: func(address net.Addr) { ready <- address }})
			}()
			var address net.Addr
			select {
			case address = <-ready:
			case err := <-finished:
				t.Fatalf("startup failed: %v", err)
			case <-ctx.Done():
				t.Fatal("server did not become ready")
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address.String()+"/healthz", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || string(body) != "ok\n" {
				t.Fatalf("health status=%d body=%q error=%v", response.StatusCode, body, err)
			}
			cancel()
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("server did not shut down")
			}
			if !strings.Contains(output.String(), "UI server stopped") || !strings.Contains(output.String(), "commander-ui") {
				t.Fatalf("missing lifecycle logs: %s", output.String())
			}
		})
	}
}
