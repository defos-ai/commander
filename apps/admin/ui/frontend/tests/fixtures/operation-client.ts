import type { Operation } from '../../src/api/operation-types';

export const url = 'https://commander.example/team';

export const credentials = { url, username: 'operator' };

export const timestamp = '2026-10-05T12:00:00.123456789Z';

export const operation: Operation = {
  id: 'operation-1',
  mission: {
    name: 'review-website-with-browser', objectives: ['Review a website'], required_actions: ['check-links'],
    starting_actions: ['discover-pages'], input_schema: null, output_schema: null,
  },
  input: { url: 'https://example.com' }, status: 'waiting',
  progress: { task_counts: { succeeded: 1 }, total_tasks: 1, finished_tasks: 1, pending_decisions: 0, pending_questions: 0, settled: false },
  affinity: { for_actions: ['check-links', 'capture-page'], agent_id: 'agent-1' },
  preflight: {
    ready: false, version: 2, actions: [{
      action_name: 'capture-page', ready_agent_ids: ['agent-1'], unavailable_agents: [{
        agent_id: 'agent-2', agent_type: 'browser', reason: 'Browser missing',
        missing_requirements: [{ kind: 'executable', name: 'chromium' }, { kind: 'secret', name: 'SCREENSHOT_TOKEN' }],
      }],
    }],
  },
  tasks: [{
    id: 'task-1', operation_id: 'operation-1', parent_task_id: 'task-0', action_name: 'check-links',
    agent_id: 'agent-1', command_id: 'command-1', input: { url: 'https://example.com' },
    status: 'succeeded', started_at: timestamp, finished_at: timestamp,
    result: {
      action_name: 'check-links', output: { broken_links: 0 },
      artifacts: [{
        id: 'artifact-1', name: 'Links report', agent_id: 'agent-1', action_name: 'check-links', created_at: timestamp,
        files: [{ id: 'file-1', name: 'links.json', media_type: 'application/json', size: 42, sha256: 'abc' }],
      }],
      next_actions: [{ action_name: 'capture-page', input: { url: 'https://example.com' }, reason: 'Review rendering' }],
    },
    continuation: {
      version: 2, decision_by: 'operator', status: 'awaiting_operator',
      proposed_actions: [{ action_name: 'capture-page' }], created_task_ids: [], question_id: 'question-1',
    },
  }],
  questions: [{
    id: 'question-1', task_id: 'task-1', status: 'pending', text: 'Continue with rendering checks?',
    options: ['Yes', 'No'], asked_at: timestamp, expires_at: timestamp,
  }],
  events: [{ sequence: 1, operation_id: 'operation-1', type: 'task.finished', status: 'waiting', task_id: 'task-1', created_at: timestamp }],
  created_at: timestamp, updated_at: timestamp,
};
