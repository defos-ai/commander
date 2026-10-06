export interface OperationGraph {
  operation_id: string;
  status: string;
  updated_at: string;
  nodes: GraphNode[];
  edges: GraphEdge[];
}

export type GraphNode =
  | { id: string; kind: 'task'; task: GraphTask }
  | { id: string; kind: 'artifact'; artifact: GraphArtifact }
  | { id: string; kind: 'file'; file: GraphFile };

export interface GraphEdge {
  source: string;
  target: string;
  kind: 'parent_child' | 'produced' | 'contains';
}

export interface GraphTask {
  id: string;
  parent_task_id?: string;
  step_id?: string;
  action_name: string;
  agent_id?: string;
  command_id?: string;
  status: string;
  error?: string;
  started_at?: string | null;
  finished_at?: string | null;
}

export interface GraphArtifact {
  id: string;
  name: string;
  agent_id: string;
  action_name: string;
  command_id?: string;
  created_at: string;
}

export interface GraphFile {
  artifact_id: string;
  id: string;
  name: string;
  media_type: string;
  size: number;
  sha256: string;
}
