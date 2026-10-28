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
	"github.com/defos-ai/commander/framework/crypto"
)

func TestSpawnerCLIValidatesBeforeCreatingState(t *testing.T) {
	t.Setenv("COMMANDER_AGENT_ID_SUFFIX", "")
	t.Setenv("SPAWNER_RUNTIME", "")
	t.Setenv("SPAWNER_DOCKER_NETWORK", "")
	for _, test := range []struct{ name, argument, expected string }{
		{"unknown action", "--actions=checkout-template", "unknown action"},
		{"duplicate action", "--actions=spawn-agent,spawn-agent", "duplicate"},
		{"full agent ID", "--agent-id-suffix=spawner-agent-12345678", "agent-id-suffix"},
		{"negative heartbeat", "--heartbeat-interval=-1s", "heartbeat-interval"},
		{"zero registration timeout", "--registration-timeout=0", "registration-timeout"},
		{"negative registration timeout", "--registration-timeout=-1s", "registration-timeout"},
		{"removed startup flag", "--spawn-agents=github.com/defos-ai/fixture-agent@v0.2.0", "flag provided but not defined"},
		{"invalid bus", "--bus-url=https://example.invalid", "bus"},
		{"invalid storage", "--storage-url=https://example.invalid", "storage"},
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
	t.Run("invalid environment suffix", func(t *testing.T) {
		t.Setenv("COMMANDER_AGENT_ID_SUFFIX", "1234ABCD")
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

func TestSpawnerCLIAgentIDSuffixFlagOverridesEnvironment(t *testing.T) {
	t.Setenv("COMMANDER_AGENT_ID_SUFFIX", "invalid")
	t.Setenv("SPAWNER_RUNTIME", "native")
	root := filepath.Join(t.TempDir(), "unused")
	t.Setenv("SPAWNER_STATE_DIR", filepath.Join(root, "spawner"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, suffix := range []string{"1234abcd", ""} {
		args := []string{
			"--bus-url=" + filepath.Join(root, "bus"),
			"--storage-url=" + filepath.Join(root, "storage"),
			"--agent-id-suffix=" + suffix,
		}
		if err := cli.Run(ctx, args, io.Discard); !errors.Is(err, context.Canceled) {
			t.Fatalf("explicit suffix %q did not override the environment: %v", suffix, err)
		}
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pre-canceled startup created state")
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
		{"kubernetes without namespace", "SPAWNER_KUBERNETES_NAMESPACE", map[string]string{"SPAWNER_RUNTIME": "kubernetes", "SPAWNER_KUBERNETES_NAMESPACE": ""}},
		{"docker without network", "SPAWNER_DOCKER_NETWORK", map[string]string{"SPAWNER_RUNTIME": "docker"}},
		{"invalid secrets key", "SECRETS_ENCRYPTION_KEY", map[string]string{"SECRETS_ENCRYPTION_KEY": "private-invalid-key"}},
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
			if strings.Contains(err.Error(), "private-invalid-key") {
				t.Fatal("invalid encryption key was exposed in the error")
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid environment created state")
			}
		})
	}
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
	for _, network := range []string{"host", "bad/network", strings.Repeat("n", 129)} {
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
	t.Setenv("GITHUB_TOKEN", "private-github-sentinel")
	root := filepath.Join(t.TempDir(), "unused")
	t.Setenv("SPAWNER_STATE_DIR", filepath.Join(root, "spawner"))
	args := []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage")}
	var output bytes.Buffer
	if err := cli.Run(t.Context(), append(args, "--help"), &output); err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"spawn-agent", "install-agent", "registration-timeout", "agent-id-suffix"} {
		if !strings.Contains(output.String(), word) {
			t.Errorf("help omits %s", word)
		}
	}
	for _, flag := range []string{"-state-dir", "-runtime", "-docker-network", "-search-paths", "-spawn-agents"} {
		if strings.Contains(output.String(), flag) {
			t.Errorf("help advertises removed flag %s", flag)
		}
	}
	if strings.Contains(output.String(), "sentinel") {
		t.Fatal("help exposed environment values")
	}
	t.Setenv("SECRETS_ENCRYPTION_KEY", "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := cli.Run(ctx, args, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled host returned %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("help or pre-canceled startup created state")
	}
}

func TestSpawnerCLIEmbeddedSecretsKeyOverridesEnvironment(t *testing.T) {
	t.Setenv("COMMANDER_AGENT_ID_SUFFIX", "")
	t.Setenv("SPAWNER_RUNTIME", "native")
	t.Setenv("SECRETS_ENCRYPTION_KEY", "private-invalid-key")
	root := filepath.Join(t.TempDir(), "unused")
	t.Setenv("SPAWNER_STATE_DIR", filepath.Join(root, "spawner"))
	args := []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage")}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, key := range []string{strings.Repeat("ab", 32), ""} {
		if err := cli.RunWithOptions(ctx, args, io.Discard, cli.Options{SecretsEncryptionKey: &key}); !errors.Is(err, context.Canceled) {
			t.Fatalf("embedded key did not override invalid environment: %v", err)
		}
	}
	key := "private-invalid-embedded-key"
	err := cli.RunWithOptions(ctx, args, io.Discard, cli.Options{SecretsEncryptionKey: &key})
	if !errors.Is(err, crypto.ErrInvalidKey) || strings.Contains(err.Error(), key) {
		t.Fatalf("invalid embedded key was not safely rejected: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pre-canceled or invalid key configuration created state")
	}
}
