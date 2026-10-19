package packages_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/cli"
)

func TestSpawnerCLIValidatesBeforeCreatingState(t *testing.T) {
	t.Setenv("COMMANDER_AGENT_ID_SUFFIX", "")
	t.Setenv("SPAWNER_RUNTIME", "")
	t.Setenv("SPAWNER_DOCKER_NETWORK", "")
	for _, test := range []struct{ name, argument, expected string }{
		{"unknown action", "--actions=checkout-template", "unknown action"},
		{"duplicate action", "--actions=spawn-agent,spawn-agent", "duplicate"},
		{"negative heartbeat", "--heartbeat-interval=-1s", "heartbeat-interval"},
		{"zero registration timeout", "--registration-timeout=0", "registration-timeout"},
		{"negative registration timeout", "--registration-timeout=-1s", "registration-timeout"},
		{"unpinned package", "--spawn-agents=github.com/defos-ai/fixture-agent@latest", "stable version"},
		{"invalid bus", "--bus-url=https://example.invalid", "bus"},
		{"invalid storage", "--storage-url=https://example.invalid", "storage"},
		{"removed state flag", "--state-dir=ignored", "flag provided but not defined"},
		{"removed runtime flag", "--runtime=native", "flag provided but not defined"},
		{"removed network flag", "--docker-network=commander_default", "flag provided but not defined"},
		{"positional argument", "unexpected", "positional"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "unused")
			t.Setenv("SPAWNER_STATE_DIR", filepath.Join(root, "spawner"))
			args := []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"), test.argument}
			err := cli.Run(t.Context(), args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("invalid configuration returned %v", err)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid configuration created state")
			}
		})
	}
	for _, value := range []string{"builder-agent-12345678", "spawner-agent-12345678", "arbitrary-text", "1234567", "123456789", "1234ABCD", "1234567g", " 12345678", "12345678 ", " "} {
		t.Run("agent id suffix "+value, func(t *testing.T) {
			t.Setenv("COMMANDER_AGENT_ID_SUFFIX", value)
			root := filepath.Join(t.TempDir(), "unused")
			t.Setenv("SPAWNER_STATE_DIR", filepath.Join(root, "spawner"))
			err := cli.Run(t.Context(), []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage")}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "COMMANDER_AGENT_ID_SUFFIX") {
				t.Fatalf("invalid ID suffix returned %v", err)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid identity created state")
			}
		})
	}
}

func TestSpawnerCLIValidatesEnvironmentBeforeCreatingState(t *testing.T) {
	t.Setenv("COMMANDER_AGENT_ID_SUFFIX", "")
	t.Setenv("SPAWNER_RUNTIME", "")
	t.Setenv("SPAWNER_DOCKER_NETWORK", "")
	for _, test := range []struct {
		name, expected string
		environment    map[string]string
	}{
		{"blank state", "SPAWNER_STATE_DIR", map[string]string{"SPAWNER_STATE_DIR": " "}},
		{"unknown runtime", "SPAWNER_RUNTIME", map[string]string{"SPAWNER_RUNTIME": "containerd"}},
		{"docker without network", "SPAWNER_DOCKER_NETWORK", map[string]string{"SPAWNER_RUNTIME": "docker"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "unused")
			t.Setenv("SPAWNER_STATE_DIR", filepath.Join(root, "spawner"))
			for name, value := range test.environment {
				t.Setenv(name, value)
			}
			err := cli.Run(t.Context(), []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage")}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("invalid environment returned %v", err)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid environment created state")
			}
		})
	}
	// Empty environment values retain the ordinary native and state defaults.
	t.Setenv("BUS_URL", "")
	t.Setenv("STORAGE_URL", "")
	t.Setenv("SPAWNER_STATE_DIR", "")
	t.Setenv("SPAWNER_DOCKER_NETWORK", "host")
	t.Setenv("DOCKER_HOST", "invalid://unrelated-daemon")
	t.Setenv("DOCKER_TLS_VERIFY", "1")
	t.Setenv("DOCKER_CERT_PATH", filepath.Join(t.TempDir(), "missing-certificates"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := cli.Run(ctx, nil, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("empty defaults or irrelevant Docker environment were rejected: %v", err)
	}
}

func TestSpawnerCLIDockerRequiresRemoteProvidersBeforeStartup(t *testing.T) {
	t.Setenv("COMMANDER_AGENT_ID_SUFFIX", "")
	t.Setenv("SPAWNER_RUNTIME", "docker")
	t.Setenv("SPAWNER_DOCKER_NETWORK", "commander_default")
	for _, test := range []struct {
		name, busURL, storageURL, expected string
	}{
		{"filesystem bus", "./state/bus", "s3://artifacts", "amqp or amqps"},
		{"filesystem storage", "amqp://example.invalid/", "./state/storage", "s3 storage-url"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "unused")
			t.Setenv("SPAWNER_STATE_DIR", root)
			err := cli.Run(t.Context(), []string{
				"--bus-url=" + test.busURL, "--storage-url=" + test.storageURL,
			}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("invalid Docker providers returned %v", err)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid Docker settings created state")
			}
		})
	}
}

func TestSpawnerCLIDockerValidatesNetworkAndExplicitTimeoutBeforeProviders(t *testing.T) {
	t.Setenv("COMMANDER_AGENT_ID_SUFFIX", "")
	t.Setenv("SPAWNER_RUNTIME", "docker")
	t.Setenv("SPAWNER_DOCKER_NETWORK", "commander_default")
	for _, network := range []string{"host", "none", "bridge", "default", "bad/network", "commander default", "../network", strings.Repeat("n", 129)} {
		t.Run(network, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "unused")
			t.Setenv("SPAWNER_STATE_DIR", filepath.Join(root, "spawner"))
			t.Setenv("SPAWNER_DOCKER_NETWORK", network)
			err := cli.Run(t.Context(), []string{
				"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"),
			}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "SPAWNER_DOCKER_NETWORK") {
				t.Fatalf("invalid Docker network reached providers: %v", err)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid Docker network created provider state")
			}
		})
	}
	// The Docker default must not overwrite an explicit timeout. Zero and
	// negative durations remain invalid, while a positive override is accepted.
	for _, timeout := range []string{"0s", "-1s", "5s"} {
		t.Run("timeout "+timeout, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			err := cli.Run(ctx, []string{"--bus-url=amqp://example.invalid/", "--storage-url=s3://artifacts", "--registration-timeout=" + timeout}, io.Discard)
			if timeout == "5s" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("valid explicit timeout was rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "registration-timeout") {
				t.Fatalf("invalid explicit timeout was overwritten by default: %v", err)
			}
		})
	}
}

func TestSpawnerCLIHelpAndPreCanceledStartupDoNotTouchProviders(t *testing.T) {
	t.Setenv("COMMANDER_AGENT_ID_SUFFIX", "")
	t.Setenv("SPAWNER_RUNTIME", "native")
	t.Setenv("BUS_URL", "amqp://user:private-bus-sentinel@example.invalid")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "private-storage-sentinel")
	t.Setenv("SECRETS_ENCRYPTION_KEY", "private-encryption-sentinel")
	root := filepath.Join(t.TempDir(), "unused")
	t.Setenv("SPAWNER_STATE_DIR", filepath.Join(root, "spawner"))
	args := []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage")}
	var output bytes.Buffer
	if err := cli.Run(t.Context(), append(args, "--help"), &output); err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"spawn-agent", "spawn-agents", "registration-timeout"} {
		if !strings.Contains(output.String(), word) {
			t.Errorf("help omits %s", word)
		}
	}
	for _, flag := range []string{"-state-dir", "-runtime", "-docker-network"} {
		if strings.Contains(output.String(), flag) {
			t.Errorf("help advertises removed flag %s", flag)
		}
	}
	if strings.Contains(output.String(), "sentinel") {
		t.Fatal("help exposed environment values")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := cli.Run(ctx, args, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled host returned %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("help or pre-canceled startup created state")
	}
}
