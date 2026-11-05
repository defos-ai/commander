package messages

const (
	AdapterRequest         MessageType = "adapter.request"
	AdapterRequestCanceled MessageType = "adapter.request.canceled"
	ControllerResponse     MessageType = "controller.response"
)

const (
	ControlSession  MessageType = "agent.control_session"
	DiscoverActions MessageType = "agent.discover"
	StopAgent       MessageType = "agent.stop"
	AgentStarted    MessageType = "agent.started"
	AgentHeartbeat  MessageType = "agent.heartbeat"
	AgentUpdated    MessageType = "agent.updated"
	AgentStopped    MessageType = "agent.stopped"
)

const (
	RunAction            MessageType = "action.run"
	ActionResultAccepted MessageType = "action.result.accepted"
	ActionSucceeded      MessageType = "action.succeeded"
	CommandFailed        MessageType = "command.failed"
)

const (
	SecretSyncRequested      MessageType = "secret.sync.requested"
	SecretDelivered          MessageType = "secret.delivered"
	SecretInstalled          MessageType = "secret.installed"
	SecretTransientResponded MessageType = "secret.transient.responded"
	SecretTransientReceived  MessageType = "secret.transient.received"
	SecretRequestCanceled    MessageType = "secret.request.canceled"
)

const (
	CheckpointRequested MessageType = "execution.checkpoint.requested"
	CheckpointResponded MessageType = "execution.checkpoint.responded"
)

const (
	QuestionRequested MessageType = "execution.question.requested"
	QuestionResponded MessageType = "execution.question.responded"
)

const (
	ExecutionLogged MessageType = "execution.logged"
	DataShared      MessageType = "execution.data.shared"
)
