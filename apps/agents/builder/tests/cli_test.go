package builder_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	builder "github.com/defos-ai/commander/apps/agents/builder"
	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/apps/agents/builder/cli"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/crypto"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
)

func TestBuilderConstruction(t *testing.T) {
	storage, err := fsartifacts.NewArtifactFileFsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := agent.AgentConfig{
		Type: "ignored", ArtifactFileStore: storage,
		Server: agent.ServerInfo{Hostname: "fixture", IPAddress: "127.0.0.1"},
	}
	var previousID string
	for range 2 {
		instance, err := builder.NewBuilderAgent(config, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		snapshot := instance.Describe()
		if snapshot.Type != builder.BuilderAgentType || !id.ValidAgent(snapshot.ID) || !strings.HasPrefix(snapshot.ID, "builder-agent-") || snapshot.ID == previousID {
			t.Fatalf("incorrect builder identity: %s / %s", snapshot.ID, snapshot.Type)
		}
		previousID = snapshot.ID
		if len(snapshot.Actions) != 1 || snapshot.Actions[0].Name != builderactions.CheckoutTemplateName {
			t.Fatalf("unexpected default catalog: %+v", snapshot.Actions)
		}
	}
	config.Actions = []action.Executor{}
	empty, err := builder.NewBuilderAgent(config, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Describe().Actions) != 0 {
		t.Fatal("explicit empty catalog was replaced with defaults")
	}
	config.Actions = nil
	if _, err := builder.NewBuilderAgent(config, ""); err == nil {
		t.Fatal("default catalog accepted empty workspace configuration")
	}
}

func TestCLIRegistrationAndCancellation(t *testing.T) {
	cipher, err := crypto.NewSecretCipher(strings.Repeat("a1", 32))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, test := range []struct {
		name    string
		empty   bool
		key     string
		options cli.Options
	}{
		{name: "missing git"},
		{name: "empty catalog", empty: true},
		{name: "injected overrides malformed environment", empty: true, key: "malformed-private-key", options: cli.Options{SecretCipher: cipher}},
		{name: "nil uses environment", empty: true, key: strings.Repeat("b2", 32)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SECRETS_ENCRYPTION_KEY", test.key)
			empty := test.empty
			root := t.TempDir()
			bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
			if err != nil {
				t.Fatal(err)
			}
			defer bus.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			subscription, err := bus.Subscribe(ctx, comms.Inbox{
				Topic: messages.ControllerEvents(messages.DefaultControllerType), Name: "builder-fixture", Mode: comms.TransientExclusive,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer subscription.Close()
			args := []string{
				"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"),
				"--workspace-dir=" + filepath.Join(root, "workspaces"), "--search-paths=", "--heartbeat-interval=0",
			}
			if empty {
				args = append(args, "--actions=")
			}
			serving, stop := context.WithCancel(ctx)
			finished := make(chan struct{})
			var runErr error
			go func() { defer close(finished); runErr = cli.RunWithOptions(serving, args, io.Discard, test.options) }()
			defer func() {
				stop()
				select {
				case <-finished:
				case <-time.After(7 * time.Second):
					t.Error("builder host failed to stop")
				}
			}()
			snapshot := readSnapshot(t, ctx, subscription, messages.AgentStarted)
			if snapshot.Type != builder.BuilderAgentType || !strings.HasPrefix(snapshot.ID, "builder-agent-") || !id.ValidAgent(snapshot.ID) || ids[snapshot.ID] {
				t.Fatalf("registration must use a fresh builder identity: %s / %s", snapshot.ID, snapshot.Type)
			}
			ids[snapshot.ID] = true
			if snapshot.Status != agent.AgentReady || snapshot.DiscoveryScope == nil || snapshot.DiscoveryScope.Paths == nil || len(snapshot.DiscoveryScope.Paths) != 0 {
				t.Fatalf("incorrect registration readiness/scope: %+v", snapshot)
			}
			if empty {
				if len(snapshot.Actions) != 0 {
					t.Fatal("empty CLI selection exposed default actions")
				}
			} else {
				if len(snapshot.Actions) != 1 {
					t.Fatalf("incorrect catalog length: %d", len(snapshot.Actions))
				}
				definition := snapshot.Actions[0]
				if definition.Name != builderactions.CheckoutTemplateName || definition.Status != actionmodel.Unavailable || len(definition.MissingRequirements) != 1 || definition.MissingRequirements[0].Kind != actionmodel.ExecutableRequirement || definition.MissingRequirements[0].Name != "git" {
					t.Fatalf("missing git was not advertised: %+v", definition)
				}
			}
			stop()
			select {
			case <-finished:
				if !errors.Is(runErr, context.Canceled) {
					t.Fatalf("unexpected host shutdown result: %v", runErr)
				}
			case <-ctx.Done():
				t.Fatal("host cancellation timed out")
			}
			stopped := readSnapshot(t, ctx, subscription, messages.AgentStopped)
			if stopped.ID != snapshot.ID || stopped.Status != agent.AgentStopped {
				t.Fatalf("incorrect shutdown announcement: %+v", stopped)
			}
		})
	}
}

func readSnapshot(t *testing.T, ctx context.Context, subscription comms.Subscription, kind messages.MessageType) agent.AgentSnapshot {
	t.Helper()
	delivery, err := subscription.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	message := delivery.Message()
	if message.Type != kind {
		t.Fatalf("expected %s, received %s", kind, message.Type)
	}
	var snapshot agent.AgentSnapshot
	if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if err := delivery.Ack(ctx); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestCLIValidatesBeforeOpeningProviders(t *testing.T) {
	t.Setenv("SECRETS_ENCRYPTION_KEY", "")
	for _, test := range []struct{ name, argument, expected string }{
		{"empty workspace", "--workspace-dir=", "workspace-dir"},
		{"unknown action", "--actions=build-agent", "unknown action"},
		{"duplicate action", "--actions=checkout-template,checkout-template", "duplicate"},
		{"negative heartbeat", "--heartbeat-interval=-1s", "heartbeat-interval"},
		{"positional argument", "unexpected", "positional"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "unused")
			args := []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"), "--workspace-dir=" + filepath.Join(root, "workspaces"), test.argument}
			err := cli.Run(t.Context(), args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("invalid configuration was not explained: %v", err)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid configuration created runtime state")
			}
		})
	}
	t.Run("pre-cancelled", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "unused")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := cli.Run(ctx, []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"), "--workspace-dir=" + filepath.Join(root, "workspaces")}, io.Discard)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pre-cancelled startup returned %v", err)
		}
		if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("pre-cancelled startup created runtime state")
		}
	})
}

func TestCLIWorkspaceEnvironment(t *testing.T) {
	t.Setenv("SECRETS_ENCRYPTION_KEY", "")
	t.Setenv("BUILDER_WORKSPACE_DIR", " ")
	root := t.TempDir()
	args := []string{"--bus-url=nats://127.0.0.1:4222", "--storage-url=" + filepath.Join(root, "storage")}
	if err := cli.Run(t.Context(), args, io.Discard); err == nil || !strings.Contains(err.Error(), "workspace-dir") {
		t.Fatalf("workspace environment setting was not validated: %v", err)
	}
	args = append(args, "--workspace-dir="+filepath.Join(root, "workspaces"))
	if err := cli.Run(t.Context(), args, io.Discard); err == nil || !strings.Contains(err.Error(), "unsupported bus scheme") {
		t.Fatalf("explicit workspace flag did not override the environment: %v", err)
	}
}

func TestCLIRejectsUnavailableProviders(t *testing.T) {
	t.Setenv("SECRETS_ENCRYPTION_KEY", "")
	const secret = "provider-test-secret"
	for _, test := range []struct {
		name, flag, url string
		dependency      string
		invalidConfig   bool
		filesystemForm  string
	}{
		{name: "RabbitMQ bus unavailable", flag: "bus-url", url: "amqp://user:" + secret + "@127.0.0.1:1", dependency: "rabbitmq"},
		{name: "RabbitMQ TLS bus unavailable", flag: "bus-url", url: "amqps://user:" + secret + "@127.0.0.1:1", dependency: "rabbitmq"},
		{name: "unsupported broker", flag: "bus-url", url: "nats://user:" + secret + "@127.0.0.1:4222"},
		{name: "S3 URL with credentials", flag: "storage-url", url: "s3://user:" + secret + "@artifacts/bucket", invalidConfig: true},
		{name: "uppercase FILE URL", flag: "bus-url", url: "nats://127.0.0.1:4222", filesystemForm: "FILE"},
		{name: "uppercase FS URL", flag: "bus-url", url: "nats://127.0.0.1:4222", filesystemForm: "FS"},
		{name: "absolute path containing colon", flag: "bus-url", url: "nats://127.0.0.1:4222", filesystemForm: "path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.filesystemForm == "path" && runtime.GOOS == "windows" {
				t.Skip("Windows does not support colons in filenames")
			}
			root := t.TempDir()
			fileURL := func(name string) string {
				if test.filesystemForm == "path" {
					return filepath.Join(root, name+":1")
				}
				scheme := test.filesystemForm
				if scheme == "" {
					scheme = "file"
				}
				return (&url.URL{Scheme: scheme, Path: filepath.Join(root, name)}).String()
			}
			args := []string{
				"--bus-url=" + fileURL("bus"),
				"--storage-url=" + fileURL("storage"),
				"--log-level=info", "--log-format=json",
				"--workspace-dir=" + filepath.Join(root, "workspaces"),
				"--" + test.flag + "=" + test.url,
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			var logs bytes.Buffer

			err := cli.Run(ctx, args, &logs)

			if ctx.Err() != nil {
				t.Fatalf("startup did not report the unavailable dependency before its deadline: %v", err)
			}
			if err == nil {
				t.Fatal("startup accepted an unavailable provider")
			}
			message := strings.ToLower(err.Error())
			if !strings.Contains(message, test.flag) {
				t.Errorf("error does not identify %s: %v", test.flag, err)
			}
			if test.invalidConfig {
				if !strings.Contains(message, "credentials") && !strings.Contains(message, "userinfo") {
					t.Errorf("startup did not explain invalid S3 configuration: %v", err)
				}
			} else if test.dependency != "" {
				if !strings.Contains(message, test.dependency) || !strings.Contains(message, "connect failed") {
					t.Errorf("startup must identify the failed %s connection: %v", test.dependency, err)
				}
				var connectionErr *net.OpError
				if !errors.As(err, &connectionErr) {
					t.Errorf("startup must preserve the underlying connection error: %v", err)
				}
			} else if !strings.Contains(message, "unsupported") {
				t.Errorf("startup did not explain the unavailable provider: %v", err)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(logs.String(), secret) {
				t.Error("startup exposed credentials from the connection URL")
			}
		})
	}
}

func TestCLISecretKeyValidationPrecedesProviderStartup(t *testing.T) {
	const invalidKey = "private-invalid-encryption-key"
	t.Setenv("SECRETS_ENCRYPTION_KEY", invalidKey)
	root := filepath.Join(t.TempDir(), "unused")
	var logs bytes.Buffer
	err := cli.Run(t.Context(), []string{
		"--bus-url=" + filepath.Join(root, "bus"),
		"--storage-url=" + filepath.Join(root, "storage"),
	}, &logs)
	if err == nil || !strings.Contains(err.Error(), "SECRETS_ENCRYPTION_KEY") {
		t.Fatal("invalid encryption key did not fail configuration")
	}
	if strings.Contains(err.Error(), invalidKey) || strings.Contains(logs.String(), invalidKey) {
		t.Fatal("invalid encryption key appeared in diagnostics")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("agent opened a provider before validating the encryption key")
	}
	logs.Reset()
	if err := cli.Run(t.Context(), []string{"--help"}, &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "SECRETS_ENCRYPTION_KEY") || strings.Contains(logs.String(), invalidKey) {
		t.Fatal("help must describe the encryption setting without exposing its value")
	}
}

func TestCLISecretCipherOptions(t *testing.T) {
	const invalidKey = "private-invalid-encryption-key"
	validKey := strings.Repeat("02", 32)
	cipher, err := crypto.NewSecretCipher(validKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		key       string
		cipher    *crypto.SecretCipher
		extraArg  string
		wantError string
		wantCause error
	}{
		{name: "nil cipher keeps invalid environment validation", key: invalidKey, wantError: "SECRETS_ENCRYPTION_KEY", wantCause: crypto.ErrInvalidKey},
		{name: "zero cipher cannot fall back to valid environment", key: validKey, cipher: &crypto.SecretCipher{}, wantError: "secret cipher", wantCause: crypto.ErrMissingKey},
		{name: "injected cipher preserves action validation", key: invalidKey, cipher: cipher, extraArg: "--actions=unknown-action", wantError: "unknown action"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SECRETS_ENCRYPTION_KEY", test.key)
			root := filepath.Join(t.TempDir(), "runtime")
			args := []string{
				"--bus-url=nats://127.0.0.1:4222", "--storage-url=" + filepath.Join(root, "storage"),
				"--log-level=info", "--log-format=json",
				"--workspace-dir=" + filepath.Join(root, "workspaces"),
			}
			if test.extraArg != "" {
				args = append(args, test.extraArg)
			}
			var logs bytes.Buffer
			err := cli.RunWithOptions(t.Context(), args, &logs, cli.Options{SecretCipher: test.cipher})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("expected %s, got %v", test.wantError, err)
			}
			if test.wantCause != nil && !errors.Is(err, test.wantCause) {
				t.Fatalf("cipher validation did not preserve its cause: %v", err)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid configuration created runtime state: %v", err)
			}
			if os.Getenv("SECRETS_ENCRYPTION_KEY") != test.key {
				t.Fatal("host modified the encryption key environment")
			}
			for _, key := range []string{invalidKey, validKey} {
				if strings.Contains(err.Error(), key) || strings.Contains(logs.String(), key) {
					t.Fatal("encryption key appeared in diagnostics")
				}
			}
		})
	}
}

func TestCLISecretCipherOptionsHelpAndCancellation(t *testing.T) {
	t.Setenv("SECRETS_ENCRYPTION_KEY", "invalid-key")
	root := filepath.Join(t.TempDir(), "runtime")
	args := []string{
		"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"),
		"--log-level=info", "--log-format=json",
		"--workspace-dir=" + filepath.Join(root, "workspaces"),
	}
	var help bytes.Buffer
	if err := cli.RunWithOptions(t.Context(), append(args, "--help"), &help, cli.Options{SecretCipher: &crypto.SecretCipher{}}); err != nil {
		t.Fatalf("help validated the cipher: %v", err)
	}
	if !strings.Contains(help.String(), "Usage:") {
		t.Fatal("help did not describe the CLI")
	}
	cipher, err := crypto.NewSecretCipher(strings.Repeat("03", 32))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := cli.RunWithOptions(ctx, args, io.Discard, cli.Options{SecretCipher: cipher}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled startup returned %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("help or cancelled startup created runtime state: %v", err)
	}
}
