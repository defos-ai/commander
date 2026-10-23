package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/admin/cli"
)

func TestCLI(t *testing.T) {
	t.Run("help does not require a built frontend", func(t *testing.T) {
		t.Setenv("UI_STATIC_DIR", t.TempDir())
		var output bytes.Buffer
		if code := cli.Main([]string{"serve", "--help"}, nil, &output, io.Discard); code != 0 {
			t.Fatalf("help exit = %d: %s", code, output.String())
		}
		if !strings.Contains(output.String(), "UI_LISTEN_ADDRESS") || !strings.Contains(output.String(), "UI_STATIC_DIR") ||
			!strings.Contains(output.String(), "UI_API_UPSTREAMS") {
			t.Fatalf("missing configuration help: %s", output.String())
		}
	})

	t.Run("missing frontend fails before listening", func(t *testing.T) {
		ready := false
		err := cli.RunWithOptions(context.Background(), []string{"serve", "--listen-address=127.0.0.1:0", "--static-dir=" + t.TempDir()}, nil, io.Discard, io.Discard, cli.Options{
			Ready: func(net.Addr) { ready = true },
		})
		if err == nil || !strings.Contains(err.Error(), "index.html") || ready {
			t.Fatalf("missing build: ready=%v error=%v", ready, err)
		}
	})

	for _, args := range [][]string{{"--unknown"}, {"--listen-address=invalid"}, {"--listen-address=:70000"}, {"--static-dir="}, {"unexpected"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if err := cli.Run(context.Background(), append([]string{"serve"}, args...), nil, io.Discard, io.Discard); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	t.Run("invalid api-upstreams", func(t *testing.T) {
		ready := false
		err := cli.RunWithOptions(context.Background(), []string{"serve", "--listen-address=127.0.0.1:0", "--static-dir=" + builtFrontend(t), "--api-upstreams=ftp://invalid"}, nil, io.Discard, io.Discard, cli.Options{
			Ready: func(net.Addr) { ready = true },
		})
		if err == nil || !strings.Contains(err.Error(), "api-upstreams") || ready {
			t.Fatalf("invalid api-upstreams accepted: ready=%v error=%v", ready, err)
		}
	})
}

func TestCLIServesAndShutsDown(t *testing.T) {
	for _, useFlags := range []bool{false, true} {
		name := "environment overrides PORT"
		if useFlags {
			name = "flags override environment"
		}
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/local/healthz" {
					t.Errorf("unexpected upstream path: %s", r.URL.Path)
				}
				io.WriteString(w, `{"status":"ok"}`)
			}))
			defer upstream.Close()
			publicURL := "http://localhost:14274/local"
			directory := builtFrontend(t)
			t.Setenv("PORT", "invalid")
			t.Setenv("UI_LISTEN_ADDRESS", "127.0.0.1:0")
			t.Setenv("UI_STATIC_DIR", directory)
			t.Setenv("UI_API_UPSTREAMS", "localhost:14274="+strings.TrimPrefix(upstream.URL, "http://"))
			var args []string
			if useFlags {
				t.Setenv("UI_LISTEN_ADDRESS", "invalid")
				t.Setenv("UI_STATIC_DIR", "missing-directory")
				t.Setenv("UI_API_UPSTREAMS", "invalid")
				args = []string{"--listen-address=127.0.0.1:0", "--static-dir=" + directory,
					"--api-upstreams=localhost:14274=" + strings.TrimPrefix(upstream.URL, "http://")}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ready := make(chan net.Addr, 1)
			finished := make(chan error, 1)
			var output bytes.Buffer
			go func() {
				finished <- cli.RunWithOptions(ctx, append([]string{"serve"}, args...), nil, io.Discard, &output, cli.Options{Ready: func(address net.Addr) { ready <- address }})
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
			request, err = http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address.String()+proxyPath(publicURL, "/healthz"), nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("X-Commander-UI", "1")
			response, err = http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			var result map[string]string
			err = json.NewDecoder(response.Body).Decode(&result)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || result["status"] != "ok" {
				t.Fatalf("API upstream configuration failed: status=%d body=%v error=%v", response.StatusCode, result, err)
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
