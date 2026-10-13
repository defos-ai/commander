import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { Mission } from '../src/api/types';
import { MissionRulesGraph } from '../src/components/MissionRulesGraph';

const mission: Mission = {
  name: 'review-website-with-browser', objective: 'Review a website.',
  required_actions: ['discover-pages', 'check-links', 'capture-page'],
  starting_actions: ['discover-pages', 'manual-start'], input_schema: null, output_schema: null,
  rules: {
    'aggregate-results': { actions: ['discover-pages'] },
    'decide-next-actions': { actions: ['capture-page', 'rule-only-action'], params: { decision_by: 'model' } },
  },
};

function inspect(name: string) {
  fireEvent.click(screen.getByRole('button', { name: `Inspect rules for ${name}` }));
  return within(screen.getByRole('dialog', { name: 'Rule details' }));
}

describe('mission rules graph', () => {
  it('shows known actions and their behavior without inventing action-to-action dependencies', () => {
    render(<MissionRulesGraph mission={mission} />);
    const graph = screen.getByRole('region', { name: 'Mission rules graph' });
    expect(within(graph).getAllByRole('button').map(button => button.getAttribute('aria-label')).sort()).toEqual([
      'Inspect rules for capture-page', 'Inspect rules for check-links', 'Inspect rules for discover-pages',
      'Inspect rules for manual-start', 'Inspect rules for rule-only-action',
    ]);
    expect(within(within(graph).getByRole('button', { name: 'Inspect rules for manual-start' })).getByText('Starting action')).toBeVisible();
    expect(within(graph).getByLabelText('discover-pages: Run valid proposals')).toBeVisible();
    expect(within(graph).getByLabelText('discover-pages: Wait for all children')).toBeVisible();
    expect(within(graph).getByLabelText('capture-page: Model review')).toBeVisible();
    expect(within(graph).getByLabelText('check-links: Continue independently')).toBeVisible();

    const details = inspect('discover-pages');
    expect(details.getByText('Valid next-action proposals run automatically. The action chooses which follow-ups to propose.')).toBeVisible();
    expect(details.getByText(/Wait for every direct child created by this execution/)).toHaveTextContent('Recursive descendants are not included.');
    expect(details.queryByRole('button', { name: 'capture-page' })).not.toBeInTheDocument();
  });

  it('uses automatic proposals and independent children when no rule matches', () => {
    render(<MissionRulesGraph mission={mission} />);
    const details = inspect('check-links');
    expect(details.getByRole('heading', { name: 'Automatically run proposed actions' })).toBeVisible();
    expect(details.getByText('Each generated child continues independently according to its own rules.')).toBeVisible();
    expect(details.getByText('Failed work, invalid proposals or no proposals require model review.')).toBeVisible();
  });

  it('explains grouped model decisions and restores focus after closing details', () => {
    render(<MissionRulesGraph mission={mission} />);
    const node = screen.getByRole('button', { name: 'Inspect rules for capture-page' });
    node.focus();
    const details = inspect('capture-page');
    expect(details.getByRole('heading', { name: 'capture-page' })).toHaveFocus();
    expect(details.getByRole('heading', { name: 'Model review' })).toBeVisible();
    expect(details.getByText('Commander decides the next actions. Inside an aggregation group, this decision waits for the group and includes all its results.')).toBeVisible();
    expect(node).toHaveAttribute('aria-pressed', 'true');
    fireEvent(screen.getByRole('dialog'), new Event('cancel', { cancelable: true }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(node).toHaveFocus();
    expect(document.body.style.overflow).not.toBe('hidden');
  });

  it('allows model decisions and aggregation on the same action', () => {
    render(<MissionRulesGraph mission={{ ...mission, rules: {
      'aggregate-results': { actions: ['capture-page'] },
      'decide-next-actions': { actions: ['capture-page'], params: { decision_by: 'model' } },
    } }} />);
    const details = inspect('capture-page');
    expect(details.getByRole('heading', { name: 'Model review' })).toBeVisible();
    expect(details.getByText(/Wait for every direct child created by this execution/)).toBeVisible();
  });

  it('closes a removed action selection and returns focus to the graph', () => {
    const view = render(<MissionRulesGraph mission={mission} />);
    inspect('rule-only-action');
    view.rerender(<MissionRulesGraph mission={{ ...mission, rules: {} }} />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Mission rules graph' })).toHaveFocus();
  });

  it.each([undefined, {}])('shows default behavior for known actions with rules %s', rules => {
    render(<MissionRulesGraph mission={{ ...mission, rules }} />);
    const details = inspect('discover-pages');
    expect(details.getByRole('heading', { name: 'Automatically run proposed actions' })).toBeVisible();
    expect(details.getByText('Each generated child continues independently according to its own rules.')).toBeVisible();
  });

  it('explains the default without inventing actions for an empty mission', () => {
    render(<MissionRulesGraph mission={{ ...mission, required_actions: null, starting_actions: null, rules: {} }} />);
    expect(screen.getByText('No action rules defined. Valid proposals run automatically; failures or no proposals require model review.')).toBeVisible();
    expect(screen.queryByRole('region')).not.toBeInTheDocument();
  });
});
