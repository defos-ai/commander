import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach, beforeEach, vi } from 'vitest';
import { initializeLocalization } from '../src/i18n';
import { installDialogAPI } from './fakes/dialog';

await initializeLocalization();

installDialogAPI();

beforeEach(() => { window.history.replaceState(null, '', '/'); });

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
