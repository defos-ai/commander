import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach, vi } from 'vitest';
import { initializeLocalization } from '../src/i18n';

await initializeLocalization();

// jsdom does not implement the browser's native dialog lifecycle.
Object.defineProperties(HTMLDialogElement.prototype, {
  showModal: { configurable: true, value(this: HTMLDialogElement) { this.open = true; } },
  close: { configurable: true, value(this: HTMLDialogElement) { this.open = false; } },
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
