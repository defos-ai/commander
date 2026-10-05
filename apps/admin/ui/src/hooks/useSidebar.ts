import { useCallback, useEffect, useRef, useState } from 'react';

const mobileQuery = '(max-width: 760px)';

function isMobile() {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    && window.matchMedia(mobileQuery).matches;
}

export function useSidebar() {
  const [mobile, setMobile] = useState(isMobile);
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLElement>(null);
  const restoreFrame = useRef<number | null>(null);
  const close = useCallback(() => setOpen(false), []);
  const modal = mobile && open;

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const query = window.matchMedia(mobileQuery);
    const update = () => {
      setMobile(query.matches);
      if (!query.matches) setOpen(false);
    };
    update();
    query.addEventListener('change', update);
    return () => query.removeEventListener('change', update);
  }, []);

  useEffect(() => {
    if (!modal) return;
    if (restoreFrame.current !== null) {
      window.cancelAnimationFrame(restoreFrame.current);
      restoreFrame.current = null;
    }
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    const controls = () => Array.from(panelRef.current?.querySelectorAll<HTMLElement>(
      'a[href], button, input, select, textarea, [tabindex], [contenteditable="true"]',
    ) ?? []).filter(element => element.tabIndex >= 0 && !element.matches(':disabled')
      && !element.closest('[hidden], [inert], [aria-hidden="true"]')
      && window.getComputedStyle(element).display !== 'none'
      && window.getComputedStyle(element).visibility !== 'hidden');
    const focusFirst = () => (controls()[0] ?? panelRef.current)?.focus();
    focusFirst();

    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') {
        event.preventDefault();
        close();
        return;
      }
      if (event.key !== 'Tab') return;
      const focusable = controls();
      const first = focusable[0];
      const last = focusable.at(-1);
      if (!first) {
        event.preventDefault();
        panelRef.current?.focus();
      } else if (!panelRef.current?.contains(document.activeElement)) {
        event.preventDefault();
        (event.shiftKey ? last : first)?.focus();
      } else if (event.shiftKey && (document.activeElement === first || document.activeElement === panelRef.current)) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    }

    function onFocusIn(event: FocusEvent) {
      if (event.target instanceof Node && !panelRef.current?.contains(event.target)) focusFirst();
    }

    window.addEventListener('keydown', onKeyDown);
    document.addEventListener('focusin', onFocusIn);
    return () => {
      window.removeEventListener('keydown', onKeyDown);
      document.removeEventListener('focusin', onFocusIn);
      document.body.style.overflow = previousOverflow;
      // Wait for React to reveal the trigger or desktop navigation before focus.
      restoreFrame.current = window.requestAnimationFrame(() => {
        restoreFrame.current = null;
        const target = isMobile() ? triggerRef.current ?? previousFocus : controls()[0];
        if (target?.isConnected && !target.closest('[hidden], [inert]')) target.focus();
      });
    };
  }, [modal, close]);

  return { mobile, open, modal, visible: !mobile || open, setOpen, close, triggerRef, panelRef };
}
