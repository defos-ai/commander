export const initialMissionDefinition = JSON.stringify({
  name: '',
  objectives: [''],
  required_actions: [],
  starting_actions: [],
  input_schema: {},
  output_schema: {},
}, null, 2);

export type MissionDraft = {
  name: string;
  objectives: string[];
  completion_decision: 'model' | 'operator';
  required_actions: string[];
  optional_actions: string[];
  starting_actions: string[];
  input_schema: string;
  output_schema: string;
  rules: string;
  action_definitions: string;
};

type Span = { start: number; end: number };
type Document = { value: Record<string, unknown>; fields: Map<string, Span>; open: number };

function object(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function whitespace(source: string, index: number): number {
  while (/\s/.test(source[index] ?? '') && index < source.length) index++;
  return index;
}

// JSON.parse validates the grammar; this scan locates original tokens without
// converting schema numbers or rewriting escaped strings.
function stringEnd(source: string, start: number): number {
  let index = start + 1;
  while (index < source.length) {
    if (source[index] === '\\') index += 2;
    else if (source[index++] === '"') return index;
  }
  throw new SyntaxError('Invalid JSON string');
}

function valueEnd(source: string, start: number): number {
  if (source[start] === '"') return stringEnd(source, start);
  if (source[start] !== '{' && source[start] !== '[') {
    let end = start;
    while (end < source.length && !/[\s,}\]]/.test(source[end])) end++;
    return end;
  }
  let depth = 1;
  let index = start + 1;
  while (depth > 0) {
    const character = source[index];
    if (character === '"') index = stringEnd(source, index);
    else {
      if (character === '{' || character === '[') depth++;
      else if (character === '}' || character === ']') depth--;
      index++;
    }
  }
  return index;
}

function document(source: string): Document {
  const value: unknown = JSON.parse(source);
  if (!object(value)) throw new TypeError('Mission definition must be an object');
  const fields = new Map<string, Span>();
  const open = whitespace(source, 0);
  let index = whitespace(source, open + 1);
  while (source[index] !== '}') {
    const keyEnd = stringEnd(source, index);
    const key = JSON.parse(source.slice(index, keyEnd)) as string;
    if (fields.has(key)) throw new TypeError('Mission definition contains duplicate fields');
    const start = whitespace(source, whitespace(source, keyEnd) + 1);
    const end = valueEnd(source, start);
    fields.set(key, { start, end });
    index = whitespace(source, end);
    if (source[index] === ',') index = whitespace(source, index + 1);
  }
  return { value, fields, open };
}

export { document as readJSONObject, valueEnd as jsonValueEnd, whitespace as skipJSONWhitespace };

function strings(value: unknown, nullable = false): value is string[] | undefined | null {
  return (nullable && value == null) || (Array.isArray(value) && value.every(entry => typeof entry === 'string'));
}

export function readMissionDraft(source: string): MissionDraft {
  const { value, fields } = document(source);
  if ('steps' in value || typeof value.name !== 'string' || !strings(value.objectives) || !value.objectives?.length
    || !strings(value.required_actions, true) || !strings(value.optional_actions, true) || !strings(value.starting_actions, true)) {
    throw new TypeError('Mission form fields have invalid types');
  }
  for (const field of ['input_schema', 'output_schema', 'rules']) {
    if (value[field] != null && !object(value[field])) throw new TypeError('Mission object field has an invalid type');
  }
  if (value.action_definitions != null && !Array.isArray(value.action_definitions)) {
    throw new TypeError('Mission action definitions must be a list');
  }
  const completion = value.completion;
  if (completion != null && (!object(completion) || Object.keys(completion).some(key => key !== 'decision_by') ||
    (completion.decision_by !== 'model' && completion.decision_by !== 'operator'))) {
    throw new TypeError('Mission completion policy must specify model or operator');
  }
  function raw(field: string, fallback: string): string {
    const span = fields.get(field);
    return span ? source.slice(span.start, span.end) : fallback;
  }
  return {
    name: value.name,
    objectives: value.objectives,
    completion_decision: completion?.decision_by === 'operator' ? 'operator' : 'model',
    required_actions: value.required_actions ?? [],
    optional_actions: value.optional_actions ?? [],
    starting_actions: value.starting_actions ?? [],
    input_schema: raw('input_schema', '{}'),
    output_schema: raw('output_schema', '{}'),
    rules: raw('rules', '{}'),
    action_definitions: raw('action_definitions', '[]'),
  };
}

export function updateMissionDefinition(source: string, field: string, jsonValue: string): string {
  const { fields, open } = document(source);
  JSON.parse(jsonValue);
  const current = fields.get(field);
  if (current) return source.slice(0, current.start) + jsonValue + source.slice(current.end);
  const last = [...fields.values()].at(-1);
  const index = last?.end ?? open + 1;
  return source.slice(0, index) + `${last ? ',' : ''}\n  ${JSON.stringify(field)}: ${jsonValue}` + source.slice(index);
}

export function writeMissionDraft(source: string, draft: MissionDraft): string {
  const previous = readMissionDraft(source);
  let result = source;
  for (const field of ['name', 'objectives', 'required_actions', 'optional_actions', 'starting_actions'] as const) {
    const jsonValue = JSON.stringify(draft[field]);
    if (JSON.stringify(previous[field]) !== jsonValue) result = updateMissionDefinition(result, field, jsonValue);
  }
  for (const field of ['input_schema', 'output_schema', 'rules', 'action_definitions'] as const) {
    if (previous[field] !== draft[field]) result = updateMissionDefinition(result, field, draft[field]);
  }
  if (previous.completion_decision !== draft.completion_decision) {
    result = updateMissionDefinition(result, 'completion', JSON.stringify({ decision_by: draft.completion_decision }));
  }
  readMissionDraft(result);
  return result;
}
