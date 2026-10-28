import { render } from '@testing-library/react';
import { vi } from 'vitest';
import { WorkspaceLayout } from '../../src/components/WorkspaceLayout';

export const connection = { id: 'local', name: 'Local Commander', url: 'http://localhost:4274' };

export function workspace() {
  const onNavigate = vi.fn();
  const onBack = vi.fn();
  const view = render(<WorkspaceLayout connection={connection} section="overview" onNavigate={onNavigate} onBack={onBack}>
    <h1>Connection overview</h1><button>Refresh overview</button>
  </WorkspaceLayout>);
  return { ...view, onNavigate, onBack };
}
