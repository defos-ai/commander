package controller

import (
	"errors"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/controller/workers"
	"github.com/defos-ai/commander/framework/controller/workers/coordination"
	"github.com/defos-ai/commander/framework/controller/workers/event"
	"github.com/defos-ai/commander/framework/controller/workers/secret"
	"github.com/defos-ai/commander/framework/controller/workers/session"
	"github.com/defos-ai/commander/framework/controller/workers/sharing"
)

func assemble(c *Controller, bus comms.Bus, config ControllerConfig) ([]workers.Worker, error) {
	coordination, err := coordination.New(c.lifetime, coordination.Dependencies{
		Bus: bus, Inventory: config.AgentStore, Executions: config.ExecutionStore, Artifacts: config.ArtifactStore,
		HeartbeatTimeout: config.HeartbeatTimeout, SweepInterval: config.LeaseSweepInterval, Stop: c.beginClose,
	})
	if err != nil {
		c.cancel(err)
		return nil, err
	}
	c.coordination = coordination
	list := []workers.Worker{coordination}
	if config.SessionStore != nil {
		c.sessions, err = session.New(session.Dependencies{
			Store: config.SessionStore, Operations: config.OperationStore, Executions: config.ExecutionStore,
			Agents: config.AgentStore, Actions: config.ActionStore, Remote: coordination, Logger: config.Logger,
		})
		if err != nil {
			c.cancel(err)
			return nil, err
		}
		list = append(list, c.sessions)
	}
	var secretEvents consumers.SecretEvents
	var secrets *secret.Worker
	if config.SecretStore != nil {
		secrets, err = secret.New(secret.Dependencies{
			Bus: bus, Inventory: config.AgentStore, Secrets: config.SecretStore, Requests: config.SecretRequestStore,
			Decryptor: config.SecretDecryptor, Coordination: coordination, ReconcileInterval: config.LeaseSweepInterval,
		})
		if err != nil {
			c.cancel(err)
			return nil, err
		}
		secretEvents = secrets
		list = append(list, secrets)
	}
	c.sharing = sharing.New(c.lifetime, sharing.Dependencies{
		Bus: bus, Operations: config.OperationStore, Executions: config.ExecutionStore,
		Artifacts: config.ArtifactStore, Agents: coordination, Logger: config.Logger,
	})
	var sessions event.SessionObserver
	if c.sessions != nil {
		sessions = c.sessions
	}
	c.events, err = event.New(c.lifetime, event.Dependencies{
		Bus: bus, ControllerType: config.Type, ControllerID: config.ID, Inventory: config.AgentStore,
		Actions: config.ActionStore, Executions: config.ExecutionStore, Operations: config.OperationStore,
		Artifacts: config.ArtifactStore, Secrets: secretEvents, Sessions: sessions, Sharing: c.sharing,
		Coordination: coordination, Logger: config.Logger,
	})
	if err != nil {
		c.cancel(err)
		return nil, err
	}
	if secrets != nil {
		if err := secrets.Reconcile(c.lifetime); err != nil {
			c.cancel(err)
			return nil, errors.Join(err, c.events.Close())
		}
	}
	list = append(list, c.sharing, c.events)
	return list, nil
}
