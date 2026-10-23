# Commander Admin

Build the CLI from the repository root:

```sh
go build -o bin/commander-admin ./apps/admin/cmd/commander-admin
```

Set `COMMANDER_API_PASSWORD` to your API password. The API URL defaults to
`http://localhost:4274` and the username to `commander`; override them with
`COMMANDER_API_URL` and `COMMANDER_API_USERNAME`.

## Missions

```sh
bin/commander-admin missions list
bin/commander-admin missions export build-agent > mission.json
bin/commander-admin missions validate mission.json
bin/commander-admin missions import mission.json --dry-run
bin/commander-admin missions import mission.json
```

Files accept one mission, an array, or JSON from `missions list --json`. Use `-`
for stdin and `--json` for structured results. Validation works without API access.

Imports preserve existing names and stop on the first failure, reporting completed,
uncertain and unattempted items. After an uncertain write, export that mission to
inspect saved state before submitting again. Use `missions --help` for options.

## Secrets

```sh
bin/commander-admin secrets requests
bin/commander-admin secrets list builder-agent
bin/commander-admin secrets set builder-agent CODEX_API_KEY
bin/commander-admin secrets delete builder-agent CODEX_API_KEY
```

`set` opens a hidden terminal prompt or reads piped/redirected input automatically:

```sh
printf '%s' "$CODEX_API_KEY" | bin/commander-admin secrets set builder-agent CODEX_API_KEY
```

Piped input preserves newlines. Values never appear in output. `--json` returns metadata.
Changes use the observed ID/version; inspect metadata after an uncertain write
before submitting again. Saving a value does not confirm agent readiness.

For browser administration, follow the [Admin UI setup](ui/README.md).
