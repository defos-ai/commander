import type { Mission } from '../../src/api/types';

export const url = 'https://commander.example/proxy';

export const credentials = { url: url, username: ' opérateur ' };

export const mission: Mission = {
  name: 'review-website-with-browser',
  objectives: ['Review a website'],
  required_actions: ['discover-pages'],
  optional_actions: ['capture-page'],
  action_definitions: [{ name: 'discover-pages', description: 'Discover pages over HTTP.',
    input_schema: { type: 'object', required: ['url'], properties: { url: { type: 'string' } } },
    output_schema: { type: 'object' }, required_secrets: ['SITE_TOKEN'] }],
  starting_actions: ['discover-pages'],
  rules: {
    'aggregate-results': { for_actions: ['discover-pages'] },
    'decide-next-actions': { for_actions: ['check-links'], params: { decision_by: 'model' } },
    'same-agent': { for_actions: ['discover-pages', 'check-links'] },
  },
  input_schema: { type: 'object', properties: { url: { type: 'string' } } },
  output_schema: {},
};
