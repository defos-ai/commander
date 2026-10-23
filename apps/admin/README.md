# Commander Admin

Build the CLI from the repository root:

```sh
go build -o bin/commander-admin ./apps/admin/cmd/commander-admin
```

Set `COMMANDER_API_PASSWORD` to your API password. The API URL defaults to
`http://localhost:4274` and the username to `commander`; override them with
`COMMANDER_API_URL` and `COMMANDER_API_USERNAME`.

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

For browser administration, follow the [Admin UI setup](ui/README.md).
