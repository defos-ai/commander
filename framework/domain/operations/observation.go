package operations

import (
	"errors"

	"commander/framework/artifact"
	actionmodel "commander/framework/domain/actions"
	"commander/framework/id"
)

type AgentObservation struct {
	AgentID string                   `json:"agent_id"`
	Result  actionmodel.ActionOutput `json:"result"`
}

func (o AgentObservation) Validate() error {
	if !id.ValidAgent(o.AgentID) {
		return errors.New("invalid observation agent ID")
	}
	if err := o.Result.Validate(); err != nil {
		return err
	}
	refs := append([]artifact.Ref(nil), o.Result.Artifacts...)
	if o.Result.OutputArtifact != nil {
		refs = append(refs, *o.Result.OutputArtifact)
	}
	for _, ref := range refs {
		if ref.AgentID != o.AgentID || ref.ActionName != o.Result.ActionName {
			return errors.New("observation artifact ownership does not match its agent and action")
		}
	}
	return nil
}
