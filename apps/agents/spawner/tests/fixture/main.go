package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/crypto"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	fsprovider "github.com/defos-ai/commander/providers/fs"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	busPath, err := fsprovider.FilesystemPath(os.Getenv("BUS_URL"))
	if err != nil {
		return err
	}
	storagePath, err := fsprovider.FilesystemPath(os.Getenv("STORAGE_URL"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(storagePath, 0700); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	mode := filepath.Base(executable)
	agentType := "fixture-agent"
	if strings.Contains(mode, "-builder") {
		agentType = "builder-agent"
	}
	suffix := os.Getenv("COMMANDER_AGENT_ID_SUFFIX")
	agentID, err := id.AgentIDFromSuffix(agentType, suffix)
	if err != nil {
		return err
	}
	privateEnvironment := false
	for _, key := range []string{"SPAWNER_PARENT_SECRET", "API_PASSWORD", "MCP_TOKEN"} {
		privateEnvironment = privateEnvironment || os.Getenv(key) != ""
	}
	challenge, err := secretChallenge()
	if err != nil {
		return err
	}
	report, err := json.Marshal(map[string]any{
		"agent_id": agentID, "agent_id_suffix": suffix, "pid": os.Getpid(), "inherited_secret": privateEnvironment,
		"secret_challenge": challenge,
		"region":           os.Getenv("AWS_REGION"), "home": os.Getenv("HOME"),
		"chromium_path": os.Getenv("CHROMIUM_PATH"), "chromium_no_sandbox": os.Getenv("CHROMIUM_NO_SANDBOX"),
	})
	if err != nil {
		return err
	}
	reportPath := filepath.Join(storagePath, agentID+".starting")
	if err := os.WriteFile(reportPath, report, 0600); err != nil {
		return err
	}
	if err := os.Rename(reportPath, filepath.Join(storagePath, agentID+".started")); err != nil {
		return err
	}
	defer os.WriteFile(filepath.Join(storagePath, agentID+".stopped"), nil, 0600)
	bus, err := fscomms.NewFsBus(busPath)
	if err != nil {
		return err
	}
	defer bus.Close()
	if strings.Contains(mode, "delayed") {
		if err := waitForRegistrationGate(ctx); err != nil {
			return err
		}
	}
	if strings.Contains(mode, "silent") {
		<-ctx.Done()
		return nil
	}
	if strings.Contains(mode, "wrong-type") || strings.Contains(mode, "wrong-id") || strings.Contains(mode, "invalid-schema") {
		snapshot := agent.AgentSnapshot{
			ID: agentID, Type: "fixture-agent", Status: agent.AgentReady,
			Server: agent.ServerInfo{Hostname: "fixture", IPAddress: "127.0.0.1"},
		}
		if strings.Contains(mode, "invalid-schema") {
			snapshot.Actions = []actionmodel.Action{{
				Name: "fixture-action", Status: actionmodel.Ready,
				InputSchema: json.RawMessage(`[]`), OutputSchema: json.RawMessage(`{}`),
			}}
		} else if strings.Contains(mode, "wrong-type") {
			snapshot.Type = "different-agent"
		} else {
			snapshot.ID = "fixture-agent-12345678"
			if snapshot.ID == agentID {
				snapshot.ID = "fixture-agent-87654321"
			}
		}
		message, err := messages.NewMessage(messages.AgentStarted, snapshot.ID, snapshot)
		if err != nil {
			return err
		}
		if err := bus.Publish(ctx, messages.ControllerEvents(messages.DefaultControllerType), message); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}
	store, err := fsartifacts.NewArtifactFileFsStore(storagePath)
	if err != nil {
		return err
	}
	executor, err := action.NewFunc(action.FuncConfig[struct{}, struct{}]{
		Action: actionmodel.Action{Name: "fixture-action"},
		Run:    func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil },
	})
	if err != nil {
		return err
	}
	interval := time.Duration(0)
	instance, err := agent.NewAgent(agent.AgentConfig{
		ID: agentID, Type: agent.AgentType(agentType), Actions: []action.Executor{executor},
		ArtifactFileStore: store, HeartbeatInterval: &interval,
		Server: agent.ServerInfo{Hostname: "fixture", IPAddress: "127.0.0.1"},
	})
	if err != nil {
		return err
	}
	_ = instance.Serve(ctx, bus, nil)
	return nil
}

// Only ciphertext crosses back to the parent; the key stays in this process.
func secretChallenge() (*secrets.Secret, error) {
	key := os.Getenv("SECRETS_ENCRYPTION_KEY")
	if key == "" {
		return nil, nil
	}
	cipher, err := crypto.NewSecretCipher(key)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	encrypted, err := cipher.Encrypt(secrets.Metadata{
		ID: id.New(), Key: secrets.Key{Name: "BOOTSTRAP_CHECK"},
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}, []byte("fixture secret bootstrap"))
	return &encrypted, err
}

func waitForRegistrationGate(ctx context.Context) error {
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		_, err := os.Stat("register")
		if err == nil {
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
