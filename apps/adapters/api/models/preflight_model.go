package models

type PreflightResponse struct {
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
	MissingSecrets      []string                    `json:"missing_secrets,omitempty"`
}
