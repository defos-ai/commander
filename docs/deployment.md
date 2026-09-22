# Local setup and configuration

Current setup uses Docker Compose and filesystem providers. Production deployment
instructions will follow when hosting support is available.

## Local Compose

From the repository root, start MCP, one controller, two web-quality agents, and the sample website:

```sh
docker compose up --build -d
```

Compose creates shared Docker volumes automatically. A short-lived `volume-init`
service sets their root directories' ownership to `1000:1000` before the
controller starts. All application containers run as this non-root user.
Filesystem paths, mounts, and initialization belong to Compose; application
images do not configure storage backends or create state directories.

Connect your client to `http://localhost:4273/mcp`; see [client setup](../apps/adapters/README.md#quick-start).
Use `docker compose logs -f` for logs and `docker compose down` to stop services.
Stopping services preserves state; `docker compose down --volumes` deletes it.
Loading a mission file upserts definitions and does not remove saved missions.
When switching from the old bundled agents, `docker compose up --build -d --remove-orphans`
also removes their old containers. For a completely fresh development setup,
`docker compose down --volumes --remove-orphans` deletes stored operations,
missions and artifacts before the next startup.
The sample website is visible at `http://localhost:8088/`. For a mission, use
`http://sample-website:8080/`, which is reachable from the agents. Browser setup
and an end-to-end checklist are in the [web quality guide](../apps/agents/web-quality/README.md).

## Configuration

| Variable | Purpose | Compose default |
| --- | --- | --- |
| `COMMANDER_MCP_PORT` | Published MCP port | `4273` |
| `COMMANDER_WEBSITE_PORT` | Published sample website port | `8088` |
| `BUS_URL` | Message bus | `file:///state/bus` |
| `STORAGE_URL` | Artifacts and reports | `file:///state/storage` |
| `DATABASE_URL` | Controller and adapter database | `file:///state/database` |
| `LOG_LEVEL` / `LOG_FORMAT` | Logging | `info` / `json` |

Application CLI flags override environment values. Native defaults use
`./state/bus`, `./state/storage`, and `./state/database`; run each binary with
`--help` for available flags. MCP and the controller share all three locations;
agents share the bus and storage. See [providers](../providers/README.md) for supported URLs.

## State

| Docker volume | Container path | Shared by |
| --- | --- | --- |
| `bus` | `/state/bus` | Controller, MCP, agents |
| `storage` | `/state/storage` | Controller, MCP, agents |
| `database` | `/state/database` | Controller, MCP |

These volumes hold messages, artifacts/reports, and database records respectively.
Compose prefixes their names with the project name, normally `commander`.
Agents do not mount the database volume.

Compose supplies the example mission file as a read-only config and passes `--with-missions`
to upsert `review-website` at controller startup. Without that flag, no missions are loaded; see
[controller setup](../apps/controllers/README.md#quick-start). The controller populates
`actions.json` from agent announcements. Native runs still use local `state/`
directories, which are ignored by Git; Compose does not use those directories.

See the [architecture overview](overview.md#state-and-restart-recovery) for
component responsibilities, restart behavior, and current scaling limits.
