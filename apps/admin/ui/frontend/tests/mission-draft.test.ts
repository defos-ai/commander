import { describe, expect, it } from 'vitest';
import { initialMissionDefinition, readMissionDraft, updateMissionDefinition, writeMissionDraft } from '../src/missions/draft';

describe('mission form draft', () => {
  it('opens a blank draft and nullable lists without adding properties to its JSON', () => {
    expect(readMissionDraft(initialMissionDefinition)).toMatchObject({
      name: '', objectives: [''], required_actions: [], optional_actions: [], starting_actions: [],
      input_schema: '{}', output_schema: '{}', rules: '{}', steps: '[]', action_definitions: '[]',
    });
    const source = '{"name":"draft","objectives":["First", ""], "required_actions":null,"starting_actions":null,"rules":null}';
    expect(readMissionDraft(source)).toMatchObject({
      name: 'draft', objectives: ['First', ''], required_actions: [], starting_actions: [], rules: 'null',
    });
    expect(updateMissionDefinition(source, 'name', '"edited"')).toBe(source.replace('"draft"', '"edited"'));
    expect(writeMissionDraft(source, readMissionDraft(source))).toBe(source);
  });

  it('retains exact advanced fields, unknown properties, string escapes and numeric tokens through form edits', () => {
    const source = String.raw`{
  "name": "old",
  "objectives": ["Build \\ agents", "Explain \"results\""],
  "required_actions": ["build-agent"],
  "optional_actions": ["publish-source"],
  "starting_actions": ["build-agent"],
  "input_schema": {"minimum":9007199254740993,"multipleOf":0.10000000000000001,"default":1e+100},
  "output_schema": {"description":"\u0061\/b"},
  "rules": {"same-agent":{"for_actions":["build-agent"],"future":9007199254740995}},
  "steps": [{"id":"build","objective":"Build it","future":{"text":"brackets } ] , \\\""}}],
  "action_definitions": [{"name":"build-agent","input_schema":{"maximum":9007199254740997}}],
  "future_field": {"enabled":true,"large":9007199254740999}
}`;
    const before = readMissionDraft(source);
    expect(writeMissionDraft(source, before)).toBe(source);
    expect(before.input_schema).toBe('{"minimum":9007199254740993,"multipleOf":0.10000000000000001,"default":1e+100}');
    expect(before.output_schema).toBe(String.raw`{"description":"\u0061\/b"}`);
    let changed = updateMissionDefinition(source, 'name', '"new"');
    changed = updateMissionDefinition(changed, 'objectives', JSON.stringify(['Build it', 'Report it']));
    changed = updateMissionDefinition(changed, 'required_actions', '["build-agent","check-result"]');
    expect(changed).toBe(source.replace('"name": "old"', '"name": "new"')
      .replace(String.raw`["Build \\ agents", "Explain \"results\""]`, '["Build it","Report it"]')
      .replace('"required_actions": ["build-agent"]', '"required_actions": ["build-agent","check-result"]'));
    const after = readMissionDraft(changed);
    for (const field of ['input_schema', 'output_schema', 'rules', 'steps', 'action_definitions'] as const) {
      expect(after[field]).toBe(before[field]);
    }
    expect(changed).toContain('"large":9007199254740999');
    expect(writeMissionDraft(source, { ...before, name: 'new', objectives: ['Build it', 'Report it'],
      required_actions: ['build-agent', 'check-result'] })).toBe(changed);
  });

  it('updates escaped property names and inserts absent fields without replacing nested names', () => {
    const source = String.raw` { "n\u0061me":"old", "objectives":["Build"],"future":{"name":"old"}} `;
    const renamed = updateMissionDefinition(source, 'name', '"new"');
    expect(renamed).toBe(source.replace(':"old"', ':"new"'));
    const withActions = updateMissionDefinition(renamed, 'optional_actions', '["publish-source"]');
    expect(readMissionDraft(withActions).optional_actions).toEqual(['publish-source']);
    expect(withActions).toContain('"future":{"name":"old"}');
    expect(JSON.parse(updateMissionDefinition(' { } ', 'quoted"key', 'false'))).toEqual({ 'quoted"key': false });
  });

  it.each([
    'not JSON', 'null', '[]', '42', '{}',
    '{"name":"draft","objectives":[]}',
    '{"name":"draft","objectives":"Build"}',
    '{"name":"draft","objectives":["Build",42]}',
    '{"name":42,"objectives":["Build"]}',
    '{"name":"draft","objectives":["Build"],"required_actions":"run"}',
    '{"name":"draft","objectives":["Build"],"optional_actions":[null]}',
    '{"name":"draft","objectives":["Build"],"starting_actions":{}}',
    '{"name":"draft","objectives":["Build"],"input_schema":[]}',
    '{"name":"draft","objectives":["Build"],"rules":false}',
    '{"name":"draft","objectives":["Build"],"steps":{}}',
    '{"name":"draft","objectives":["Build"],"action_definitions":{}}',
    String.raw`{"name":"first","n\u0061me":"last","objectives":["Build"]}`,
  ])('rejects JSON that cannot be represented by the form without losing fields: %s', source => {
    expect(() => readMissionDraft(source)).toThrow();
  });

  it('rejects invalid replacements and ambiguous source properties before changing the draft', () => {
    expect(() => updateMissionDefinition(initialMissionDefinition, 'rules', '{')).toThrow();
    expect(() => updateMissionDefinition('{"name":"first","name":"last"}', 'name', '"new"')).toThrow();
    expect(() => updateMissionDefinition('[]', 'name', '"new"')).toThrow();
    const draft = readMissionDraft(initialMissionDefinition);
    expect(() => writeMissionDraft(initialMissionDefinition, { ...draft, input_schema: '{' })).toThrow();
    expect(() => writeMissionDraft(initialMissionDefinition, { ...draft, input_schema: '[]' })).toThrow();
    expect(() => writeMissionDraft(initialMissionDefinition, { ...draft, objectives: [] })).toThrow();
    expect(writeMissionDraft(initialMissionDefinition, { ...draft, output_schema: '{"maximum":9007199254740993}' }))
      .toContain('"output_schema": {"maximum":9007199254740993}');
  });
});
