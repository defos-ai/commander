import { useEffect, useMemo, useRef, useState } from 'react';
import { Brain, GitBranch, Workflow } from 'lucide-react';
import type { Mission } from '../api/types';
import { useTranslation } from '../i18n';
import { Dialog } from './Dialog';
import { GraphCanvas } from './GraphCanvas';
import { GRAPH_NODE_HEIGHT, GRAPH_NODE_WIDTH, layoutGraph } from './graph-layout';
import '../styles/mission-rules-graph.css';

type Stage = 'action' | 'decision' | 'children';

function missionRulesGraph(mission: Mission) {
  const rules = mission.rules ?? {};
  const starting = new Set(mission.starting_actions ?? []);
  const names = new Set([...starting, ...(mission.required_actions ?? []), ...Object.values(rules).flatMap(rule => rule.actions)]);
  const actions = [...names].sort((a, b) => Number(starting.has(b)) - Number(starting.has(a)) || a.localeCompare(b))
    .map(name => ({ name, starting: starting.has(name),
      aggregate: rules['aggregate-results']?.actions.includes(name) ?? false,
      model: rules['decide-next-actions']?.actions.includes(name) ?? false,
    }));
  const stages: Stage[] = ['action', 'decision', 'children'];
  const nodes = actions.flatMap(action => stages.map(stage => ({ id: JSON.stringify([action.name, stage]), stage, action })));
  const edges = actions.flatMap(action => stages.slice(1).map((stage, index) => ({
    source: JSON.stringify([action.name, stages[index]]), target: JSON.stringify([action.name, stage]), action: action.name,
  })));
  return { actions, nodes, edges };
}

export function MissionRulesGraph({ mission }: { mission: Mission }) {
  const { t } = useTranslation();
  const graph = useMemo(() => missionRulesGraph(mission), [mission]);
  const layout = useMemo(() => layoutGraph(graph), [graph]);
  const [selectedName, setSelectedName] = useState<string>();
  const selected = graph.actions.find(action => action.name === selectedName);
  const viewport = useRef<HTMLDivElement>(null);
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  const returnFocusName = useRef<string | undefined>(undefined);
  const heading = useRef<HTMLHeadingElement>(null);

  useEffect(() => { if (selected) heading.current?.focus(); }, [selected?.name]);

  useEffect(() => {
    if (selectedName && !selected) {
      returnFocusName.current = selectedName;
      setSelectedName(undefined);
    }
  }, [selectedName, selected]);

  useEffect(() => {
    if (!selected && returnFocusName.current) {
      (buttons.current.get(returnFocusName.current) ?? viewport.current)?.focus();
      returnFocusName.current = undefined;
    }
  }, [selected]);

  function closeDetails() {
    returnFocusName.current = selectedName;
    setSelectedName(undefined);
  }

  if (!graph.actions.length) return <p className="mission-empty-rules">{t($ => $.missions.noRules)}</p>;

  return <div className="mission-rules-graph">
    <GraphCanvas width={layout.width} height={layout.height} label={t($ => $.missionRulesGraph.renderLabel)} viewportRef={viewport}
      legend={<ul className="graph-legend">
        <li><Workflow size={14} aria-hidden="true" />{t($ => $.missionRulesGraph.action)}</li>
        <li><Brain size={14} aria-hidden="true" />{t($ => $.missionRulesGraph.decision)}</li>
        <li><GitBranch size={14} aria-hidden="true" />{t($ => $.missionRulesGraph.children)}</li>
      </ul>}
      edges={layout.edges.map(({ edge, path }) => ({ id: JSON.stringify([edge.source, edge.target]), path, selected: edge.action === selectedName }))}>
      {layout.nodes.map(({ node, x, y }) => {
        const { action, stage } = node;
        const style = { left: x, top: y, width: GRAPH_NODE_WIDTH, height: GRAPH_NODE_HEIGHT };
        const decision = action.model ? t($ => $.missions.modelReview) : t($ => $.missionRulesGraph.runProposals);
        const children = action.aggregate ? t($ => $.missionRulesGraph.waitAll) : t($ => $.missionRulesGraph.eachChild);
        if (stage !== 'action') return <div className="graph-node graph-rule-stage" key={node.id} style={style}
          aria-label={t($ => $.missionRulesGraph.stageFor, { name: action.name, stage: stage === 'decision' ? decision : children })}>
          <span className="graph-node-kind">{stage === 'decision' ? <><Brain size={14} aria-hidden="true" />{t($ => $.missionRulesGraph.decision)}</>
            : <><GitBranch size={14} aria-hidden="true" />{t($ => $.missionRulesGraph.children)}</>}</span>
          <strong className="graph-node-name">{stage === 'decision' ? decision : children}</strong>
          <span className="graph-node-meta">{stage === 'decision' ? t($ => $.missionRulesGraph.afterAction) : t($ => $.missionRulesGraph.ifGenerated)}</span>
        </div>;
        return <button type="button" className="graph-node" key={node.id}
          ref={element => { if (element) buttons.current.set(action.name, element); else buttons.current.delete(action.name); }}
          style={style} data-starting={action.starting || undefined}
          aria-label={t($ => $.missionRulesGraph.selectNode, { name: action.name })} aria-haspopup="dialog"
          aria-pressed={action.name === selectedName} onClick={() => setSelectedName(action.name)}>
          <span className="graph-node-kind"><Workflow size={14} aria-hidden="true" />{action.starting ? t($ => $.missionRulesGraph.startingAction) : t($ => $.missionRulesGraph.action)}</span>
          <strong className="graph-node-name" title={action.name}>{action.name}</strong>
          <span className="graph-node-meta">{decision}</span>
        </button>;
      })}
    </GraphCanvas>
    <p className="mission-muted">{t($ => $.missionRulesGraph.fallback)}</p>
    {selected && <Dialog open title={t($ => $.missionRulesGraph.nodeDetails)} onClose={closeDetails} className="graph-node-dialog">
      <div className="graph-node-details">
        <h3 ref={heading} tabIndex={-1}>{selected.name}</h3>
        {selected.starting && <p className="mission-muted">{t($ => $.missionRulesGraph.startingAction)}</p>}
        <h4>{selected.model ? t($ => $.missions.modelReview) : t($ => $.missions.run)}</h4>
        <p>{selected.model ? t($ => $.missionRulesGraph.reviewDescription) : t($ => $.missionRulesGraph.runDescription)}</p>
        <h4>{t($ => $.missionRulesGraph.children)}</h4>
        <p>{selected.aggregate ? t($ => $.missionRulesGraph.aggregateDescription) : t($ => $.missionRulesGraph.independentDescription)}</p>
        <p className="mission-muted">{t($ => $.missionRulesGraph.fallback)}</p>
      </div>
    </Dialog>}
  </div>;
}
