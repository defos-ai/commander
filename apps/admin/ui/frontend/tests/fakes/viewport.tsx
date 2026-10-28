import { act } from '@testing-library/react';
import { vi } from 'vitest';

export function viewport(mobile: boolean) {
  const listeners = new Set<(event: MediaQueryListEvent) => void>();
  const media = {
    matches: mobile,
    media: '(max-width: 760px)',
    onchange: null,
    addEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => listeners.add(listener),
    removeEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener),
  };
  vi.stubGlobal('matchMedia', vi.fn(() => media));
  return (matches: boolean) => act(() => {
    media.matches = matches;
    listeners.forEach(listener => listener({ matches, media: media.media } as MediaQueryListEvent));
  });
}
