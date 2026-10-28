package default_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/controllers/default/cli"
	"github.com/defos-ai/commander/apps/controllers/default/tests/fixtures"
	"github.com/defos-ai/commander/framework/comms"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	fssecrets "github.com/defos-ai/commander/providers/fs/secrets"
)

func TestCLIAuthenticatesAndTransfersSavedEncryptedSecrets(t *testing.T) {
	t.Setenv("SECRETS_ENCRYPTION_KEY", fixtures.SecretEncryptionKey)
	t.Setenv("ENV", "")
	root := t.TempDir()
	database := filepath.Join(root, "database")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	const agentID = "fixture-agent-01234567"
	inbox, err := bus.Subscribe(ctx, comms.Inbox{Topic: messages.AgentEvents(agentID), Name: agentID, Mode: comms.TransientExclusive})
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()
	values, err := fssecrets.NewSecretFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	cipher := fixtures.SecretCipher(t)
	now := time.Now().UTC()
	key := secrets.Key{Name: "API_TOKEN"}
	secret, err := cipher.Encrypt(secrets.Metadata{ID: id.New(), Key: key, Version: 1, CreatedAt: now, UpdatedAt: now}, []byte("local-only-fixture-token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := values.Create(ctx, secret); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- cli.RunWithOptions(ctx, []string{
			"--bus-url=" + filepath.Join(root, "bus"),
			"--storage-url=" + filepath.Join(root, "storage"),
			"--database-url=" + database,
		}, io.Discard, cli.Options{Ready: func() { close(ready) }})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("controller shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("controller did not become ready")
	}
	client := controllerevents.Client{Bus: bus}
	if err := client.State(ctx, controllerevents.AgentStarted, agents.AgentSnapshot{
		ID: agentID, Type: "fixture", Status: agents.AgentReady,
		Server: agents.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"},
		Actions: []actions.Action{{Name: "inspect", Status: actions.Unavailable,
			RequiredSecrets: []string{key.Name}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}},
	}, ""); err != nil {
		t.Fatal(err)
	}
	inventory, err := fsagents.NewAgentFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := inventory.Get(ctx, agentID); err == nil {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("controller did not observe the agent")
		}
	}
	request := secrets.Request{ID: id.New(), Key: key, AgentID: agentID, AgentType: "fixture", Status: secrets.Pending, RequestedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Minute)}
	message, err := controllerevents.NewSecretSyncRequested(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Publish(ctx, message); err != nil {
		t.Fatal(err)
	}
	delivery, err := inbox.Next(ctx)
	if err != nil {
		t.Fatalf("controller did not deliver the saved encrypted secret: %v", err)
	}
	if err := agentevents.ValidateSecretDeliveryRequest(delivery.Message(), request); err != nil {
		t.Fatal(err)
	}
	payload, err := agentevents.DecodeSecretDelivery(delivery.Message())
	if err != nil || payload.Secret.Metadata.ID != secret.Metadata.ID || !bytes.Equal(payload.Secret.Value.Ciphertext, secret.Value.Ciphertext) {
		t.Fatal("controller did not forward the saved ciphertext unchanged")
	}
	requests, err := fssecrets.NewRequestFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	record, err := requests.Get(ctx, request.ID)
	if err != nil || record.Delivery == nil || record.Delivery.ID != delivery.Message().ID || record.Request.Status != secrets.Pending {
		t.Fatal("delivery was not durably reserved before publishing")
	}
	if err := delivery.Ack(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCLIRejectsInvalidSecretCipherBeforeOpeningProviders(t *testing.T) {
	for _, injected := range []bool{false, true} {
		name := "environment"
		if injected {
			name = "injected cipher"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("ENV", "")
			t.Setenv("SECRETS_ENCRYPTION_KEY", "private-invalid-key")
			options, want := cli.Options{}, crypto.ErrInvalidKey
			if injected {
				t.Setenv("SECRETS_ENCRYPTION_KEY", fixtures.SecretEncryptionKey)
				options.SecretCipher, want = &crypto.SecretCipher{}, crypto.ErrMissingKey
			}
			root := t.TempDir()
			var logs bytes.Buffer

			err := cli.RunWithOptions(t.Context(), fixtures.ControllerArgs(root), &logs, options)

			if !errors.Is(err, want) || strings.Contains(err.Error(), "private-invalid-key") ||
				strings.Contains(logs.String(), "private-invalid-key") || strings.Contains(logs.String(), fixtures.SecretEncryptionKey) {
				t.Fatal("invalid cipher was ignored or exposed key material")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid cipher created provider or registration state")
			}
		})
	}
}

func TestCLIInjectedSecretCipherOverridesEnvironment(t *testing.T) {
	t.Setenv("ENV", "")
	t.Setenv("SECRETS_ENCRYPTION_KEY", "private-invalid-key")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	root := t.TempDir()
	fixtures.PendingSecret(t, root)
	ready := false

	err := cli.RunWithOptions(ctx, fixtures.ControllerArgs(root), io.Discard, cli.Options{
		SecretCipher: fixtures.SecretCipher(t), Ready: func() { ready = true; cancel() },
	})

	if err != nil || !ready {
		t.Fatal("valid injected cipher did not override the environment and deliver the pending secret")
	}
}

func TestCLIWrongSecretKeyStopsPendingDelivery(t *testing.T) {
	t.Setenv("ENV", "")
	t.Setenv("SECRETS_ENCRYPTION_KEY", strings.Repeat("ab", 32))
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	root := t.TempDir()
	fixtures.PendingSecret(t, root)
	ready := false

	err := cli.RunWithOptions(ctx, fixtures.ControllerArgs(root), io.Discard, cli.Options{
		Ready: func() { ready = true; cancel() },
	})

	if ready || err == nil || !strings.Contains(err.Error(), "cannot authenticate secret for delivery") {
		t.Fatal("wrong encryption key did not stop pending delivery before readiness")
	}
}
