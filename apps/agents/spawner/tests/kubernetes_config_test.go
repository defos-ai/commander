package packages_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/cli"
)

func kubernetesCLIEnvironment(t *testing.T) {
	t.Helper()
	for name, value := range map[string]string{
		"COMMANDER_AGENT_ID_SUFFIX": "", "SECRETS_ENCRYPTION_KEY": "", "SPAWNER_RUNTIME": "kubernetes",
		"SPAWNER_KUBERNETES_NAMESPACE": "agents", "SPAWNER_KUBERNETES_POD_PREFIX": "",
		"SPAWNER_KUBERNETES_SERVICE_ACCOUNT": "agent-worker", "SPAWNER_KUBERNETES_CONFIG_MAP": "agent-config",
		"SPAWNER_KUBERNETES_SECRET": "agent-credentials", "SPAWNER_KUBERNETES_IMAGE_PULL_SECRETS": "",
	} {
		t.Setenv(name, value)
	}
}

func TestSpawnerCLIKubernetesValidatesBeforeStartup(t *testing.T) {
	for _, test := range []struct {
		name, setting, value, bus, storage, timeout, expected string
	}{
		{name: "missing namespace", setting: "NAMESPACE", expected: "SPAWNER_KUBERNETES_NAMESPACE"},
		{name: "invalid namespace", setting: "NAMESPACE", value: "Agents", expected: "Kubernetes"},
		{name: "invalid literal prefix", setting: "POD_PREFIX", value: "private-sentinel/", expected: "Kubernetes"},
		{name: "missing Secret reference", setting: "SECRET", expected: "SPAWNER_KUBERNETES_SECRET"},
		{name: "cross-namespace reference", setting: "CONFIG_MAP", value: "other/private-sentinel", expected: "Kubernetes"},
		{name: "duplicate pull reference", setting: "IMAGE_PULL_SECRETS", value: "private-sentinel, private-sentinel", expected: "IMAGE_PULL_SECRETS"},
		{name: "invalid pull reference", setting: "IMAGE_PULL_SECRETS", value: "other/private-sentinel", expected: "Kubernetes"},
		{name: "filesystem bus", bus: "./state/bus", expected: "amqp or amqps"},
		{name: "filesystem storage", storage: "./state/storage", expected: "s3 storage-url"},
		{name: "malformed storage credentials", storage: "s3://key:private-sentinel/raw%zz@artifacts", expected: "storage-url"},
		{name: "invalid explicit timeout", timeout: "0s", expected: "registration-timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			kubernetesCLIEnvironment(t)
			if test.setting != "" {
				t.Setenv("SPAWNER_KUBERNETES_"+test.setting, test.value)
			}
			bus, storage := "amqp://example.invalid/", "s3://artifacts"
			if test.bus != "" {
				bus = test.bus
			}
			if test.storage != "" {
				storage = test.storage
			}
			args := []string{"--bus-url=" + bus, "--storage-url=" + storage}
			if test.timeout != "" {
				args = append(args, "--registration-timeout="+test.timeout)
			}
			root := filepath.Join(t.TempDir(), "unused")
			t.Setenv("SPAWNER_STATE_DIR", root)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			err := cli.Run(ctx, args, io.Discard)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.expected)) || strings.Contains(err.Error(), "private-sentinel") {
				t.Fatalf("invalid configuration was not safely rejected before startup: %v", err)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid Kubernetes configuration created local state")
			}
		})
	}
}

func TestSpawnerCLIKubernetesAcceptsLiteralPrefixesAndRawS3Credentials(t *testing.T) {
	for _, prefix := range []string{"", "team-", "team."} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			kubernetesCLIEnvironment(t)
			t.Setenv("SPAWNER_KUBERNETES_POD_PREFIX", prefix)
			t.Setenv("SPAWNER_KUBERNETES_IMAGE_PULL_SECRETS", " first.registry , second-registry ")
			t.Setenv("SPAWNER_DOCKER_NETWORK", "invalid/ignored")
			t.Setenv("SPAWNER_STATE_DIR", " ")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			args := []string{"--bus-url=amqps://example.invalid/", "--storage-url=s3://key:raw/secret+=@artifacts/prefix"}
			if prefix != "" {
				args = append(args, "--registration-timeout=5s")
			}
			err := cli.Run(ctx, args, io.Discard)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("valid Kubernetes settings were rejected: %v", err)
			}
		})
	}
}

func TestSpawnerCLINativeAndDockerIgnoreKubernetesSettings(t *testing.T) {
	for _, mode := range []string{"native", "docker", "embedded-native"} {
		t.Run(mode, func(t *testing.T) {
			kubernetesCLIEnvironment(t)
			for _, suffix := range []string{"NAMESPACE", "POD_PREFIX", "SERVICE_ACCOUNT", "CONFIG_MAP", "SECRET", "IMAGE_PULL_SECRETS"} {
				t.Setenv("SPAWNER_KUBERNETES_"+suffix, "private-sentinel/invalid")
			}
			t.Setenv("SPAWNER_RUNTIME", mode)
			t.Setenv("SPAWNER_DOCKER_NETWORK", "commander_default")
			t.Setenv("SPAWNER_STATE_DIR", filepath.Join(t.TempDir(), "unused"))
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			args := []string{"--bus-url=amqp://example.invalid/", "--storage-url=s3://artifacts"}
			var err error
			if mode == "embedded-native" {
				t.Setenv("SPAWNER_RUNTIME", "kubernetes")
				err = cli.RunWithOptions(ctx, args, io.Discard, cli.Options{Runtime: "native"})
			} else {
				err = cli.Run(ctx, args, io.Discard)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("unselected Kubernetes settings affected %s: %v", mode, err)
			}
		})
	}
}

func TestSpawnerCLIKubernetesMissingAuthenticationPrecedesProviders(t *testing.T) {
	kubernetesCLIEnvironment(t)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	root := filepath.Join(t.TempDir(), "unused")
	t.Setenv("SPAWNER_STATE_DIR", root)
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(root, "aws-config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(root, "aws-credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err := cli.Run(ctx, []string{"--bus-url=amqp://example.invalid/", "--storage-url=s3://key:private-sentinel/raw+=@artifacts"}, io.Discard)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "in-cluster authentication") || strings.Contains(err.Error(), "private-sentinel") || strings.Contains(err.Error(), "bus-url") || strings.Contains(err.Error(), "storage-url") {
		t.Fatalf("missing in-cluster endpoint was not rejected before providers: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing in-cluster authentication created local state")
	}
}
