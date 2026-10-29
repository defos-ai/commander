# Commander Admin

Manage mission definitions, shared secrets and runtime sessions for operational
setup and maintenance. Use the CLI for repeatable administration, and serve the
browser Admin UI for operator review of distributed agent work.

Build the CLI from the repository root:

```sh
go build -o bin/commander-admin ./apps/admin/cmd/commander-admin
```

For API commands, set `COMMANDER_API_PASSWORD` to your API password.
The API URL defaults to
`http://localhost:4274` and the username to `commander`; override them with
`COMMANDER_API_URL` and `COMMANDER_API_USERNAME`.

## Missions

```sh
bin/commander-admin missions list
bin/commander-admin missions export build-agent > mission.json
bin/commander-admin missions validate mission.json
bin/commander-admin missions import mission.json --dry-run
bin/commander-admin missions import mission.json
bin/commander-admin missions update mission.json
bin/commander-admin missions seed
```

Files accept one mission, an array, or JSON from `missions list --json`. Use `-`
for stdin and `--json` for structured results. Validation works without API access.
File paths use the Admin or Playground process's filesystem. In Docker, mount
the file; see [Compose imports](../../docs/deployment.md#import-missions).
`seed` adds bundled missions without replacing saved definitions; use `--dry-run`
to preview it. All-in-one and Compose run it automatically after the API is ready.

Imports preserve existing names and stop on the first failure, reporting completed,
uncertain and unattempted items. After an uncertain write, export that mission to
inspect saved state before submitting again. Use `missions --help` for options.

To edit a mission, export it, change its objectives, actions, rules or schemas,
then run `missions update mission.json`. Update requires exactly one definition
under its existing name and affects future operations; existing operations keep
their saved definition. Names cannot be changed by an update.

## Secrets

Secrets are shared by name across Commander. Supply a value once for all agents
that request it.

```sh
bin/commander-admin secrets requests
bin/commander-admin secrets list
bin/commander-admin secrets set OPENAI_API_KEY
bin/commander-admin secrets delete OPENAI_API_KEY
```

`set` opens a hidden terminal prompt or reads piped/redirected input automatically:

```sh
printf '%s' "$OPENAI_API_KEY" | bin/commander-admin secrets set OPENAI_API_KEY
```

Piped input preserves newlines. Values never appear in output. `--json` returns metadata.
Changes use the observed ID/version; inspect metadata after an uncertain write
before submitting again. Saving a value does not confirm agent readiness.

## Sessions

```sh
bin/commander-admin sessions list
bin/commander-admin sessions list --owner-agent-id browser-agent-1234abcd
bin/commander-admin sessions inspect SESSION_ID
bin/commander-admin sessions close SESSION_ID
```

Use `verify` to check a resource through its owner. `abandon` releases a lost
session without confirming closure. All commands support `--json`.
After a failed or uncertain control, inspect saved state before submitting again.

## Admin UI

```sh
bin/commander-admin serve --static-dir apps/admin/ui/frontend/dist
```

Build the frontend first, or use a binary with embedded assets; see
[Admin UI setup](ui/README.md#native-server). Open [localhost:4275](http://localhost:4275)
and connect to your API in the browser. `serve` needs no `COMMANDER_API_*` settings.

The `commander-admin` Docker image starts the UI by default. Pass `missions`,
`secrets` or `sessions` after the image name to run a CLI command instead.

In Playground, use `/admin help` for the same commands and `/admin serve` to
start the UI. Secret values use a local masked form.

Explore [11 practical Commander use cases](https://github.com/defos-ai/commander/blob/main/docs/use-cases.md):
security operations, authorized security testing, compliance monitoring,
multi-agent development, incident response, prospect research and outreach,
competitive intelligence, data processing, ticket resolution, infrastructure
monitoring and long-running research for universities and R&D centers.
