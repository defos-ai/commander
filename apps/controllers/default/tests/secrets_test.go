package default_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/controllers/default/cli"
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

func TestCLITransfersSavedEncryptedSecretsWithoutEncryptionKey(t *testing.T) {
	// The controller only handles ciphertext; it must not parse or require this key.
	t.Setenv("SECRETS_ENCRYPTION_KEY", "controller-does-not-decrypt")
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
	cipher, err := crypto.NewSecretCipher("014ac583269fe047014ac583269fe047014ac583269fe047014ac583269fe047")
	if err != nil {
		t.Fatal(err)
	}
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
