package host

const MissionDefinition = `{
  "name": "Research / #1?",
  "objectives": ["Review actions that have not been implemented yet.", "Preserve ordered guidance for later execution."],
  "required_actions": ["inspect-domain", "summarize-domain"],
  "optional_actions": ["publish-review"],
  "starting_actions": ["inspect-domain"],
  "action_definitions": [{
    "name": "inspect-domain",
    "description": "Inspect a requested domain.",
    "input_schema": {"type":"object","properties":{"limit":{"type":"integer","maximum":9007199254740993},"scale":{"type":"number","default":1.2300e-6}}},
    "output_schema": {"type":"object","x-note":"\u0061"}
  }, {
    "name": "publish-review",
    "description": "Publish a review after operator consent.",
    "required_secrets": ["PUBLISH_TOKEN"],
    "input_schema": {"type":"object"},
    "output_schema": {"type":"object"}
  }],
  "rules": {
    "aggregate-results": {"for_actions": ["inspect-domain"]},
    "decide-next-actions": {
      "for_actions": ["summarize-domain"],
      "params": {"decision_by": "model"}
    },
    "same-agent": {"for_actions": ["inspect-domain", "summarize-domain"]}
  },
  "input_schema": {
    "type": "object",
    "properties": {"domain": {"$ref": "#/$defs/domain"}},
    "$defs": {"domain": {"type": "string", "minLength": 1}},
    "required": ["domain"],
    "additionalProperties": false,
    "x-purpose": "Schema extensions belong to the mission author."
  },
  "output_schema": {
    "type": "object",
    "properties": {"summary": {"type": "string"}},
    "required": ["summary"]
  }
}`
