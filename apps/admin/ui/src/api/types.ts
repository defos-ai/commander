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
