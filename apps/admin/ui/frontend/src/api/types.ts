export interface Mission {
  name: string;
  objectives: string[];
  required_actions: string[] | null;
  optional_actions?: string[];
  starting_actions: string[] | null;
  steps?: MissionStep[];
  rules?: Partial<Record<MissionRuleName, MissionRule>>;
  input_schema: Record<string, unknown> | null;
  output_schema: Record<string, unknown> | null;
}

export interface MissionStep {
  id: string;
  objective: string;
  agent_types: string[] | null;
  required_actions: string[] | null;
}

export type MissionRuleName = 'aggregate-results' | 'decide-next-actions' | 'same-agent';

export interface MissionRule {
  for_actions: string[];
  params?: { decision_by?: 'model' };
}

export interface Action {
  name: string;
  description: string;
  status: string;
  status_reason?: string;
  missing_requirements?: ActionRequirement[] | null;
  required_secrets?: string[];
  input_schema: Record<string, unknown> | null;
  output_schema: Record<string, unknown> | null;
}

export interface ActionRequirement {
  kind: string;
  name: string;
  // Sensitivity of local configuration; managed secrets use kind: 'secret'.
  sensitive?: boolean;
}

export interface Agent {
  id: string;
  type: string;
  status: string;
  work?: AgentWork | null;
  server: { ip_address: string; hostname: string };
  responsibilities: string[] | null;
  actions: Action[] | null;
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
  missing_requirements?: ActionRequirement[] | null;
}

export interface Controller {
  id: string;
  type: string;
  status: string;
  status_reason?: string;
  server: { hostname: string; os: string; arch: string };
  version: string;
  agent_selector: string;
  rule_selector: string;
  started_at: string;
  last_heartbeat: string;
  lease_expires_at: string;
  stale: boolean;
}
