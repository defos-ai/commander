package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMCPCommandLine(t *testing.T) {
	for _, scenario := range []struct {
		name string
		args []string
		help bool
	}{
		{"help", []string{"--help"}, true},
		{"unknown flag", []string{"--unknown-flag"}, false},
		{"removed state directory", []string{"--state-dir=state"}, false},
		{"removed controller target ID", []string{"--controller-id=default"}, false},
		{"removed controller type", []string{"--controller-type=default"}, false},
		{"removed adapter ID", []string{"--adapter-id=mcp-adapter-0123abcd"}, false},
		{"unsupported broker", []string{"--bus-url=amqp://user:secret-credential@localhost"}, false},
		{"unsupported storage", []string{"--storage-url=s3://unsupported?token=secret-token"}, false},
		{"unsupported database", []string{"--database-url=mongodb://user:secret-credential@localhost/defos"}, false},
		{"malformed secret connection", []string{"--bus-url=amqp://user:secret-credential@localhost/%zz?token=secret-token"}, false},
		{"filesystem credentials", []string{"--storage-url=file://user:secret-credential@localhost/state"}, false},
		{"filesystem query secret", []string{"--database-url=file:///state?token=secret-token"}, false},
		{"removed controller heartbeat flag", []string{"--heartbeat-timeout=10s"}, false},
		{"explicit empty overrides environment", []string{"--bus-url="}, false},
		{"empty storage overrides environment", []string{"--storage-url="}, false},
		{"empty database overrides environment", []string{"--database-url="}, false},
		{"invalid log format", []string{"--log-format=yaml"}, false},
		{"invalid address", []string{"--listen-address=invalid"}, false},
		{"wildcard host", []string{"--allowed-hosts=*"}, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: empty working directory with all runtime locations below it.
			d := newDeployment(t)
			if scenario.help {
				for key := range d.env {
					d.env[key] = "invalid://user:private-environment-value@host"
				}
			}
			command := exec.CommandContext(d.ctx, mcpBinary, scenario.args...)
			command.Dir, command.Env = d.root, processEnvironment(d.env)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr

			// Act.
			err := command.Run()

			// Assert: help succeeds; invalid arguments fail before creating state.
			if scenario.help {
				if err != nil || !strings.Contains(stderr.String(), "Usage: commander-mcp") {
					t.Fatalf("help: %v\n%s", err, stderr.String())
				}
				if strings.Contains(stderr.String(), "private-environment-value") {
					t.Fatal("help exposed an environment value")
				}
				for _, flag := range []string{"bus-url", "storage-url", "database-url", "listen-address", "allowed-hosts", "log-level", "log-format"} {
					if !strings.Contains(stderr.String(), flag) {
						t.Errorf("help does not describe %s", flag)
					}
				}
				for _, removed := range []string{"state-dir", "heartbeat-timeout", "controller-id", "controller-type", "adapter-id"} {
					if strings.Contains(stderr.String(), removed) {
						t.Errorf("help advertises removed flag %s", removed)
					}
				}
			} else {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 || stderr.Len() == 0 {
					t.Fatalf("invalid arguments did not report failure: %v, %s", err, stderr.String())
				}
			}
			if strings.Contains(stderr.String(), "secret-") || strings.Contains(stderr.String(), "private-environment-value") {
				t.Fatalf("CLI exposed connection credentials: %s", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("CLI wrote unexpected stdout: %s", stdout.String())
			}
			entries, err := os.ReadDir(d.root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("command created runtime state: %v, %v", entries, err)
			}
		})
	}
}

func TestMCPIdentityAndRoutingIgnoreEnvironment(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprintf("flags_override_environment=%t", override), func(t *testing.T) {
			// Arrange.
			d := newDeployment(t)
			d.env["ADAPTER_ID"] = "mcp-adapter-0123abcd"
			d.env["CONTROLLER_TYPE"] = "unused-controller"
			seedMission(t, d)

			// Act: publish a command and receive its reply through the default controller.
			endpoint, adapter := d.mcp(t, override)
			session := connect(t, d.ctx, endpoint)
			operation := startMission(t, d, session)

			// Assert.
			if adapter.id == d.env["ADAPTER_ID"] || operation.ID == "" {
				t.Fatalf("environment changed generated identity or default routing: id=%q operation=%+v", adapter.id, operation)
			}
		})
	}
}

func TestMCPHTTPHostAndOrigin(t *testing.T) {
	// Arrange: the live service permits its loopback address and one ingress host.
	d := newDeployment(t)
	endpoint, _ := d.mcp(t, false, "--allowed-hosts=localhost,127.0.0.1,::1,commander.test")
	initialization := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"http-guard-test","version":"1"}}}`)
	for _, scenario := range []struct {
		name, host, origin, fetchSite string
		status                        int
	}{
		{"native loopback", "", "", "", http.StatusOK},
		{"configured ingress", "commander.test", "https://commander.test", "same-origin", http.StatusOK},
		{"untrusted host", "attacker.test", "", "", http.StatusForbidden},
		{"foreign origin", "commander.test", "https://attacker.test", "", http.StatusForbidden},
		{"cross-site browser", "commander.test", "", "cross-site", http.StatusForbidden},
		{"null origin", "commander.test", "null", "", http.StatusForbidden},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange.
			request, err := http.NewRequestWithContext(d.ctx, http.MethodPost, endpoint, bytes.NewReader(initialization))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json, text/event-stream")
			if scenario.host != "" {
				request.Host = scenario.host
			}
			if scenario.origin != "" {
				request.Header.Set("Origin", scenario.origin)
			}
			if scenario.fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", scenario.fetchSite)
			}

			// Act.
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body := responseBody(response)

			// Assert.
			if response.StatusCode != scenario.status {
				t.Fatalf("HTTP status = %d, want %d: %.200s", response.StatusCode, scenario.status, body)
			}
			if scenario.status == http.StatusOK {
				var reply struct {
					Result json.RawMessage
					Error  json.RawMessage
				}
				if json.Unmarshal(body, &reply) != nil || len(reply.Result) == 0 || len(reply.Error) != 0 {
					t.Fatalf("MCP did not initialize successfully: %.200s", body)
				}
			}
		})
	}
}
