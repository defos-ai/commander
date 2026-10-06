import { useRef, useState, type MouseEvent, type PointerEvent } from 'react';

type Gesture = {
  pointerID: number;
  target: HTMLElement;
  x: number;
  y: number;
  left: number;
  top: number;
  moved: boolean;
};

export function useGraphPan() {
  const gesture = useRef<Gesture | null>(null);
  const suppressClick = useRef(false);
  const [panning, setPanning] = useState(false);

  function start(event: PointerEvent<HTMLDivElement>) {
    if (!gesture.current && event.isPrimary) suppressClick.current = false;
    if (event.button !== 0 || event.pointerType === 'touch' || !event.isPrimary) return;
    const target = event.target instanceof Element
      ? event.target.closest<HTMLElement>('.graph-node') ?? event.currentTarget : event.currentTarget;
    gesture.current = {
      pointerID: event.pointerId, target, x: event.clientX, y: event.clientY,
      left: event.currentTarget.scrollLeft, top: event.currentTarget.scrollTop, moved: false,
    };
    suppressClick.current = false;
    setPanning(false);
    target.setPointerCapture(event.pointerId);
  }

  function finish(event: PointerEvent<HTMLDivElement>) {
    const current = gesture.current;
    if (!current || current.pointerID !== event.pointerId) return;
    gesture.current = null;
    setPanning(false);
    if (current.target.hasPointerCapture(event.pointerId)) current.target.releasePointerCapture(event.pointerId);
  }

  function move(event: PointerEvent<HTMLDivElement>) {
    const current = gesture.current;
    if (!current || current.pointerID !== event.pointerId) return;
    if ((event.buttons & 1) === 0) { finish(event); return; }
    const dx = event.clientX - current.x;
    const dy = event.clientY - current.y;
    if (!current.moved && Math.hypot(dx, dy) < 4) return;
    if (!current.moved) {
      current.moved = true;
      suppressClick.current = true;
      setPanning(true);
    }
    event.preventDefault();
    event.currentTarget.scrollLeft = current.left - dx;
    event.currentTarget.scrollTop = current.top - dy;
  }

  function click(event: MouseEvent<HTMLDivElement>) {
    if (!suppressClick.current || event.detail === 0) return;
    suppressClick.current = false;
    event.preventDefault();
    event.stopPropagation();
  }

  return {
    panning,
    onPointerDown: start,
    onPointerMove: move,
    onPointerUp: finish,
    onPointerCancel: finish,
    onLostPointerCapture: finish,
    onClickCapture: click,
  };
}
