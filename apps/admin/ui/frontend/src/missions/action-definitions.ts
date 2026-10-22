import { jsonValueEnd, readJSONObject, skipJSONWhitespace, updateMissionDefinition } from './draft';

export type ActionDefinitionDraft = {
  name: string;
  description: string;
  input_schema: string;
  output_schema: string;
  required_secrets: string[];
};

type ErrorKind = 'name' | 'description' | 'input_schema' | 'output_schema' | 'required_secrets'
  | 'duplicate' | 'invalid_definitions' | 'limit';

export class ActionDefinitionDraftError extends Error {
  constructor(readonly kind: ErrorKind) {
    super(kind);
    this.name = 'ActionDefinitionDraftError';
  }
}

type Entry = { draft: ActionDefinitionDraft; start: number; end: number };
type Definitions = { entries: Entry[]; open: number; nullable: boolean };

function secretNames(value: unknown): value is string[] {
  return Array.isArray(value) && value.every(name => typeof name === 'string'
    && /^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$/.test(name)) && new Set(value).size === value.length;
}

function validName(name: unknown): name is string {
  return typeof name === 'string' && name.trim() !== '' && name.trim() === name;
}

function schema(source: string): Record<string, unknown> {
  return readJSONObject(source).value;
}

export function validateActionDefinition(draft: ActionDefinitionDraft): void {
  if (!validName(draft.name)) throw new ActionDefinitionDraftError('name');
  if (typeof draft.description !== 'string') throw new ActionDefinitionDraftError('description');
  if (!secretNames(draft.required_secrets)) throw new ActionDefinitionDraftError('required_secrets');
  for (const field of ['input_schema', 'output_schema'] as const) {
    try {
      if (schema(draft[field]).type !== 'object') throw new Error();
    } catch { throw new ActionDefinitionDraftError(field); }
  }
}

function definitions(source: string): Definitions {
  try {
    const value: unknown = JSON.parse(source);
    if (value === null) return { entries: [], open: skipJSONWhitespace(source, 0), nullable: true };
    if (!Array.isArray(value)) throw new Error();
    const open = skipJSONWhitespace(source, 0);
    const entries: Entry[] = [];
    const names = new Set<string>();
    let start = skipJSONWhitespace(source, open + 1);
    while (source[start] !== ']') {
      const end = jsonValueEnd(source, start);
      const raw = source.slice(start, end);
      const { value: definition, fields } = readJSONObject(raw);
      if (!validName(definition.name) || names.has(definition.name)
        || (definition.description !== undefined && typeof definition.description !== 'string')
        || !secretNames(definition.required_secrets ?? [])) throw new Error();
      function rawSchema(field: string): string {
        const span = fields.get(field);
        if (!span) throw new Error();
        const result = raw.slice(span.start, span.end);
        schema(result);
        return result;
      }
      const draft: ActionDefinitionDraft = {
        name: definition.name,
        description: definition.description as string | undefined ?? '',
        input_schema: rawSchema('input_schema'),
        output_schema: rawSchema('output_schema'),
        required_secrets: definition.required_secrets as string[] | undefined ?? [],
      };
      entries.push({ draft, start, end });
      names.add(draft.name);
      start = skipJSONWhitespace(source, end);
      if (source[start] === ',') start = skipJSONWhitespace(source, start + 1);
    }
    return { entries, open, nullable: false };
  } catch { throw new ActionDefinitionDraftError('invalid_definitions'); }
}

export function readActionDefinitions(source: string): ActionDefinitionDraft[] {
  // Reading checks representability; saving checks the edited schema's root type.
  // The API remains responsible for compiling schemas and mission references.
  return definitions(source).entries.map(entry => entry.draft);
}

function newDefinition(draft: ActionDefinitionDraft): string {
  const fields = [
    `"name": ${JSON.stringify(draft.name)}`,
    `"description": ${JSON.stringify(draft.description)}`,
    `"input_schema": ${draft.input_schema}`,
    `"output_schema": ${draft.output_schema}`,
  ];
  if (draft.required_secrets.length) fields.push(`"required_secrets": ${JSON.stringify(draft.required_secrets)}`);
  return `{\n    ${fields.join(',\n    ')}\n  }`;
}

export function writeActionDefinition(source: string, draft: ActionDefinitionDraft, originalName?: string): string {
  validateActionDefinition(draft);
  const { entries, open, nullable } = definitions(source);
  if (entries.length > 100) throw new ActionDefinitionDraftError('limit');
  if (originalName !== undefined) {
    if (draft.name !== originalName) throw new ActionDefinitionDraftError('name');
    const entry = entries.find(candidate => candidate.draft.name === originalName);
    if (!entry) throw new ActionDefinitionDraftError('invalid_definitions');
    let raw = source.slice(entry.start, entry.end);
    for (const field of ['description', 'input_schema', 'output_schema', 'required_secrets'] as const) {
      if (JSON.stringify(entry.draft[field]) === JSON.stringify(draft[field])) continue;
      const value = field === 'input_schema' || field === 'output_schema' ? draft[field] : JSON.stringify(draft[field]);
      raw = updateMissionDefinition(raw, field, value);
    }
    return source.slice(0, entry.start) + raw + source.slice(entry.end);
  }
  if (entries.some(entry => entry.draft.name === draft.name)) throw new ActionDefinitionDraftError('duplicate');
  if (entries.length >= 100) throw new ActionDefinitionDraftError('limit');
  const raw = newDefinition(draft);
  if (nullable) return `[\n  ${raw}\n]`;
  const last = entries.at(-1);
  const index = last?.end ?? open + 1;
  return source.slice(0, index) + `${last ? ',' : ''}\n  ${raw}` + source.slice(index);
}

export function removeActionDefinition(source: string, name: string): string {
  const { entries } = definitions(source);
  const index = entries.findIndex(entry => entry.draft.name === name);
  if (index === -1) return source;
  const current = entries[index];
  const previous = entries[index - 1];
  const next = entries[index + 1];
  // Remove only this value and one separator; every other definition stays exact.
  const start = previous ? previous.end : current.start;
  const end = next && !previous ? next.start : current.end;
  return source.slice(0, start) + source.slice(end);
}
