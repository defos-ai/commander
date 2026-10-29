export interface Mission {
  name: string;
  objectives: string[];
  required_actions: string[] | null;
  optional_actions?: string[];
  action_definitions?: ActionDefinition[];
  starting_actions: string[] | null;
  rules?: Partial<Record<MissionRuleName, MissionRule>>;
  completion?: { decision_by: 'model' | 'operator' } | null;
  input_schema: Record<string, unknown> | null;
  output_schema: Record<string, unknown> | null;
}

export type MissionRuleName = 'aggregate-results' | 'decide-next-actions' | 'same-agent';

export interface MissionRule {
  for_actions: string[];
  params?: { decision_by?: 'model' };
}

export interface ActionDefinition {
  name: string;
  description: string;
  required_secrets?: string[];
  input_schema: Record<string, unknown>;
  output_schema: Record<string, unknown>;
}

export interface Action extends Omit<ActionDefinition, 'input_schema' | 'output_schema'> {
  status: string;
  status_reason?: string;
  missing_requirements?: ActionRequirement[] | null;
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
