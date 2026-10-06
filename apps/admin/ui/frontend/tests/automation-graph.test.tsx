import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { Mission } from '../src/api/types';
import { AutomationGraph } from '../src/components/AutomationGraph';

const mission: Mission = {
  name: 'review-website', objective: 'Review a website.',
  required_actions: ['discover-pages', 'check-links', 'capture-page'],
  starting_actions: ['discover-pages', 'manual-start'], input_schema: null, output_schema: null,
  automations: {
    'discover-pages': { run: ['check-links', 'capture-page', 'unconfigured-action', 'check-links'], continue: 'when_all' },
    'check-links': { run: ['capture-page'] },
    'capture-page': { decide: 'model' },
  },
};

function inspect(name: string) {
  fireEvent.click(screen.getByRole('button', { name: `Inspect automation ${name}` }));
  return within(screen.getByRole('dialog', { name: 'Automation details' }));
}

describe('mission automation graph', () => {
  it('shows starting actions, rule actions and targets with only their allowed follow-up edges', () => {
    render(<AutomationGraph mission={mission} />);
    const graph = screen.getByRole('region', { name: 'Mission automation graph' });
    expect(within(graph).getAllByRole('button').map(button => button.getAttribute('aria-label')).sort()).toEqual([
      'Inspect automation capture-page', 'Inspect automation check-links', 'Inspect automation discover-pages',
      'Inspect automation manual-start', 'Inspect automation unconfigured-action',
    ]);
    expect(graph.querySelectorAll('.graph-edge')).toHaveLength(4);
    expect(within(within(graph).getByRole('button', { name: 'Inspect automation manual-start' })).getByText('Starting action')).toBeVisible();
    expect(within(within(graph).getByRole('button', { name: 'Inspect automation capture-page' })).getByText('Model review')).toBeVisible();

    const details = inspect('discover-pages');
    expect(details.getByText('Run proposals automatically only when all are allowed below. These are possible follow-ups, not mandatory steps. Failed work or no proposals also require model review.')).toBeVisible();
    expect(within(details.getByRole('list')).getAllByRole('button').map(button => button.textContent)).toEqual(['check-links', 'capture-page', 'unconfigured-action']);
    expect(details.getByText('Continue after all generated tasks finish.')).toBeVisible();
    expect(graph.querySelectorAll('.graph-edge[data-selected="true"]')).toHaveLength(3);
  });

  it.each([undefined, 'when_each'])('uses per-task continuation when continue is %s', continuation => {
    render(<AutomationGraph mission={{ ...mission, automations: { ...mission.automations,
      'check-links': { run: ['capture-page'], continue: continuation },
    } }} />);
    const details = inspect('check-links');
    expect(details.getByText('Continue after each generated task finishes.')).toBeVisible();
    expect(details.queryByText('Continue after all generated tasks finish.')).not.toBeInTheDocument();
  });

  it('navigates related actions and returns focus to the selected node on close', () => {
    render(<AutomationGraph mission={mission} />);
    const node = screen.getByRole('button', { name: 'Inspect automation discover-pages' });
    node.focus();
    const details = inspect('discover-pages');
    fireEvent.click(details.getByRole('button', { name: 'capture-page' }));
    expect(details.getByRole('heading', { name: 'capture-page' })).toHaveFocus();
    expect(details.getByRole('heading', { name: 'Model review' })).toBeVisible();
    expect(details.getByText('The model decides how to continue. If this action belongs to a wait-for-all group, its results are reviewed with the group.')).toBeVisible();
    expect(details.getByRole('heading', { name: 'Can be proposed by' })).toBeVisible();
    expect(details.getByRole('button', { name: 'discover-pages' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Inspect automation capture-page' })).toHaveAttribute('aria-pressed', 'true');
    fireEvent(screen.getByRole('dialog'), new Event('cancel', { cancelable: true }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Inspect automation capture-page' })).toHaveFocus();
    expect(document.body.style.overflow).not.toBe('hidden');
  });

  it('explains targets without a rule and clears a removed selection', () => {
    const view = render(<AutomationGraph mission={mission} />);
    const details = inspect('unconfigured-action');
    expect(details.getByRole('heading', { name: 'Model review' })).toBeVisible();
    expect(details.getByText('No automation rule is defined for this action. The model decides how to continue.')).toBeVisible();

    view.rerender(<AutomationGraph mission={{ ...mission, automations: { 'discover-pages': { decide: 'model' } } }} />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Mission automation graph' })).toHaveFocus();
  });

  it('renders cyclic follow-ups and self-loops without inventing additional actions', () => {
    render(<AutomationGraph mission={{ ...mission, starting_actions: ['retry'], automations: {
      retry: { run: ['review', 'retry'] }, review: { run: ['retry'] },
    } }} />);
    const graph = screen.getByRole('region', { name: 'Mission automation graph' });
    expect(within(graph).getAllByRole('button')).toHaveLength(2);
    expect(graph.querySelectorAll('.graph-edge')).toHaveLength(3);
    expect(inspect('review').getByRole('heading', { name: 'review' })).toBeVisible();
  });

  it('preserves unfamiliar continuation and decision values in the rule details', () => {
    render(<AutomationGraph mission={{ ...mission, starting_actions: ['discover-pages'], automations: {
      'discover-pages': { run: ['capture-page'], continue: 'when_approved' },
      'capture-page': { decide: 'human' },
    } }} />);
    const details = inspect('discover-pages');
    expect(details.getByText('Continuation: when_approved')).toBeVisible();
    fireEvent.click(details.getByRole('button', { name: 'capture-page' }));
    expect(details.getByRole('heading', { name: 'Decision: human' })).toBeVisible();
  });

  it.each([undefined, {}])('keeps empty automation rules explicit', automations => {
    render(<AutomationGraph mission={{ ...mission, automations }} />);
    expect(screen.getByText('No automation rules defined.')).toBeVisible();
    expect(screen.queryByRole('region', { name: 'Mission automation graph' })).not.toBeInTheDocument();
  });
});
