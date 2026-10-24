package messages

// Wire kinds identify serialized commands and events. Typed payloads and
// publishing clients live under comms/clients, organized by recipient.
const (
	AdapterRequest         MessageType = "adapter.request"
	AdapterRequestCanceled MessageType = "adapter.request.canceled"
	ControllerResponse     MessageType = "controller.response"
	ControlSession         MessageType = "agent.control_session"
	RunAction              MessageType = "action.run"
	DiscoverActions        MessageType = "agent.discover"
	StopAgent              MessageType = "agent.stop"
	ActionResultAccepted   MessageType = "action.result.accepted"
	AgentStarted           MessageType = "agent.started"
	AgentHeartbeat         MessageType = "agent.heartbeat"
	AgentUpdated           MessageType = "agent.updated"
	AgentStopped           MessageType = "agent.stopped"
	ActionSucceeded        MessageType = "action.succeeded"
	CommandFailed          MessageType = "command.failed"
	SecretSyncRequested    MessageType = "secret.sync.requested"
	SecretDelivered        MessageType = "secret.delivered"
	SecretInstalled        MessageType = "secret.installed"
	CheckpointRequested    MessageType = "execution.checkpoint.requested"
	CheckpointResponded    MessageType = "execution.checkpoint.responded"
)
