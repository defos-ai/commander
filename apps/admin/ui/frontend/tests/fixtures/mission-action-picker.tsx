import { useState } from 'react';
import { vi } from 'vitest';
import type { Action } from '../../src/api/types';
import { MissionActionPicker, type MissionActionSelection } from '../../src/components/MissionActionPicker';

export const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };

export const credentials = { username: 'operator', url: connection.url };

export const capture: Action = {
  name: 'capture-page', description: 'Capture a screenshot.', status: 'unavailable', status_reason: 'Chromium is missing.',
  missing_requirements: [{ kind: 'executable', name: 'chromium' }],
  input_schema: { type: 'object', properties: { url: { type: 'string', description: 'Page to capture' } } },
  output_schema: {},
};

export const links: Action = { ...capture, name: 'check-links', description: 'Check links.', status: 'ready', status_reason: undefined,
  missing_requirements: [] };

export function Picker({ initial = { requiredActions: [], optionalActions: [], startingActions: [] }, disabled = false,
  onChange = vi.fn(), onSignIn = vi.fn() }: {
  initial?: MissionActionSelection;
  disabled?: boolean;
  onChange?: (selection: MissionActionSelection) => void;
  onSignIn?: () => void;
}) {
  const [selection, setSelection] = useState(initial);
  return <MissionActionPicker connection={connection} credentials={credentials} {...selection} disabled={disabled}
    onChange={value => { setSelection(value); onChange(value); }} onSignIn={onSignIn} />;
}
