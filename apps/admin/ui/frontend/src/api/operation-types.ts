import type { ActionRequirement, Mission } from './types';

export interface Operation {
  id: string;
  mission: Mission;
  input: unknown;
  status: string;
  progress: OperationProgress;
  affinity?: OperationAffinity;
  preflight?: OperationPreflight | null;
  tasks: OperationTask[] | null;
  questions: OperationQuestion[] | null;
  output?: unknown;
  report_id?: string;
  completed_by?: 'operator' | 'model';
  error?: string;
  events: OperationEvent[] | null;
  created_at: string;
  updated_at: string;
}

export interface OperationProgress {
  task_counts: Record<string, number>;
  total_tasks: number;
  finished_tasks: number;
  pending_decisions: number;
  pending_questions: number;
  settled: boolean;
}

export interface OperationAffinity {
  for_actions: string[];
  agent_id?: string;
  error?: string;
}

export interface OperationTask {
  session_intent?: OperationSessionIntent | null;
  id: string;
  operation_id: string;
  parent_task_id?: string;
  agent_id: string;
  agent_type?: string;
  missing_requirements?: ActionRequirement[] | null;
  action_name: string;
  command_id?: string;
  deadline_at?: string | null;
  input?: unknown;
  status: string;
  result?: OperationActionOutput | null;
  continuation?: OperationContinuation | null;
  error?: string;
  started_at: string;
  finished_at?: string | null;
}

export interface OperationActionOutput {
  session?: OperationSessionReference | null;
  action_name: string;
  output: unknown;
  artifacts?: OperationArtifact[] | null;
  next_actions?: OperationNextAction[] | null;
}

export interface OperationContinuation {
  version: number;
  decision_by: string;
  status: string;
  proposed_actions: OperationNextAction[] | null;
  created_task_ids: string[] | null;
  question_id?: string;
}

export interface OperationNextAction {
  action_name: string;
  input?: unknown;
  reason?: string;
}

export interface OperationQuestion {
  id: string;
  status: string;
  task_id?: string;
  text: string;
  options?: string[] | null;
  answer?: string;
  answered_by?: 'operator' | 'model';
  asked_at: string;
  answered_at?: string | null;
  fallback_at: string;
}

export interface OperationEvent {
  sequence: number;
  operation_id: string;
  type: string;
  status: string;
  task_id?: string;
  question_id?: string;
  message?: string;
  created_at: string;
}

export interface OperationPreflight {
  ready: boolean;
  version: number;
  actions: OperationActionReadiness[] | null;
  tasks?: OperationTaskPlacement[] | null;
}

export interface OperationSessionIntent {
  mode: string;
  scope_id: string;
  id?: string;
  error?: string;
}

export interface OperationSessionReference {
  id: string;
  scope_id: string;
  owner_agent_id: string;
}

export interface OperationTaskPlacement {
  task_id: string;
  owner_agent_id?: string;
  reason?: string;
  agent_type?: string;
  missing_requirements?: ActionRequirement[] | null;
}

export interface OperationActionReadiness {
  action_name: string;
  ready_agent_ids?: string[] | null;
  unavailable_agents?: OperationAgentReadiness[] | null;
}

export interface OperationAgentReadiness {
  agent_id: string;
  agent_type?: string;
  reason: string;
  missing_requirements?: ActionRequirement[] | null;
}

export interface OperationArtifact {
  id: string;
  name: string;
  agent_id: string;
  action_name: string;
  command_id?: string;
  created_at: string;
  files: OperationArtifactFile[] | null;
}

export interface OperationArtifactFile {
  id: string;
  name: string;
  media_type: string;
  size: number;
  sha256: string;
}
