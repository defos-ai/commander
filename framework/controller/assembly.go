package controller

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/controller/workers"
	"github.com/defos-ai/commander/framework/controller/workers/command"
	"github.com/defos-ai/commander/framework/controller/workers/coordination"
	"github.com/defos-ai/commander/framework/controller/workers/event"
	"github.com/defos-ai/commander/framework/controller/workers/operation"
	"github.com/defos-ai/commander/framework/controller/workers/secret"
	"github.com/defos-ai/commander/framework/controller/workers/session"
	"github.com/defos-ai/commander/framework/controller/workers/sharing"
	runtimeSessions "github.com/defos-ai/commander/framework/sessions"
)

func assemble(c *Controller, bus comms.Bus, config ControllerConfig) (result []workers.Worker, err error) {
	// Retain constructed workers for cleanup even when an error returns a nil list.
	var list []workers.Worker
	defer func() {
		if err != nil {
			c.cancel(err)
			c.cancelCore(err)
			err = errors.Join(err, closeWorkers(context.Background(), list))
		}
	}()
	coordinator, err := coordination.New(c.coreLifetime, coordination.Dependencies{
		Bus: bus, Inventory: config.AgentStore, Executions: config.ExecutionStore, Artifacts: config.ArtifactStore,
		HeartbeatTimeout: config.HeartbeatTimeout, SweepInterval: config.LeaseSweepInterval, Stop: c.fail,
	})
	if err != nil {
		return nil, err
	}
	c.coordination = coordinator
	list = append(list, coordinator)
	if config.SessionStore != nil {
		c.sessions, err = session.New(session.Dependencies{
			Store: config.SessionStore, Operations: config.OperationStore, Executions: config.ExecutionStore,
			Agents: config.AgentStore, Actions: config.ActionStore, Remote: coordinator, Logger: config.Logger,
		})
		if err != nil {
			return nil, err
		}
		list = append(list, c.sessions)
	}
	var secretEvents consumers.SecretEvents
	var secrets *secret.Worker
	if config.SecretStore != nil {
		secrets, err = secret.New(secret.Dependencies{
			Bus: bus, Inventory: config.AgentStore, Secrets: config.SecretStore, Requests: config.SecretRequestStore,
			Executions: config.ExecutionStore, Operations: config.OperationStore,
			Decryptor: config.SecretDecryptor, Coordination: coordinator, ReconcileInterval: config.LeaseSweepInterval,
		})
		if err != nil {
			return nil, err
		}
		secretEvents = secrets
		c.secrets = secrets
		list = append(list, secrets)
	}
	c.sharing = sharing.New(c.coreLifetime, sharing.Dependencies{
		Bus: bus, Operations: config.OperationStore, Executions: config.ExecutionStore,
		Artifacts: config.ArtifactStore, Agents: coordinator, Logger: config.Logger,
	})
	list = append(list, c.sharing)
	var sessions event.SessionObserver
	if c.sessions != nil {
		sessions = c.sessions
	}
	c.events, err = event.New(c.coreLifetime, event.Dependencies{
		Bus: bus, ControllerType: config.Type, ControllerID: config.ID, Inventory: config.AgentStore,
		Actions: config.ActionStore, Executions: config.ExecutionStore, Operations: config.OperationStore,
		Artifacts: config.ArtifactStore, Secrets: secretEvents, Sessions: sessions, Sharing: c.sharing,
		Coordination: coordinator, Logger: config.Logger,
	})
	if err != nil {
		return nil, err
	}
	list = append(list, c.events)
	if secrets != nil {
		if err := secrets.InitializeTransient(c.coreLifetime); err != nil {
			return nil, err
		}
		if err := secrets.Reconcile(c.coreLifetime); err != nil {
			return nil, err
		}
	}
	c.coreWorkers = append([]workers.Worker(nil), list...)
	var sessionCoordinator runtimeSessions.Coordinator
	var publisher coordination.SessionPublisher
	if c.sessions != nil {
		sessionCoordinator, publisher = c.sessions, c.sessions
	}
	c.operations, err = operation.New(c.lifetime, coordination.Executor{Worker: coordinator, Sessions: publisher}, operation.Dependencies{
		Sessions: sessionCoordinator, AgentCatalog: coordinator, Sharing: c.sharing,
		MissionStore: config.MissionStore, OperationStore: config.OperationStore, ReportStore: config.ReportStore,
		AgentSelector: config.AgentSelector, TaskTimeout: config.TaskTimeout, Capacity: config.Capacity, Logger: config.Logger,
	})
	if err != nil {
		return nil, err
	}
	list = append(list, c.operations)
	c.commands, err = command.New(c.lifetime, command.Dependencies{
		Bus: bus, ControllerType: config.Type, ControllerID: config.ID,
		Controller: c, Operations: c.operations, Logger: config.Logger,
	})
	if err != nil {
		return nil, err
	}
	list = append(list, c.commands)
	return list, nil
}
