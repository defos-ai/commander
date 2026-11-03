export default {
  continuation: 'Continuation',
  behavior: 'Behavior',
  appliesTo: 'Actions',
  effect: 'Effect',
  aggregateResults: 'Aggregate results',
  modelReview: 'Model review',
  sameAgent: 'Same agent',
  sameAgentSetting: 'One agent per operation; no fallback',
  allDirectChildren: 'Wait for all direct children to finish',
  reviewProposals: 'Review valid proposals before continuing',
  defaults: 'Valid proposals run automatically unless model review is required. Failed work and invalid or empty proposals require model review. An action can aggregate child results and require model review.',
  empty: 'No continuation overrides.',
} as const;
