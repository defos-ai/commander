package default_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/controllers/default/cli"
	"github.com/defos-ai/commander/apps/controllers/default/tests/fixtures"
	"github.com/defos-ai/commander/framework/comms"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	fsmissions "github.com/defos-ai/commander/providers/fs/missions"
	fsoperations "github.com/defos-ai/commander/providers/fs/operations"
)

func TestCLITaskTimeoutControlsSavedDispatchDeadline(t *testing.T) {
	for _, test := range []struct {
		name, environment, flag string
		want                    time.Duration
	}{
		{name: "default", want: 15 * time.Minute},
		{name: "environment", environment: "17m", want: 17 * time.Minute},
		{name: "flag overrides environment", environment: "17m", flag: "90s", want: 90 * time.Second},
		{name: "flag overrides invalid environment", environment: "invalid", flag: "90s", want: 90 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ENV", "")
			t.Setenv("TASK_TIMEOUT", test.environment)
			root := t.TempDir()
			database := filepath.Join(root, "database")
			catalog, err := fsmissions.NewMissionFsStore(database)
			if err != nil {
				t.Fatal(err)
			}
			if err := catalog.Upsert(t.Context(), missions.Mission{Name: "timeout-fixture",
				Objectives: []string{"Observe the configured task deadline"}, RequiredActions: []string{"inspect"},
				InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := bus.Close(); err != nil {
					t.Error(err)
				}
			})
			args := fixtures.ControllerArgs(root)
			if test.flag != "" {
				args = append(args, "--task-timeout="+test.flag)
			}
			fixtures.StartController(t, ctx, args)
			const agentID = "timeout-agent-01234567"
			inbox, err := bus.Subscribe(ctx, comms.Inbox{Topic: messages.AgentCommands(agentID), Name: agentID, Mode: comms.TransientExclusive})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := inbox.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := (controllerevents.Client{Bus: bus}).State(ctx, controllerevents.AgentStarted, agents.AgentSnapshot{
				ID: agentID, Type: "timeout-agent", Status: agents.AgentReady,
				Server:  agents.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"},
				Actions: []actions.Action{{Name: "inspect", Status: actions.Ready, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}},
			}, ""); err != nil {
				t.Fatal(err)
			}
			inventory, err := fsagents.NewAgentFsStore(database)
			if err != nil {
				t.Fatal(err)
			}
			fixtures.Await(t, ctx, "ready agent", func() bool {
				record, err := inventory.Get(ctx, agentID)
				return err == nil && record.Status == agents.AgentReady
			})
			adapterID, err := id.AdapterID("timeout-test")
			if err != nil {
				t.Fatal(err)
			}
			client, err := controllercommands.NewClient(ctx, bus, adapterID, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := client.Close(); err != nil {
					t.Error(err)
				}
			})
			store, err := fsoperations.NewOperationFsStore(database)
			if err != nil {
				t.Fatal(err)
			}

			op, err := client.Start(ctx, controllercommands.StartOperationCommand{MissionName: "timeout-fixture", Input: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			queued, err := client.Assign(ctx, controllercommands.AssignTaskCommand{OperationID: op.ID, ActionName: "inspect", Input: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}

			var dispatched operations.Task
			fixtures.Await(t, ctx, "saved dispatch", func() bool {
				var err error
				dispatched, err = store.GetTask(ctx, op.ID, queued.ID)
				return err == nil && dispatched.Status == operations.TaskRunning
			})

			if dispatched.DeadlineAt == nil || dispatched.StartedAt.IsZero() || dispatched.AgentID != agentID ||
				dispatched.DeadlineAt.Sub(dispatched.StartedAt) != test.want {
				t.Fatalf("saved deadline did not use configured timeout %s: %+v", test.want, dispatched)
			}
		})
	}
}

func TestCLIRejectsInvalidTaskTimeoutBeforeOpeningProviders(t *testing.T) {
	for _, test := range []struct {
		name, environment, flag string
		flagProvided            bool
	}{
		{name: "invalid environment", environment: "invalid"},
		{name: "zero environment", environment: "0"},
		{name: "negative environment", environment: "-1s"},
		{name: "invalid flag", flagProvided: true, flag: "invalid"},
		{name: "zero flag", flagProvided: true, flag: "0"},
		{name: "negative flag", flagProvided: true, flag: "-1s"},
		{name: "blank flag overrides environment", environment: "17m", flagProvided: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TASK_TIMEOUT", test.environment)
			root := t.TempDir()
			args := fixtures.ControllerArgs(root)
			if test.flagProvided {
				args = append(args, "--task-timeout="+test.flag)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			ready := false
			err := cli.RunWithOptions(ctx, args, io.Discard, cli.Options{Ready: func() { ready = true; cancel() }})
			if err == nil || !strings.Contains(err.Error(), "task-timeout") || ready {
				t.Fatalf("invalid timeout was not rejected before startup: ready=%t error=%v", ready, err)
			}
			for _, name := range []string{"bus", "storage", "database"} {
				if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
					t.Fatalf("invalid timeout touched the %s provider: %v", name, err)
				}
			}
		})
	}
}

func TestCLIHelpDoesNotValidateTaskTimeoutEnvironment(t *testing.T) {
	t.Setenv("TASK_TIMEOUT", "invalid")
	var output bytes.Buffer
	if err := cli.Run(t.Context(), []string{"--help"}, &output); err != nil || !strings.Contains(output.String(), "task-timeout") {
		t.Fatalf("help required a valid task timeout: output=%s error=%v", output.String(), err)
	}
}
