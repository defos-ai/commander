# Release notes v0.6.1

## Commander v0.6.1

This documentation patch clarifies how to get started with Commander.

- Lead the quick start with a coding-agent installation prompt, followed by
  manual setup and the Playground Hacker News digest example.
- Use the Hacker News digest in the architecture overview, including topic
  selection and the saved `digest.md` and `stories.json` artifacts.
- Point the README's Ask ChatGPT prompt to Commander's documented use cases.
- Keep use-case links absolute so they work from published package documentation.

Runtime behavior is unchanged from v0.6.0. No saved-data migration or reset is
required.

[Getting started](https://github.com/defos-ai/commander/blob/v0.6.1/README.md#quick-start) ·
[Overview](https://github.com/defos-ai/commander/blob/v0.6.1/docs/overview.md) ·
[Full changelog](https://github.com/defos-ai/commander/compare/v0.6.0...v0.6.1)

## Commander builder agent v0.6.1

- Align internal module requirements with Commander v0.6.1. Builder capabilities
  and configuration are unchanged from v0.6.0.
- No workspace reset or credential changes are required.

Package: `github.com/defos-ai/commander/apps/agents/builder@v0.6.1`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/builder/v0.6.1/apps/agents/builder/README.md)

## Commander spawner agent v0.6.1

- Align internal module requirements with Commander v0.6.1. Spawner capabilities
  and configuration are unchanged from v0.6.0.
- No installation-state reset or credential changes are required.

Package: `github.com/defos-ai/commander/apps/agents/spawner@v0.6.1`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/spawner/v0.6.1/apps/agents/spawner/README.md)
