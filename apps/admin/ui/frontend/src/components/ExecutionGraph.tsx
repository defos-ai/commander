import { useEffect, useMemo, useRef, useState } from 'react';
import { File, Package, Workflow } from 'lucide-react';
import type { GraphNode, OperationGraph } from '../api/graph-types';
import type { OperationTask, OperationTaskPlacement } from '../api/operation-types';
import { useTranslation } from '../i18n';
import { Dialog } from './Dialog';
import { GraphCanvas } from './GraphCanvas';
import { GraphNodeDetails, graphNodeName } from './GraphNodeDetails';
import { GRAPH_NODE_HEIGHT, GRAPH_NODE_WIDTH, layoutGraph } from './graph-layout';
import { TaskStatus, type SessionNavigationTarget } from './OperationDetails';
import '../styles/catalog.css';

const nodeIcons = { task: Workflow, artifact: Package, file: File };

function NodeContents({ node }: { node: GraphNode }) {
  const { t } = useTranslation();
  const Icon = nodeIcons[node.kind];
  return <>
    <span className="graph-node-kind"><Icon size={14} aria-hidden="true" />{t($ => $.operationGraph.categories[node.kind])}</span>
    <strong className="graph-node-name" title={graphNodeName(node)}>{graphNodeName(node)}</strong>
    <span className="graph-node-meta">{node.kind === 'task' ? <><TaskStatus status={node.task.status} /><code title={node.task.id}>{node.task.id.slice(0, 8)}</code></>
      : node.kind === 'file' ? node.file.media_type : node.artifact.action_name}</span>
  </>;
}

export function ExecutionGraph({ graph, tasks, placements, onOpenArtifact, onOpenSession }: {
  graph: OperationGraph;
  tasks?: OperationTask[] | null;
  placements?: OperationTaskPlacement[] | null;
  onOpenArtifact?: (artifactID: string, fileID?: string) => void;
  onOpenSession?: (target: SessionNavigationTarget) => void;
}) {
  const { t } = useTranslation();
  const [selectedID, setSelectedID] = useState<string>();
  const viewport = useRef<HTMLDivElement>(null);
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  const returnFocusID = useRef<string | undefined>(undefined);
  const pendingArtifact = useRef<{ artifactID: string; fileID?: string } | undefined>(undefined);
  const layout = useMemo(() => {
    try { return layoutGraph(graph); } catch { return null; }
  }, [graph]);
  const selected = graph.nodes.find(node => node.id === selectedID);
  const taskData = selected?.kind === 'task' ? tasks?.find(task => task.id === selected.task.id && task.operation_id === graph.operation_id) : undefined;

  useEffect(() => {
    if (selectedID && !selected) {
      returnFocusID.current = selectedID;
      setSelectedID(undefined);
    }
  }, [selectedID, selected]);

  useEffect(() => {
    if (selected) return;
    if (returnFocusID.current) {
      (buttons.current.get(returnFocusID.current) ?? viewport.current)?.focus();
      returnFocusID.current = undefined;
    }
    if (pendingArtifact.current) {
      const artifact = pendingArtifact.current;
      pendingArtifact.current = undefined;
      onOpenArtifact?.(artifact.artifactID, artifact.fileID);
    }
  }, [selected, onOpenArtifact]);

  function clearSelection() {
    returnFocusID.current = selectedID;
    setSelectedID(undefined);
  }

  function openArtifact(artifactID: string, fileID?: string) {
    pendingArtifact.current = { artifactID, fileID };
    clearSelection();
  }

  if (!layout) return <p className="notice" role="alert">{t($ => $.operationGraph.errors.invalid_response)}</p>;
  if (!graph.nodes.length) return <div className="catalog-empty"><Workflow size={26} strokeWidth={1.3} aria-hidden="true" />
    <h2>{t($ => $.operationGraph.empty)}</h2><p>{t($ => $.operationGraph.emptyDescription)}</p>
  </div>;

  return <>
    <GraphCanvas width={layout.width} height={layout.height} label={t($ => $.operationGraph.renderLabel)} viewportRef={viewport}
      edges={layout.edges.map(({ edge, path }) => ({ id: `${edge.kind}:${edge.source}:${edge.target}`, path, selected: edge.source === selectedID || edge.target === selectedID }))}
      legend={<ul className="graph-legend">{(['task', 'artifact', 'file'] as const).map(kind => {
        const Icon = nodeIcons[kind];
        return <li key={kind}><Icon size={14} aria-hidden="true" />{t($ => $.operationGraph.categories[kind])}</li>;
      })}</ul>}>
      {layout.nodes.map(({ node, x, y }) => <button type="button" className="graph-node" key={node.id}
        ref={element => { if (element) buttons.current.set(node.id, element); else buttons.current.delete(node.id); }}
        style={{ left: x, top: y, width: GRAPH_NODE_WIDTH, height: GRAPH_NODE_HEIGHT }} data-kind={node.kind}
        aria-label={t($ => $.operationGraph.selectNode, { kind: t($ => $.operationGraph.categories[node.kind]), name: graphNodeName(node), id: node.id })}
        aria-pressed={node.id === selectedID} aria-haspopup="dialog" onClick={() => setSelectedID(node.id)}>
        <NodeContents node={node} />
      </button>)}
    </GraphCanvas>
    {selected && <Dialog open title={t($ => $.operationGraph.nodeDetails)} onClose={clearSelection} className="graph-node-dialog">
      <GraphNodeDetails graph={graph} node={selected} taskData={taskData}
        placement={taskData ? placements?.find(placement => placement.task_id === taskData.id) : undefined}
        onSelect={setSelectedID} onOpenArtifact={onOpenArtifact ? openArtifact : undefined} onOpenSession={onOpenSession} />
    </Dialog>}
  </>;
}
