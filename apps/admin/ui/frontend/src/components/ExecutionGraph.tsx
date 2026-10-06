import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { File, Package, RotateCcw, Workflow, ZoomIn, ZoomOut } from 'lucide-react';
import type { GraphNode, OperationGraph } from '../api/graph-types';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import { Dialog } from './Dialog';
import { GraphNodeDetails, graphNodeName } from './GraphNodeDetails';
import { GRAPH_NODE_HEIGHT, GRAPH_NODE_WIDTH, layoutGraph } from './graph-layout';
import { TaskStatus } from './OperationDetails';
import '../styles/catalog.css';
import '../styles/operation-graph.css';

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

export function ExecutionGraph({ graph, onOpenArtifact }: { graph: OperationGraph; onOpenArtifact?: (artifactID: string, fileID?: string) => void }) {
  const { t } = useTranslation();
  const identity = useId();
  const [selectedID, setSelectedID] = useState<string>();
  const [zoom, setZoom] = useState(1);
  const viewport = useRef<HTMLDivElement>(null);
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  const returnFocusID = useRef<string | undefined>(undefined);
  const pendingArtifact = useRef<{ artifactID: string; fileID?: string } | undefined>(undefined);
  const layout = useMemo(() => {
    try { return layoutGraph(graph); } catch { return null; }
  }, [graph]);
  const selected = graph.nodes.find(node => node.id === selectedID);

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

  function reset() {
    setZoom(1);
    viewport.current?.scrollTo?.({ left: 0, top: 0 });
  }

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

  return <div className="execution-graph">
    <div className="graph-toolbar">
      <ul className="graph-legend">{(['task', 'artifact', 'file'] as const).map(kind => {
        const Icon = nodeIcons[kind];
        return <li key={kind}><Icon size={14} aria-hidden="true" />{t($ => $.operationGraph.categories[kind])}</li>;
      })}</ul>
      <div className="graph-zoom-controls">
        <Button variant="secondary" aria-label={t($ => $.operationGraph.zoomOut)} disabled={zoom <= .5} onClick={() => setZoom(value => Math.max(.5, value - .25))}><ZoomOut size={16} aria-hidden="true" /></Button>
        <output aria-label={t($ => $.operationGraph.zoom)}>{t($ => $.operationGraph.zoomValue, { value: Math.round(zoom * 100) })}</output>
        <Button variant="secondary" aria-label={t($ => $.operationGraph.zoomIn)} disabled={zoom >= 1.5} onClick={() => setZoom(value => Math.min(1.5, value + .25))}><ZoomIn size={16} aria-hidden="true" /></Button>
        <Button variant="secondary" onClick={reset}><RotateCcw size={14} aria-hidden="true" />{t($ => $.operationGraph.reset)}</Button>
      </div>
    </div>
    <p className="graph-navigation-hint" id={`${identity}-hint`}>{t($ => $.operationGraph.navigationHint)}</p>
    <div className="graph-viewport" ref={viewport} tabIndex={0} role="region" aria-label={t($ => $.operationGraph.renderLabel)} aria-describedby={`${identity}-hint`}>
      <div className="graph-scale-frame" style={{ width: layout.width * zoom, height: layout.height * zoom }}>
        <div className="graph-canvas" style={{ width: layout.width, height: layout.height, transform: `scale(${zoom})` }}>
          <svg className="graph-edges" width={layout.width} height={layout.height} aria-hidden="true">
            <defs><marker id={`${identity}-arrow`} viewBox="0 0 8 8" refX="7" refY="4" markerWidth="6" markerHeight="6" orient="auto-start-reverse"><path d="M 0 0 L 8 4 L 0 8 z" fill="context-stroke" /></marker></defs>
            {layout.edges.map(({ edge, path }) => <path key={`${edge.kind}:${edge.source}:${edge.target}`} d={path} className="graph-edge"
              data-selected={edge.source === selectedID || edge.target === selectedID ? 'true' : undefined} markerEnd={`url(#${identity}-arrow)`} />)}
          </svg>
          {layout.nodes.map(({ node, x, y }) => <button type="button" className="graph-node" key={node.id}
            ref={element => { if (element) buttons.current.set(node.id, element); else buttons.current.delete(node.id); }}
            style={{ left: x, top: y, width: GRAPH_NODE_WIDTH, height: GRAPH_NODE_HEIGHT }} data-kind={node.kind}
            aria-label={t($ => $.operationGraph.selectNode, { kind: t($ => $.operationGraph.categories[node.kind]), name: graphNodeName(node), id: node.id })}
            aria-pressed={node.id === selectedID} aria-haspopup="dialog" onClick={() => setSelectedID(node.id)}>
            <NodeContents node={node} />
          </button>)}
        </div>
      </div>
    </div>
    {selected && <Dialog open title={t($ => $.operationGraph.nodeDetails)} onClose={clearSelection} className="graph-node-dialog">
      <GraphNodeDetails graph={graph} node={selected} onSelect={setSelectedID} onOpenArtifact={onOpenArtifact ? openArtifact : undefined} />
    </Dialog>}
  </div>;
}
