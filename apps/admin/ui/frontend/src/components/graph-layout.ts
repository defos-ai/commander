import type { GraphEdge, GraphNode, OperationGraph } from '../api/graph-types';

export const GRAPH_NODE_WIDTH = 240;
export const GRAPH_NODE_HEIGHT = 112;
const COLUMN_GAP = 80;
const ROW_GAP = 32;
const PADDING = 32;

export interface PositionedGraphNode { node: GraphNode; x: number; y: number; }

export function layoutGraph(graph: OperationGraph) {
  if (!graph.nodes.length) return { nodes: [], edges: [], width: 0, height: 0 };
  const nodes = new Map(graph.nodes.map(node => [node.id, node]));
  if (nodes.size !== graph.nodes.length) throw new Error('Duplicate graph node');
  const parents = new Map(graph.nodes.map(node => [node.id, [] as string[]]));
  const children = new Map(graph.nodes.map(node => [node.id, [] as string[]]));
  for (const edge of graph.edges) {
    if (!nodes.has(edge.source) || !nodes.has(edge.target)) throw new Error('Unknown graph endpoint');
    parents.get(edge.target)!.push(edge.source);
    children.get(edge.source)!.push(edge.target);
  }
  const remaining = new Map([...parents].map(([id, items]) => [id, items.length]));
  const rank = new Map(graph.nodes.map(node => [node.id, 0]));
  const queue = graph.nodes.filter(node => !remaining.get(node.id)).map(node => node.id);
  for (let index = 0; index < queue.length; index++) {
    const id = queue[index];
    for (const child of children.get(id)!) {
      rank.set(child, Math.max(rank.get(child)!, rank.get(id)! + 1));
      const pending = remaining.get(child)! - 1;
      remaining.set(child, pending);
      if (pending === 0) queue.push(child);
    }
  }
  if (queue.length !== nodes.size) throw new Error('Graph contains a cycle');

  const columns: GraphNode[][] = [];
  for (const node of graph.nodes) (columns[rank.get(node.id)!] ??= []).push(node);
  let maxRows = 0;
  for (const column of columns) maxRows = Math.max(maxRows, column.length);
  const contentHeight = maxRows * (GRAPH_NODE_HEIGHT + ROW_GAP) - ROW_GAP;
  const positioned = new Map<string, PositionedGraphNode>();
  const parentCenter = (node: GraphNode) => {
    const ids = parents.get(node.id)!;
    return ids.length ? ids.reduce((sum, id) => sum + positioned.get(id)!.y + GRAPH_NODE_HEIGHT / 2, 0) / ids.length : 0;
  };
  columns.forEach((column, index) => {
    column.sort((a, b) => parentCenter(a) - parentCenter(b));
    const columnHeight = column.length * (GRAPH_NODE_HEIGHT + ROW_GAP) - ROW_GAP;
    column.forEach((node, row) => positioned.set(node.id, {
      node,
      x: PADDING + index * (GRAPH_NODE_WIDTH + COLUMN_GAP),
      y: PADDING + (contentHeight - columnHeight) / 2 + row * (GRAPH_NODE_HEIGHT + ROW_GAP),
    }));
  });

  function edgePath(edge: GraphEdge) {
    const from = positioned.get(edge.source)!;
    const to = positioned.get(edge.target)!;
    const x = from.x + GRAPH_NODE_WIDTH;
    const y = from.y + GRAPH_NODE_HEIGHT / 2;
    const endY = to.y + GRAPH_NODE_HEIGHT / 2;
    if (rank.get(edge.target)! - rank.get(edge.source)! > 1) {
      // Route long edges above the nodes so intermediate columns stay readable.
      return `M ${x} ${y} L ${x + 24} ${y} L ${x + 24} ${PADDING / 2} L ${to.x - 24} ${PADDING / 2} L ${to.x - 24} ${endY} L ${to.x} ${endY}`;
    }
    const middle = (x + to.x) / 2;
    return `M ${x} ${y} C ${middle} ${y}, ${middle} ${endY}, ${to.x} ${endY}`;
  }

  return {
    nodes: [...positioned.values()],
    edges: graph.edges.map(edge => ({ edge, path: edgePath(edge) })),
    width: columns.length * (GRAPH_NODE_WIDTH + COLUMN_GAP) - COLUMN_GAP + 2 * PADDING,
    height: contentHeight + 2 * PADDING,
  };
}
