package models

type TaskPlacementResponse struct {
	AgentType           string                      `json:"agent_type,omitempty"`
	MissingRequirements []ActionRequirementResponse `json:"missing_requirements,omitempty"`
	TaskID              string                      `json:"task_id"`
	OwnerAgentID        string                      `json:"owner_agent_id,omitempty"`
	Reason              string                      `json:"reason,omitempty"`
}
type PreflightResponse struct {
	Tasks   []TaskPlacementResponse   `json:"tasks,omitempty"`
	Ready   bool                      `json:"ready"`
	Version uint64                    `json:"version"`
	Actions []ActionReadinessResponse `json:"actions"`
}

type ActionReadinessResponse struct {
	ActionName        string                   `json:"action_name"`
	ReadyAgentIDs     []string                 `json:"ready_agent_ids,omitempty"`
	UnavailableAgents []AgentReadinessResponse `json:"unavailable_agents,omitempty"`
}

type AgentReadinessResponse struct {
	AgentID             string                      `json:"agent_id"`
	AgentType           string                      `json:"agent_type,omitempty"`
	Reason              string                      `json:"reason"`
	MissingRequirements []ActionRequirementResponse `json:"missing_requirements,omitempty"`
}
