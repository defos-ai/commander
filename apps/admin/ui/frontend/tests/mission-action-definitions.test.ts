import { describe, expect, it } from 'vitest';
import {
  readActionDefinitions, removeActionDefinition, validateActionDefinition, writeActionDefinition,
  type ActionDefinitionDraft,
} from '../src/missions/action-definitions';

const draft: ActionDefinitionDraft = {
  name: 'inspect-service', description: 'Inspect a service',
  input_schema: '{"type":"object"}', output_schema: '{"type":"object"}', required_secrets: [],
};

describe('mission action definition drafts', () => {
  it('appends a contract without rewriting existing schemas, escapes or unknown fields', () => {
    const source = String.raw` [ {"name":"existing","description":"\u0061\/b","input_schema":{"type":"object","maximum":9007199254740993},"output_schema":{"type":"object"},"future":{"amount":1.00000000000000001}} ] `;
    const raw = String.raw`{"type":"object","properties":{"value":{"const":9007199254740995},"path":{"const":"a\\b\u0063"}}}`;
    const result = writeActionDefinition(source, { ...draft, input_schema: raw, required_secrets: ['SERVICE_TOKEN'] });
    expect(result).toContain(source.slice(0, source.lastIndexOf('} ') + 1));
    expect(result).toContain(raw);
    expect(readActionDefinitions(result)[1]).toEqual({ ...draft, input_schema: raw, required_secrets: ['SERVICE_TOKEN'] });
    expect(JSON.parse(result)).toHaveLength(2);
    expect(readActionDefinitions('null')).toEqual([]);
    expect(readActionDefinitions(writeActionDefinition(' null ', draft))).toEqual([draft]);
  });

  it('edits only changed fields and retains the exact source for a no-op edit', () => {
    const source = String.raw`[
 {"name":"inspect-service","description":"\u0061\/b","input_schema":{"type":"object","minimum":9007199254740993},"output_schema":{"type":"object"},"future":{"text":"[},]\\\""}},
 {"name":"other","input_schema":{"type":"object"},"output_schema":{"type":"object"},"required_secrets":null}
]`;
    const existing = readActionDefinitions(source)[0];
    expect(writeActionDefinition(source, existing, existing.name)).toBe(source);
    expect(writeActionDefinition(source, { ...existing, description: 'New description' }, existing.name))
      .toBe(source.replace(String.raw`"description":"\u0061\/b"`, '"description":"New description"'));
    const changed = writeActionDefinition(source, { ...existing, required_secrets: ['TOKEN'] }, existing.name);
    expect(changed).toContain('"minimum":9007199254740993');
    expect(readActionDefinitions(changed)[0].required_secrets).toEqual(['TOKEN']);
    const other = readActionDefinitions(source)[1];
    expect(other.description).toBe('');
    expect(writeActionDefinition(source, other, other.name)).toBe(source);
  });

  it('removes first, middle, last or sole definitions without rewriting surviving values', () => {
    const values = ['first', 'middle', 'last'].map(name => String.raw`{"name":"${name}","input_schema":{"type":"object","const":9007199254740993},"output_schema":{"type":"object","const":"\u0061"}}`);
    const source = ` [\n ${values.join(',\n ')}\n] `;
    for (const name of ['first', 'middle', 'last']) {
      const result = removeActionDefinition(source, name);
      expect(readActionDefinitions(result).map(value => value.name)).toEqual(['first', 'middle', 'last'].filter(value => value !== name));
      for (const value of values.filter(value => !value.includes(`"name":"${name}"`))) expect(result).toContain(value);
    }
    expect(JSON.parse(removeActionDefinition(`[ ${values[0]} ]`, 'first'))).toEqual([]);
    expect(removeActionDefinition(source, 'absent')).toBe(source);
    expect(removeActionDefinition(' null ', 'absent')).toBe(' null ');
  });

  it('requires an explicit edit identity and prevents renaming, duplicates and excess definitions', () => {
    const source = writeActionDefinition('[]', draft);
    expect(() => writeActionDefinition(source, draft)).toThrow('duplicate');
    expect(() => writeActionDefinition(source, { ...draft, name: 'renamed' }, draft.name)).toThrow('name');
    expect(() => writeActionDefinition(source, draft, 'absent')).toThrow('name');
    expect(() => writeActionDefinition('[]', draft, draft.name)).toThrow('invalid_definitions');
    const full = `[${Array.from({ length: 100 }, (_, index) => JSON.stringify({
      name: `action-${index}`, input_schema: { type: 'object' }, output_schema: { type: 'object' },
    })).join(',')}]`;
    expect(() => writeActionDefinition(full, draft)).toThrow('limit');
    expect(readActionDefinitions(writeActionDefinition(removeActionDefinition(full, 'action-0'), draft))).toHaveLength(100);
  });

  it('reports field errors without imposing extra naming or description constraints', () => {
    validateActionDefinition({ ...draft, name: 'Readable action: inspect/service', description: '', required_secrets: ['_TOKEN', 'service.token-1', '0token'] });
    for (const name of ['', ' ', ' leading', 'trailing ']) {
      expect(() => validateActionDefinition({ ...draft, name })).toThrow('name');
    }
    for (const input_schema of ['{', 'null', '[]', '{}', '{"type":["object"]}', '{"type":"string"}']) {
      expect(() => validateActionDefinition({ ...draft, input_schema })).toThrow('input_schema');
    }
    expect(() => validateActionDefinition({ ...draft, output_schema: 'false' })).toThrow('output_schema');
    for (const required_secrets of [[''], [' TOKEN'], ['-TOKEN'], ['token/value'], ['TÖKEN'], ['x'.repeat(129)], ['TOKEN', 'TOKEN']]) {
      expect(() => validateActionDefinition({ ...draft, required_secrets })).toThrow('required_secrets');
    }
  });

  it('refuses malformed or ambiguous existing definitions instead of overwriting them', () => {
    const valid = JSON.stringify({ name: 'inspect-service', input_schema: { type: 'object' }, output_schema: { type: 'object' } });
    const sources = ['{', '{}', '[null]', '["action"]', '[{}]', `[${valid},${valid}]`,
      String.raw`[{"name":"a","n\u0061me":"b","input_schema":{},"output_schema":{}}]`,
      '[{"name":"a","input_schema":[],"output_schema":{}}]',
      '[{"name":"a","description":42,"input_schema":{},"output_schema":{}}]',
      '[{"name":"a","required_secrets":["TOKEN","TOKEN"],"input_schema":{},"output_schema":{}}]'];
    for (const source of sources) {
      expect(() => readActionDefinitions(source)).toThrow('invalid_definitions');
      expect(() => writeActionDefinition(source, draft)).toThrow('invalid_definitions');
      expect(() => removeActionDefinition(source, 'a')).toThrow('invalid_definitions');
    }
    const repairable = '[{"name":"inspect-service","input_schema":{},"output_schema":{}}]';
    expect(readActionDefinitions(writeActionDefinition(repairable, draft, draft.name))[0]).toEqual(draft);
  });
});
