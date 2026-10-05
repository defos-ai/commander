import { describe, expect, it } from 'vitest';
import type { GraphNode, OperationGraph } from '../src/api/graph-types';
import { GRAPH_NODE_HEIGHT, GRAPH_NODE_WIDTH, layoutGraph } from '../src/components/graph-layout';

function task(id: string): GraphNode { return { id, kind: 'task', task: { id, action_name: 'check-page', status: 'running' } }; }
const base = { operation_id: 'operation-1', status: 'running', updated_at: '2026-10-05T10:00:00Z' };

describe('execution graph layout', () => {
  it('places shared results after every producer without overlapping nodes', () => {
    const graph: OperationGraph = { ...base, nodes: [task('root'), task('branch-a'), task('branch-b'), task('shared'), task('file')], edges: [
      { source: 'root', target: 'branch-a', kind: 'parent_child' },
      { source: 'root', target: 'branch-b', kind: 'parent_child' },
      { source: 'root', target: 'shared', kind: 'produced' },
      { source: 'branch-b', target: 'shared', kind: 'produced' },
      { source: 'shared', target: 'file', kind: 'contains' },
    ] };

    const layout = layoutGraph(graph);

    const nodes = new Map(layout.nodes.map(node => [node.node.id, node]));
    for (const edge of graph.edges) expect(nodes.get(edge.target)!.x).toBeGreaterThan(nodes.get(edge.source)!.x + GRAPH_NODE_WIDTH);
    for (const [index, a] of layout.nodes.entries()) for (const b of layout.nodes.slice(index + 1)) {
      expect(a.x !== b.x || Math.abs(a.y - b.y) >= GRAPH_NODE_HEIGHT).toBe(true);
    }
    for (const { x, y } of layout.nodes) {
      expect(x).toBeGreaterThanOrEqual(0); expect(y).toBeGreaterThanOrEqual(0);
      expect(x + GRAPH_NODE_WIDTH).toBeLessThanOrEqual(layout.width);
      expect(y + GRAPH_NODE_HEIGHT).toBeLessThanOrEqual(layout.height);
    }
    expect(layout.edges.find(item => item.edge.source === 'root' && item.edge.target === 'shared')!.path).toContain(' L ');
    expect(layoutGraph(graph)).toEqual(layout);
  });

  it('handles empty work and a long chain without recursion', () => {
    expect(layoutGraph({ ...base, nodes: [], edges: [] })).toEqual({ nodes: [], edges: [], width: 0, height: 0 });
    const nodes = Array.from({ length: 2000 }, (_, index) => task(String(index)));
    const graph: OperationGraph = { ...base, nodes, edges: nodes.slice(1).map((node, index) => ({ source: String(index), target: node.id, kind: 'parent_child' })) };

    const layout = layoutGraph(graph);

    expect(layout.nodes).toHaveLength(2000);
    expect(layout.nodes.at(-1)!.x).toBeGreaterThan(layout.nodes[0].x);
    expect(Number.isFinite(layout.width)).toBe(true);
  });

  it('rejects cycles and missing endpoints instead of producing an incomplete diagram', () => {
    const nodes = [task('one'), task('two')];
    expect(() => layoutGraph({ ...base, nodes, edges: [
      { source: 'one', target: 'two', kind: 'parent_child' }, { source: 'two', target: 'one', kind: 'parent_child' },
    ] })).toThrow('cycle');
    expect(() => layoutGraph({ ...base, nodes, edges: [{ source: 'one', target: 'missing', kind: 'parent_child' }] })).toThrow('endpoint');
  });
});
