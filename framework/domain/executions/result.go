package executions

import (
	"errors"
	"fmt"

	"commander/framework/artifact"
	"commander/framework/comms/messages"
	"commander/framework/domain"
	actionmodel "commander/framework/domain/actions"
)

func DecodeActionResult(message messages.Message, agentID, commandID string, call actionmodel.ActionCall) (actionmodel.ActionOutput, error) {
	if err := message.Validate(); err != nil {
		return actionmodel.ActionOutput{}, err
	}
	if message.AgentID != agentID || message.CorrelationID != commandID || message.Work != call.Work {
		return actionmodel.ActionOutput{}, errors.New("action result does not match the requested command")
	}
	if message.Type == messages.CommandFailed {
		var detail CommandError
		if err := domain.Decode(message.Payload, &detail); err != nil {
			return actionmodel.ActionOutput{}, err
		}
		if detail.ErrorType == "" || detail.Message == "" {
			return actionmodel.ActionOutput{}, errors.New("command failure must contain an error type and message")
		}
		return actionmodel.ActionOutput{}, &CommandFailure{AgentID: agentID, CommandID: commandID, Detail: detail}
	}
	if message.Type != messages.ActionSucceeded {
		return actionmodel.ActionOutput{}, fmt.Errorf("unexpected action result type %q", message.Type)
	}
	var output actionmodel.ActionOutput
	if err := domain.Decode(message.Payload, &output); err != nil {
		return actionmodel.ActionOutput{}, err
	}
	if output.ActionName != call.ActionName {
		return actionmodel.ActionOutput{}, errors.New("agent replied for a different action")
	}
	_, err := OutputArtifacts(message, output)
	if err != nil {
		return actionmodel.ActionOutput{}, err
	}
	return output, nil
}

func OutputArtifacts(message messages.Message, output actionmodel.ActionOutput) ([]artifact.Artifact, error) {
	refs := append([]artifact.Artifact(nil), output.Artifacts...)
	seen := make(map[string]artifact.Artifact, len(refs))
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		if previous, exists := seen[ref.ID]; exists && !previous.Equal(ref) {
			return nil, errors.New("artifact has conflicting metadata within the result")
		}
		seen[ref.ID] = ref
		if ref.AgentID != message.AgentID || ref.CommandID != message.CorrelationID || ref.ActionName != output.ActionName {
			return nil, errors.New("artifact ownership does not match the agent result")
		}
	}
	return refs, nil
}
