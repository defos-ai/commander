export interface Mission {
  name: string;
  objective: string;
  required_actions: string[] | null;
  starting_actions: string[] | null;
  steps?: MissionStep[];
  automations?: Record<string, MissionAutomation>;
  input_schema: Record<string, unknown> | null;
  output_schema: Record<string, unknown> | null;
}

export interface MissionStep {
  id: string;
  objective: string;
  agent_types: string[] | null;
  required_actions: string[] | null;
}

export interface MissionAutomation {
  run?: string[];
  continue?: string;
  decide?: string;
}

export interface Action {
  name: string;
  description: string;
  status: string;
  status_reason?: string;
  required_permissions: string[] | null;
  missing_requirements?: ActionRequirement[] | null;
  input_schema: Record<string, unknown> | null;
  output_schema: Record<string, unknown> | null;
}

export interface ActionRequirement {
  kind: string;
  name: string;
  secret?: boolean;
}

export interface Agent {
  id: string;
  type: string;
  status: string;
  work?: AgentWork | null;
  server: { ip_address: string; hostname: string };
  responsibilities: string[] | null;
  actions: Action[] | null;
  granted_permissions: string[] | null;
  discovery_scope: { paths: string[] | null; services: string[] | null } | null;
  config: unknown;
  observed_at?: string;
  last_seen: string;
  last_heartbeat: string;
  lease_expires_at?: string;
  stale: boolean;
}

export interface AgentWork {
  operation_id?: string;
  task_id?: string;
  step_id?: string;
  action_name: string;
  command_id?: string;
}
