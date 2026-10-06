import { useEffect, useMemo, useRef, useState } from 'react';
import { Brain, GitBranch, Workflow } from 'lucide-react';
import type { Mission, MissionAutomation } from '../api/types';
import { useTranslation } from '../i18n';
import { Dialog } from './Dialog';
import { GraphCanvas } from './GraphCanvas';
import { GRAPH_NODE_HEIGHT, GRAPH_NODE_WIDTH, layoutGraph } from './graph-layout';
import '../styles/automation-graph.css';

function automationGraph(mission: Mission) {
  const rules = mission.automations ?? {};
  const starting = new Set(mission.starting_actions ?? []);
  const names = new Set([...starting, ...Object.keys(rules), ...Object.values(rules).flatMap(rule => rule.run ?? [])]);
  const nodes = [...names].sort((a, b) => Number(starting.has(b)) - Number(starting.has(a)) || a.localeCompare(b))
    .map(name => ({ id: name, starting: starting.has(name), rule: Object.hasOwn(rules, name) ? rules[name] : undefined }));
  const edges = nodes.flatMap(node => [...new Set(node.rule?.run ?? [])].map(target => ({ source: node.id, target })));
  return { nodes, edges };
}

function ContinuationLabel({ rule, short = false }: { rule: MissionAutomation; short?: boolean }) {
  const { t } = useTranslation();
  if (rule.continue === 'when_all') return <>{short ? t($ => $.automationGraph.whenAll) : t($ => $.missions.whenAll)}</>;
  if (!rule.continue || rule.continue === 'when_each') return <>{short ? t($ => $.automationGraph.whenEach) : t($ => $.missions.whenEach)}</>;
  return <>{t($ => $.missions.continuation, { value: rule.continue })}</>;
}

function DecisionLabel({ rule }: { rule?: MissionAutomation }) {
  const { t } = useTranslation();
  return <>{!rule?.decide || rule.decide === 'model' ? t($ => $.missions.modelReview)
    : t($ => $.missions.decision, { value: rule.decide })}</>;
}

export function AutomationGraph({ mission }: { mission: Mission }) {
  const { t } = useTranslation();
  const graph = useMemo(() => automationGraph(mission), [mission]);
  const layout = useMemo(() => layoutGraph(graph, { allowCycles: true }), [graph]);
  const [selectedID, setSelectedID] = useState<string>();
  const selected = graph.nodes.find(node => node.id === selectedID);
  const viewport = useRef<HTMLDivElement>(null);
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  const returnFocusID = useRef<string | undefined>(undefined);
  const heading = useRef<HTMLHeadingElement>(null);

  useEffect(() => { if (selected) heading.current?.focus(); }, [selected?.id]);

  useEffect(() => {
    if (selectedID && !selected) {
      returnFocusID.current = selectedID;
      setSelectedID(undefined);
    }
  }, [selectedID, selected]);

  useEffect(() => {
    if (!selected && returnFocusID.current) {
      (buttons.current.get(returnFocusID.current) ?? viewport.current)?.focus();
      returnFocusID.current = undefined;
    }
  }, [selected]);

  function closeDetails() {
    returnFocusID.current = selectedID;
    setSelectedID(undefined);
  }

  function relations(names: string[]) {
    return <ul className="graph-relations">{names.map(name => <li key={name}>
      <button type="button" onClick={() => setSelectedID(name)}><strong>{name}</strong></button>
    </li>)}</ul>;
  }

  if (!Object.keys(mission.automations ?? {}).length) return <p className="mission-empty-rules">{t($ => $.missions.noAutomations)}</p>;

  return <div className="automation-graph">
    <GraphCanvas width={layout.width} height={layout.height} label={t($ => $.automationGraph.renderLabel)} viewportRef={viewport}
      legend={<ul className="graph-legend">
        <li><Workflow size={14} aria-hidden="true" />{t($ => $.automationGraph.action)}</li>
        <li><GitBranch size={14} aria-hidden="true" />{t($ => $.automationGraph.followUp)}</li>
        <li><Brain size={14} aria-hidden="true" />{t($ => $.missions.modelReview)}</li>
      </ul>}
      edges={layout.edges.map(({ edge, path }) => ({ id: JSON.stringify([edge.source, edge.target]), path, selected: edge.source === selectedID || edge.target === selectedID }))}>
      {layout.nodes.map(({ node, x, y }) => {
        const automatic = Boolean(node.rule?.run?.length);
        return <button type="button" className="graph-node" key={node.id}
          ref={element => { if (element) buttons.current.set(node.id, element); else buttons.current.delete(node.id); }}
          style={{ left: x, top: y, width: GRAPH_NODE_WIDTH, height: GRAPH_NODE_HEIGHT }} data-starting={node.starting || undefined}
          aria-label={t($ => $.automationGraph.selectNode, { name: node.id })} aria-haspopup="dialog"
          aria-pressed={node.id === selectedID} onClick={() => setSelectedID(node.id)}>
          <span className="graph-node-kind"><Workflow size={14} aria-hidden="true" />{node.starting ? t($ => $.automationGraph.startingAction) : t($ => $.automationGraph.action)}</span>
          <strong className="graph-node-name" title={node.id}>{node.id}</strong>
          <span className="graph-node-meta">{automatic ? <><GitBranch size={13} aria-hidden="true" /><ContinuationLabel rule={node.rule!} short /></>
            : <><Brain size={13} aria-hidden="true" /><DecisionLabel rule={node.rule} /></>}</span>
        </button>;
      })}
    </GraphCanvas>
    {selected && <Dialog open title={t($ => $.automationGraph.nodeDetails)} onClose={closeDetails} className="graph-node-dialog">
      <div className="graph-node-details">
        <h3 ref={heading} tabIndex={-1}>{selected.id}</h3>
        {selected.starting && <p className="mission-muted">{t($ => $.automationGraph.startingAction)}</p>}
        {selected.rule?.run?.length ? <>
          <h4>{t($ => $.missions.run)}</h4>
          <p>{t($ => $.automationGraph.runDescription)}</p>
          {relations([...new Set(selected.rule.run)])}
          <h4>{t($ => $.automationGraph.continuation)}</h4>
          <p><ContinuationLabel rule={selected.rule} /></p>
        </> : <>
          <h4><DecisionLabel rule={selected.rule} /></h4>
          {!selected.rule ? <p>{t($ => $.automationGraph.noRule)}</p>
            : (!selected.rule.decide || selected.rule.decide === 'model') && <p>{t($ => $.automationGraph.reviewDescription)}</p>}
        </>}
        {graph.edges.some(edge => edge.target === selected.id) && <>
          <h4>{t($ => $.automationGraph.proposedBy)}</h4>
          {relations(graph.edges.filter(edge => edge.target === selected.id).map(edge => edge.source))}
        </>}
      </div>
    </Dialog>}
  </div>;
}
