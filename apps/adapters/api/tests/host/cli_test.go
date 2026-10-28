package api_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/cli"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
)

func TestCLIFlagsOverrideEnvironment(t *testing.T) {
	fixture := hostfixtures.New(t)
	for _, name := range []string{"BUS_URL", "STORAGE_URL", "DATABASE_URL"} {
		t.Setenv(name, "unsupported://environment-user:environment-password@example.invalid/state")
	}
	t.Setenv("API_LISTEN_ADDRESS", "invalid-environment-address")
	t.Setenv("PORT", "invalid-lower-priority-port")
	t.Setenv("LOG_LEVEL", "invalid-environment-level")
	t.Setenv("LOG_FORMAT", "invalid-environment-format")
	t.Setenv("API_ALLOWED_HOSTS", "*.invalid")
	t.Setenv("API_TRUSTED_PROXIES", "invalid-proxy")
	t.Setenv("API_PROXY_HEADERS", "invalid-mode")

	server := hostfixtures.Start(t, append(fixture.LocalArgs(), "--allowed-hosts=127.0.0.1", "--trusted-proxies=", "--proxy-headers=none"))

	assertHealthy(t, server.Request(t, http.MethodGet, "/readyz"))
	if result := server.Request(t, http.MethodGet, "/api/v1/nonexistent", hostfixtures.Authenticate); result.Status != http.StatusNotFound {
		t.Fatalf("security flags did not override environment: status=%d body=%s", result.Status, result.Body)
	}
}

func TestCLIUsesEnvironmentConfiguration(t *testing.T) {
	fixture := hostfixtures.New(t)
	t.Setenv("BUS_URL", filepath.Join(fixture.Root, "bus"))
	t.Setenv("STORAGE_URL", filepath.Join(fixture.Root, "storage"))
	t.Setenv("DATABASE_URL", filepath.Join(fixture.Root, "database"))
	t.Setenv("API_LISTEN_ADDRESS", "127.0.0.1:0")
	t.Setenv("PORT", "invalid-lower-priority-port")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("API_ALLOWED_HOSTS", "api.example")
	t.Setenv("API_TRUSTED_PROXIES", "127.0.0.1")
	t.Setenv("API_PROXY_HEADERS", "x-forwarded")

	server := hostfixtures.Start(t, nil)

	assertHealthy(t, server.Request(t, http.MethodGet, "/readyz"))
	result := server.Request(t, http.MethodGet, "/api/v1/nonexistent", hostfixtures.Authenticate, func(r *http.Request) {
		r.Header.Set("X-Forwarded-Host", "api.example")
		r.Header.Set("X-Forwarded-Proto", "https")
	})
	if result.Status != http.StatusNotFound {
		t.Fatalf("security environment was not applied: status=%d body=%s", result.Status, result.Body)
	}
	assertSecurityHeaders(t, result, true)
	for _, directory := range []string{"bus", "database"} {
		if _, err := os.Stat(filepath.Join(fixture.Root, directory)); err != nil {
			t.Errorf("configured %s directory was not opened: %v", directory, err)
		}
	}
}

func TestCLIRejectsInvalidSecurityConfiguration(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "empty allowed hosts", args: []string{"--allowed-hosts="}},
		{name: "wildcard hostname", args: []string{"--allowed-hosts=*.example"}},
		{name: "URL instead of hostname", args: []string{"--allowed-hosts=https://api.example"}},
		{name: "hostname with port", args: []string{"--allowed-hosts=api.example:4274"}},
		{name: "hostname instead of proxy IP", args: []string{"--trusted-proxies=proxy.example"}},
		{name: "invalid proxy CIDR", args: []string{"--trusted-proxies=127.0.0.1/33"}},
		{name: "unsupported header mode", args: []string{"--proxy-headers=all"}},
		{name: "forwarded without trusted proxy", args: []string{"--proxy-headers=forwarded"}},
		{name: "X forwarded without trusted proxy", args: []string{"--proxy-headers=x-forwarded"}},
		{name: "X proto without trusted proxy", args: []string{"--proxy-headers=x-forwarded-proto"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := hostfixtures.New(t)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			var logs bytes.Buffer

			err := cli.Run(ctx, append(fixture.LocalArgs(), test.args...), &logs)

			if ctx.Err() != nil || err == nil {
				t.Fatalf("invalid security configuration did not fail startup promptly: %v", err)
			}
			if strings.Contains(err.Error()+logs.String(), "api-fixture-password") {
				t.Error("invalid configuration exposed API credentials")
			}
		})
	}
}

func TestCLIPortBindsAllInterfaces(t *testing.T) {
	fixture := hostfixtures.New(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PORT", strconv.Itoa(port))

	server := hostfixtures.Start(t, fixture.Args)

	address, ok := server.Address.(*net.TCPAddr)
	if !ok || !address.IP.IsUnspecified() || address.Port != port {
		t.Fatalf("PORT listener bound %s, want all interfaces on port %d", server.Address, port)
	}
	assertHealthy(t, server.Request(t, http.MethodGet, "/healthz"))
}

func TestCLIRejectsInvalidCredentials(t *testing.T) {
	for _, test := range []struct {
		name, username, password, diagnostic string
	}{
		{name: "username missing", password: "private-password-value", diagnostic: "API_USERNAME"},
		{name: "password missing", username: "private-username-value", diagnostic: "API_PASSWORD"},
		{name: "username contains colon", username: "private-username-value:bad", password: "private-password-value", diagnostic: "API_USERNAME"},
		{name: "password contains newline", username: "private-username-value", password: "private-password-value\nbad", diagnostic: "credentials"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := hostfixtures.New(t)
			t.Setenv("API_USERNAME", test.username)
			t.Setenv("API_PASSWORD", test.password)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			var logs bytes.Buffer

			err := cli.Run(ctx, fixture.LocalArgs(), &logs)

			if ctx.Err() != nil || err == nil {
				t.Fatalf("invalid credentials did not fail startup promptly: %v", err)
			}
			if !strings.Contains(err.Error(), test.diagnostic) {
				t.Errorf("error does not identify %s: %v", test.diagnostic, err)
			}
			for _, secret := range []string{"private-password-value", "private-username-value"} {
				if strings.Contains(err.Error()+logs.String(), secret) {
					t.Errorf("invalid configuration exposed credential %q", secret)
				}
			}
		})
	}
}

func TestCLIDoesNotAcceptCredentialFlags(t *testing.T) {
	for _, flag := range []string{"username", "password"} {
		t.Run(flag, func(t *testing.T) {
			fixture := hostfixtures.New(t)
			const secret = "private-command-line-credential"
			args := append(fixture.LocalArgs(), "--"+flag+"="+secret)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			var logs bytes.Buffer

			err := cli.Run(ctx, args, &logs)

			if ctx.Err() != nil || err == nil {
				t.Fatalf("credential flag did not fail startup promptly: %v", err)
			}
			if strings.Contains(err.Error()+logs.String(), secret) {
				t.Error("rejected credential flag appeared in the error or logs")
			}
		})
	}
}

func TestCLIRejectsInvalidSelectedPort(t *testing.T) {
	for _, port := range []string{"0", "65536", "private-invalid-port"} {
		t.Run(port, func(t *testing.T) {
			fixture := hostfixtures.New(t)
			t.Setenv("PORT", port)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			var logs bytes.Buffer

			err := cli.Run(ctx, fixture.Args, &logs)

			if ctx.Err() != nil || err == nil {
				t.Fatalf("invalid PORT did not fail startup promptly: %v", err)
			}
			if !strings.Contains(err.Error(), "PORT") {
				t.Errorf("error does not identify PORT: %v", err)
			}
			if strings.Contains(err.Error()+logs.String(), "private-invalid-port") {
				t.Error("invalid PORT value was echoed in the error or logs")
			}
		})
	}
}

func TestCLIRejectsUnavailableProvidersWithoutExposingCredentials(t *testing.T) {
	const secret = "provider-private-password"
	for _, test := range []struct {
		name, flag, url string
	}{
		{name: "unsupported database", flag: "database-url", url: "postgres://user:" + secret + "@127.0.0.1:5432/commander"},
		{name: "unavailable RabbitMQ", flag: "bus-url", url: "amqp://user:" + secret + "@127.0.0.1:1"},
		{name: "unavailable MongoDB", flag: "database-url", url: "mongodb://user:" + secret + "@127.0.0.1:1/commander?serverSelectionTimeoutMS=50"},
		{name: "malformed MongoDB query", flag: "database-url", url: "mongodb://user:" + secret + "@127.0.0.1:1/commander?tlsCertificateKeyFilePassword=" + secret + "%zz"},
		{name: "incomplete S3 credentials", flag: "storage-url", url: "s3://:" + secret + "@artifacts/bucket"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := hostfixtures.New(t)
			args := append(fixture.LocalArgs(), "--"+test.flag+"="+test.url)
			logPath := filepath.Join(t.TempDir(), "startup.log")
			logs, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { logs.Close() })
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()

			err = cli.Run(ctx, args, logs)

			if ctx.Err() != nil || err == nil {
				t.Fatalf("unavailable provider did not fail startup promptly: %v", err)
			}
			if !strings.Contains(err.Error(), test.flag) {
				t.Errorf("error does not identify %s: %v", test.flag, err)
			}
			logContent, readErr := os.ReadFile(logPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.Contains(err.Error()+string(logContent), secret) {
				t.Error("provider connection credentials appeared in the error or logs")
			}
		})
	}
}

func TestMainHelpAndConfigurationExitCodes(t *testing.T) {
	hostfixtures.New(t)
	t.Setenv("API_USERNAME", "")
	t.Setenv("API_PASSWORD", "")
	t.Setenv("DATABASE_URL", "postgres://user:private-help-password@example.invalid/database")
	t.Setenv("PORT", "private-invalid-port")
	var help bytes.Buffer
	if code := cli.Main([]string{"--help"}, &help, &help); code != 0 || !strings.Contains(help.String(), "commander-api") {
		t.Fatalf("help failed: code=%d output=%s", code, &help)
	}
	if strings.Contains(help.String(), "private-help-password") || strings.Contains(help.String(), "private-invalid-port") {
		t.Error("help includes environment configuration values")
	}

	var failure bytes.Buffer
	failureCode := cli.Main(nil, io.Discard, &failure)

	if failureCode != 1 || failure.Len() == 0 {
		t.Fatalf("configuration error should exit 1 with a diagnostic: code=%d output=%s", failureCode, &failure)
	}
	if strings.Contains(failure.String(), "private-help-password") || strings.Contains(failure.String(), "private-invalid-port") {
		t.Error("configuration diagnostic includes environment configuration values")
	}
}
