import { fireEvent } from '@testing-library/react';

export function pointer(target: Element, type: string, options: PointerEventInit = {}) {
  const event = new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, buttons: type === 'pointerup' ? 0 : 1, ...options });
  Object.defineProperties(event, {
    pointerId: { value: options.pointerId ?? 1 },
    pointerType: { value: options.pointerType ?? 'mouse' },
    isPrimary: { value: options.isPrimary ?? true },
  });
  fireEvent(target, event);
}

export function installPointerCapture(elements: Element[]) {
  for (const element of elements) {
    let captured: number | undefined;
    Object.defineProperties(element, {
      setPointerCapture: { value: (id: number) => { captured = id; } },
      hasPointerCapture: { value: (id: number) => captured === id },
      releasePointerCapture: { value: () => { captured = undefined; } },
    });
  }
}
