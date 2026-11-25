# Release notes v0.9.0

## Commander v0.9.0

Test mission flows without a model and manage reusable mission definitions
through their full lifecycle.

- Add `framework/testing/missiontest` for Go tests against a running Commander
  API, controller and agents. Import mission definitions, start operations,
  assign actions, supply decisions and answers, and inspect saved results and
  reports. Artifact downloads verify size and SHA-256. Tests supply decisions
  explicitly; agents that call models still need local substitutes.
- Mission test cleanup cancels only operations created by that driver and waits
  for their saved task outcomes and matching agent work. Failed or uncertain
  requests require saved-state inspection before retrying.
- Delete reusable missions through `DELETE /api/v1/missions/{mission_name}`,
  MCP `delete_mission`, or **Delete mission** in Admin. API success returns an
  empty `204`; an absent name returns `404 mission_not_found`. Existing
  operations keep their mission snapshots, results, artifacts and reports and
  can continue running. Admin requires confirmation and saved-state inspection
  before retrying an uncertain deletion.
- Mission updates now replace existing definitions atomically, so an edit
  racing with deletion cannot recreate the deleted name.
- Agent source discovery includes private Passive Recon, Active Recon and
  Inspector packages from `defos-ai/recon-agents`. These entries do not install
  agents or grant access to private releases.

Custom implementations of `missions.MissionStore` must add `Update` and `Delete`.
Both return `ErrUnknownMission` for an absent name; `Update` must not create a
definition. `Upsert` remains available for intentional imports. This release
does not change saved mission or operation formats, and requires no saved-state
migration. Bundled missions can be restored when default-mission seeding runs
again. Agent action inputs are unchanged.

[Mission test helper](https://github.com/defos-ai/commander/blob/v0.9.0/framework/testing/missiontest/README.md) ·
[Mission management](https://github.com/defos-ai/commander/blob/v0.9.0/docs/features/missions.md) ·
[Full changelog](https://github.com/defos-ai/commander/compare/v0.8.0...v0.9.0)

## Commander builder agent v0.9.0

- Align Commander module requirements with v0.9.0.
- Existing build, publication and release action inputs, workspace sessions and
  managed-secret behavior remain unchanged. No workspace migration is required.
- Agent authors can use the new `framework/testing/missiontest` helper to test
  generated missions against a running test stack without a decision-making
  model. Models called inside an agent still need local substitutes.

Package: `github.com/defos-ai/commander/apps/agents/builder@v0.9.0`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/builder/v0.9.0/apps/agents/builder/README.md) ·
[Mission tests](https://github.com/defos-ai/commander/blob/v0.9.0/framework/testing/missiontest/README.md)

## Commander spawner agent v0.9.0

- Align Commander module requirements with v0.9.0.
- Existing spawn/inspect action inputs, runtime selection and saved launch
  checkpoints remain unchanged. No checkpoint migration is required.
- The shared source list adds private Passive Recon, Active Recon and Inspector
  package references. Installing them still requires access to their private
  GitHub releases and any selected private container images.

Package: `github.com/defos-ai/commander/apps/agents/spawner@v0.9.0`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/spawner/v0.9.0/apps/agents/spawner/README.md) ·
[Agent sources](https://github.com/defos-ai/commander/blob/v0.9.0/sources/README.md)
