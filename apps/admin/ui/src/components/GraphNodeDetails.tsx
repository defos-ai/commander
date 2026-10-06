import { X } from 'lucide-react';
import type { GraphNode, OperationGraph } from '../api/graph-types';
import { useTranslation } from '../i18n';
import { TaskStatus } from './OperationDetails';
import { Button } from './Button';

export function graphNodeName(node: GraphNode) {
  return node.kind === 'task' ? node.task.action_name : node.kind === 'artifact' ? node.artifact.name || node.artifact.id : node.file.name || node.file.id;
}

function Timestamp({ value }: { value?: string | null }) {
  const { t } = useTranslation();
  const date = value ? new Date(value) : null;
  return date && !Number.isNaN(date.getTime()) && !value?.startsWith('0001-')
    ? <time dateTime={value ?? undefined} title={value ?? undefined}>{date.toLocaleString()}</time>
    : <span className="catalog-muted">{t($ => $.operations.notReported)}</span>;
}

export function GraphNodeDetails({ graph, node, onSelect, onClear, onOpenArtifact }: {
  graph: OperationGraph;
  node?: GraphNode;
  onSelect: (id: string) => void;
  onClear: () => void;
  onOpenArtifact?: (artifactID: string, fileID?: string) => void;
}) {
  const { t } = useTranslation();
  const nodes = new Map(graph.nodes.map(item => [item.id, item]));
  const related = node ? graph.edges.flatMap(edge => {
    const direction = edge.target === node.id ? 'incoming' : edge.source === node.id ? 'outgoing' : undefined;
    if (!direction) return [];
    const other = nodes.get(direction === 'incoming' ? edge.source : edge.target);
    return other ? [{ edge, direction, node: other } as const] : [];
  }) : [];
  return <section className="graph-node-details" aria-label={t($ => $.operationGraph.nodeDetails)}>
    <div className="graph-inspector-heading"><h2>{t($ => $.operationGraph.nodeDetails)}</h2>
      {node && <button type="button" className="icon-button" onClick={onClear} aria-label={t($ => $.operationGraph.clearSelection)}><X size={17} aria-hidden="true" /></button>}
    </div>
    {!node ? <p className="catalog-muted">{t($ => $.operationGraph.noSelection)}</p> : <>
      <p className="eyebrow">{t($ => $.operationGraph.categories[node.kind])}</p><h3>{graphNodeName(node)}</h3>
      {node.kind === 'task' ? <>
        <dl className="detail-list">
          <div><dt>{t($ => $.operations.taskId)}</dt><dd><code>{node.task.id}</code></dd></div>
          <div><dt>{t($ => $.operations.status)}</dt><dd><TaskStatus status={node.task.status} /></dd></div>
          <div><dt>{t($ => $.operations.agentId)}</dt><dd>{node.task.agent_id ? <code>{node.task.agent_id}</code> : t($ => $.operations.unassigned)}</dd></div>
          {node.task.parent_task_id && <div><dt>{t($ => $.operations.parentTaskId)}</dt><dd><code>{node.task.parent_task_id}</code></dd></div>}
          {node.task.step_id && <div><dt>{t($ => $.operations.stepId)}</dt><dd><code>{node.task.step_id}</code></dd></div>}
          {node.task.command_id && <div><dt>{t($ => $.operations.commandId)}</dt><dd><code>{node.task.command_id}</code></dd></div>}
          <div><dt>{t($ => $.operations.startedAt)}</dt><dd><Timestamp value={node.task.started_at} /></dd></div>
          {node.task.finished_at && <div><dt>{t($ => $.operations.finishedAt)}</dt><dd><Timestamp value={node.task.finished_at} /></dd></div>}
        </dl>
        {node.task.status === 'interrupted' && <p className="notice">{t($ => $.operations.interruptedNote)}</p>}
        {node.task.error && <div className="operation-error"><h4>{t($ => $.operations.error)}</h4><p>{node.task.error}</p></div>}
      </> : node.kind === 'artifact' ? <dl className="detail-list">
        <div><dt>{t($ => $.operations.artifactId)}</dt><dd><code>{node.artifact.id}</code></dd></div>
        <div><dt>{t($ => $.operations.action)}</dt><dd>{node.artifact.action_name}</dd></div>
        <div><dt>{t($ => $.operations.agentId)}</dt><dd><code>{node.artifact.agent_id}</code></dd></div>
        {node.artifact.command_id && <div><dt>{t($ => $.operations.commandId)}</dt><dd><code>{node.artifact.command_id}</code></dd></div>}
        <div><dt>{t($ => $.operations.createdAt)}</dt><dd><Timestamp value={node.artifact.created_at} /></dd></div>
      </dl> : <dl className="detail-list">
        <div><dt>{t($ => $.operations.fileId)}</dt><dd><code>{node.file.id}</code></dd></div>
        <div><dt>{t($ => $.operations.artifactId)}</dt><dd><code>{node.file.artifact_id}</code></dd></div>
        <div><dt>{t($ => $.operations.mediaType)}</dt><dd>{node.file.media_type}</dd></div>
        <div><dt>{t($ => $.operations.size)}</dt><dd>{t($ => $.operations.bytes, { size: node.file.size.toLocaleString() })}</dd></div>
        <div><dt>{t($ => $.operations.sha256)}</dt><dd><code>{node.file.sha256}</code></dd></div>
      </dl>}
      {onOpenArtifact && node.kind === 'artifact' && <Button variant="secondary" onClick={() => onOpenArtifact(node.artifact.id)}>{t($ => $.artifacts.viewArtifact)}</Button>}
      {onOpenArtifact && node.kind === 'file' && <Button variant="secondary" onClick={() => onOpenArtifact(node.file.artifact_id, node.file.id)}>{t($ => $.artifacts.viewFile)}</Button>}
      <h4>{t($ => $.operationGraph.connections)}</h4>
      {related.length ? <ul className="graph-relations">{related.map(({ edge, direction, node: other }) => <li key={`${edge.kind}:${edge.source}:${edge.target}`}>
        <button type="button" onClick={() => onSelect(other.id)}>
          <span>{t($ => $.operationGraph.relationships[edge.kind][direction])}</span>{' '}<strong>{graphNodeName(other)}</strong>
        </button>
      </li>)}</ul> : <p className="catalog-muted">{t($ => $.operationGraph.noConnections)}</p>}
    </>}
  </section>;
}
