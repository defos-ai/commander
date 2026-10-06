import { useId, useRef, useState, type ReactNode, type RefObject } from 'react';
import { RotateCcw, ZoomIn, ZoomOut } from 'lucide-react';
import { useGraphPan } from '../hooks/useGraphPan';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import '../styles/operation-graph.css';

export function GraphCanvas({ width, height, label, legend, edges, viewportRef, children }: {
  width: number;
  height: number;
  label: string;
  legend: ReactNode;
  edges: { id: string; path: string; selected?: boolean }[];
  viewportRef?: RefObject<HTMLDivElement | null>;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const identity = useId();
  const [zoom, setZoom] = useState(1);
  const internalViewport = useRef<HTMLDivElement>(null);
  const viewport = viewportRef ?? internalViewport;
  const { panning, ...panEvents } = useGraphPan();

  function reset() {
    setZoom(1);
    viewport.current?.scrollTo?.({ left: 0, top: 0 });
  }

  return <div className="execution-graph">
    <div className="graph-toolbar">
      {legend}
      <div className="graph-zoom-controls">
        <Button variant="secondary" aria-label={t($ => $.operationGraph.zoomOut)} disabled={zoom <= .5} onClick={() => setZoom(value => Math.max(.5, value - .25))}><ZoomOut size={16} aria-hidden="true" /></Button>
        <output aria-label={t($ => $.operationGraph.zoom)}>{t($ => $.operationGraph.zoomValue, { value: Math.round(zoom * 100) })}</output>
        <Button variant="secondary" aria-label={t($ => $.operationGraph.zoomIn)} disabled={zoom >= 1.5} onClick={() => setZoom(value => Math.min(1.5, value + .25))}><ZoomIn size={16} aria-hidden="true" /></Button>
        <Button variant="secondary" onClick={reset}><RotateCcw size={14} aria-hidden="true" />{t($ => $.operationGraph.reset)}</Button>
      </div>
    </div>
    <p className="graph-navigation-hint" id={`${identity}-hint`}>{t($ => $.operationGraph.navigationHint)}</p>
    <div className="graph-viewport" ref={viewport} {...panEvents} data-panning={panning || undefined} tabIndex={0} role="region" aria-label={label} aria-describedby={`${identity}-hint`}>
      <div className="graph-scale-frame" style={{ width: width * zoom, height: height * zoom }}>
        <div className="graph-canvas" style={{ width, height, transform: `scale(${zoom})` }}>
          <svg className="graph-edges" width={width} height={height} aria-hidden="true">
            <defs><marker id={`${identity}-arrow`} viewBox="0 0 8 8" refX="7" refY="4" markerWidth="6" markerHeight="6" orient="auto-start-reverse"><path d="M 0 0 L 8 4 L 0 8 z" fill="context-stroke" /></marker></defs>
            {edges.map(edge => <path key={edge.id} d={edge.path} className="graph-edge"
              data-selected={edge.selected ? 'true' : undefined} markerEnd={`url(#${identity}-arrow)`} />)}
          </svg>
          {children}
        </div>
      </div>
    </div>
  </div>;
}
