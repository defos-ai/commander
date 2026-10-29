# Playground

Use a terminal conversation to start complex missions, review execution evidence
and provide operator input as agents work. Connect to an existing Commander
deployment for operational work, or run a local all-in-one session.

## Quick start

### Docker (recommended)

```sh
docker run --name commander --rm -it -v commander-state:/home/commander/.commander -p 127.0.0.1:4275:4275 ghcr.io/defos-ai/commander-aio:stable
```

Choose a provider, enter its API key and ask:

> Install the web agent, then review defos.ai.

The image includes Admin and spawner, plus Git, Go, Codex and browser tools for
installed agents. Only spawner starts automatically; other agents install on demand.

Type `/admin` to open [Admin](http://localhost:4275) and `/credentials` for the
API login. Completed operation rows link to their Admin details once all task
outcomes are known.

The named volume retains state when the container exits. Keep it when upgrading
or restarting Commander.

### Native: one executable

Download a [release binary](../../README.md#binary), then run:

```sh
./commander --all-in-one
```

On Windows: `.\commander.exe --all-in-one`.

For longer builds, increase the controller's task observation window (default `15m`):

```sh
TASK_TIMEOUT=30m ./commander --all-in-one
```

In Docker, pass `-e TASK_TIMEOUT=30m`. This affects newly dispatched tasks;
expiry marks an unknown outcome and does not stop the agent.

Type `/admin serve` to start Admin, or pass `--serve-admin-ui` at launch.
The terminal session works without it.

### Saved state

Both modes save settings, full chat history and keys entered in Playground under
`~/.commander`. All-in-one additionally keeps its database, artifacts and agent
workspaces. Reopening restores conversation context; use `/watch <id>`
to resume operation review. Exiting still stops local hosts and spawned agents;
saved state does not keep their processes running. Install agents again when
needed after restart.

Use `--state-dir PATH` for another profile, or `--temporary` to skip local saves:

```sh
./commander --all-in-one --temporary
```

Temporary mode removes all-in-one results on exit; remote server data remains
managed by that server. An unwritable or unfamiliar state directory stops startup
with instructions to choose another directory or use `--temporary`. Existing
recognized profiles are reused. One Playground process may use a profile at a time.

Provider keys entered here are remembered encrypted on this machine, bound to
their provider and API base URL. Alternatively, set the provider's environment
variable, such as `OPENAI_API_KEY`, before launch; it takes precedence over the
remembered key. Playground does not retrieve key values from Commander's API.
Remote API passwords and MCP tokens must be supplied again or through environment
variables. Private `/credentials` output is excluded from saved chat history.
Saved conversations contain your messages and tool results: protect the profile
and its backups. Keep the entire profile, including encryption keys, when restoring.

Explicit flags override nonempty environment values, then saved settings.
Selecting a different provider does not reuse the previous provider's model or
custom base URL. `/clear` clears model context while keeping the visible transcript.

### Build from source

With Go 1.26.7+ and Node 24+, run from the repository root:

```sh
npm --prefix apps/admin/ui/frontend ci
npm --prefix apps/admin/ui/frontend run build -- --outDir ../backend/static/dist --emptyOutDir
go build -tags embed_ui -o commander ./apps/playground/cmd/commander && ./commander --all-in-one
```

For a Go-only build, omit the frontend steps and `-tags embed_ui`. To serve Admin
later, build the frontend and set `UI_STATIC_DIR` to its output directory.

Native agents use tools installed on your host: Git for checkout/publication,
Go and Codex for builds, and [Chromium](https://github.com/defos-ai/commander-browser-agent#connect-to-commander)
for browser actions. Missing tools appear in agent readiness.

### Docker Compose

From the repository root:

```sh
docker compose up --build --wait && docker compose run --build --rm --no-deps playground
```

Playground connects automatically. Spawner starts with the stack; builder is
[optional](../../docs/deployment.md#optional-builder). Additional agents run as
sibling containers. `/exit` leaves hosts and results running.
Follow [deployment teardown](../../docs/deployment.md#stopping-and-resetting-agents)
to stop the stack and its spawned agents.

Compose supplies the saved MCP token and API login automatically; `/admin`
commands and secret forms need no extra login. Browser Admin still asks for it:
use `docker compose exec api commander-api credentials`.
Bundled missions are added automatically without replacing saved definitions.
See [deployment](../../docs/deployment.md) for ports, password rotation and overrides.

### Installing agents

Ask in chat to install an agent, using its source name or GitHub repository:

> Install https://github.com/defos-ai/commander-browser-agent

Omit the version or use `@stable` for the promoted Latest release; append
`@v0.2.0` to pin a version. Installation needs Internet access. Use packages
from authors you trust: native agents run as your OS user.

Installing an agent does not install host dependencies. Check its readiness
before starting a mission. See [agent sources](../../sources/README.md)
for available packages.

### Credentials

In all-in-one, type `/credentials` to see the API login and MCP token.
From another terminal, for the Docker session above:

```sh
docker exec commander commander credentials api
docker exec commander commander credentials mcp
```

Add `--raw` for only the password or token. Generated credentials are reused from
saved all-in-one state. `commander credentials api|mcp --state-dir PATH` reads an
alternate profile; temporary generated credentials are available only through
`/credentials`. Set `API_USERNAME`, `API_PASSWORD` or `MCP_TOKEN` before the first
start to supply your own; conflicting overrides on later starts are rejected.
Docker also needs the matching `-e VARIABLE` argument.

For agent secrets, enter values in the masked form that opens when needed.
**Tab** moves between fields, **Enter** advances or saves, and **Esc** postpones.
Use `/secrets` to reopen it, or **Agents → Manage secrets** in Admin.
Values go directly to the API, outside the model. Running tasks keep their
deadline while waiting.

After connecting, Playground saves the selected provider's key through the API
under its environment-variable name, such as `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`.
Existing saved values stay unchanged. Without an API login, use
`/admin secrets set NAME` to share a key with agents.

### Model setup

Choose a provider and model with **↑/↓** and **Enter**. Key prompts link to the
provider's API key page. **Other** supports an OpenAI-compatible base URL and model.
Messages and requested tool results go to that provider and incur its API charges.
Choose **Enter model name…** for an unlisted model; use `/back` to restart setup.

To connect to an existing MCP server:

```sh
./commander --mcp-url http://localhost:4273/mcp
```

Paste the MCP token when prompted, or set `COMMANDER_MCP_TOKEN` before launch.

| Setting | Environment | Flag |
| --- | --- | --- |
| MCP endpoint | `COMMANDER_MCP_URL` | `--mcp-url` |
| Local state directory | `COMMANDER_STATE_DIR` | `--state-dir` |
| Disposable client state | `COMMANDER_TEMPORARY=true` | `--temporary` |
| MCP token | `COMMANDER_MCP_TOKEN` | Masked prompt |
| API for Admin commands and agent secrets | `COMMANDER_API_URL` | `--api-url` |
| API login | `COMMANDER_API_USERNAME`, `COMMANDER_API_PASSWORD` | Local form |
| Admin browser address | `COMMANDER_ADMIN_URL` | `--admin-url` |
| Start Admin UI | — | `--serve-admin-ui` |
| Model provider/name | `COMMANDER_LLM_PROVIDER`, `COMMANDER_LLM_MODEL` | `--provider`, `--model` |
| Model API base URL | `COMMANDER_LLM_BASE_URL` | `--base-url` |
| Redacted error details | `COMMANDER_DEBUG=true` | `--debug` |
| Disable colors | `NO_COLOR` | `--no-color` |

Flags override environment settings. Provider keys can use `OPENAI_API_KEY`,
`ANTHROPIC_API_KEY`, `GEMINI_API_KEY` (or `GOOGLE_API_KEY`), `XAI_API_KEY`,
`DEEPSEEK_API_KEY` or `COMMANDER_LLM_API_KEY` for Other.
Gemini keys are saved as `GEMINI_API_KEY`, including keys read from `GOOGLE_API_KEY`.

The standalone Admin link defaults to `http://localhost:4275`; setting
`--admin-url` changes the link, not the server. Set the API URL/login for Admin
commands, or enter them in the local form when prompted. All-in-one supplies
its own API connection. Compose supplies the default internal API login; a custom
`COMMANDER_API_URL` needs that API's own login.

### Chat

| Command | Use it to… |
| --- | --- |
| `/help` | Show essential commands and a link to this reference. |
| `/status` | Read connection and queue status. |
| `/operations` | List saved operations. |
| `/credentials`, `/admin` | Get the all-in-one login or Admin link, including during setup. |
| `/admin serve` | Start Admin UI for this session. |
| `/admin help` | List Admin commands. |
| `/secrets` | Reopen the masked agent-secret form. |
| `/watch <operation-id>` | Follow work and resume model review. |
| `/run <operation-id> <action-name> <json-input>` | Queue an action through MCP. |
| `/cancel <operation-id>` | Cancel queued operation work. Dispatched tasks may finish. |
| `/answer <question-id> <answer>` | Answer a pending question with text or an option number. |
| `/stop` or **Ctrl+C** | Stop the reply, clear queued messages and pause model review. |
| `/clear` | Clear model context; keep the transcript and saved operations. |
| `/exit` or **Ctrl+D** | Exit; all-in-one hosts stop too. |
| `/back` | Restart setup, before connecting to Commander. |

The terminal help stays compact; this table includes the additional commands.

Answer operation questions in the **Operator input** panel or Admin's operation
**Questions** section. Playground sends panel and `/answer` submissions directly
to the API, using the same login as `/admin`; enter that login locally if prompted.
If submission cannot be confirmed, inspect the saved question before submitting again.
Questions stay open after their operator waiting window (120 seconds by default).
The model may answer after `fallback_at`; operators can answer throughout.
Playground automatically reviews watched questions when that window ends.
`/stop` pauses this review; `/watch <operation-id>` resumes it. Other MCP clients
schedule their own revisit using the same saved `fallback_at`.

Running actions can ask their own questions. Answer the existing question and
let that action resume; its task deadline continues while it waits. Avoid
starting replacement work for the same pending decision.

Tool calls update their pending line when they complete or fail. The progress
area shows up to three recently updated operations; `+N more · /operations`
points to the full list. Watching and model review continue for hidden operations.

`completed` means a final report was accepted; it may include limitations.
Terminal operations show a static marker and list unknown task outcomes.
Playground keeps watching for late results and artifacts. `/stop` pauses model
review while operation updates continue; `/watch <operation-id>` resumes review.

Scroll with the wheel, trackpad or **PgUp/PgDown**. Drag to select, then use your
terminal's copy command, such as **Ctrl+Shift+C** in GNOME Terminal, **Cmd+C** in
macOS Terminal, or the Copy menu. Live updates can change the selected cells.
Answer questions with **↑/↓** and **Enter**, or type your own answer.
**Tab** returns to chat.

Manage missions with `/admin missions list`, `/admin missions import FILE` or
`/admin missions export NAME`. Use `/admin secrets set NAME` to open
a masked value form. These commands call the API directly, outside the model.

For failures, use `--debug` for redacted details and inspect saved work before
retrying. Use `/watch` to resume review of an existing operation.

## Development and testing

Build the all-in-one image locally from the repository root:

```sh
docker build -f apps/playground/docker/Dockerfile.aio -t commander:aio .
```

Docker AIO starts Admin automatically; pass `--serve-admin-ui=false` to skip it.

[Contributor guidance](AGENTS.md) · [Test setup](../../framework/tests/README.md) ·
[MCP workflow](../adapters/README.md#operation-workflow)

Explore [11 practical Commander use cases](https://github.com/defos-ai/commander/blob/main/docs/use-cases.md):
security operations, authorized security testing, compliance monitoring,
multi-agent development, incident response, prospect research and outreach,
competitive intelligence, data processing, ticket resolution, infrastructure
monitoring and long-running research for universities and R&D centers.
