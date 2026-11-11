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

> Build an agent that analyzes text and saves a summary.

The image includes Admin and spawner, plus Git, Go, Codex and browser tools for
installed agents. Only spawner starts automatically; other agents install on demand.

Type `/admin` to open [Admin](http://localhost:4275) and `/credentials` for the
API login. Completed operation rows link to their Admin details once all task
outcomes are known.

The named volume retains state when the container exits. Keep it when upgrading
or restarting Commander.

### Native: one executable

Download a [release binary](../../docs/installation.md#direct-downloads), then run:

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

Admin starts automatically. An Admin startup failure leaves the terminal and
local hosts running; `/admin serve` retries after you fix the issue.

### Headless all-in-one

Run the local hosts without Playground or a model API key:

```sh
./commander --all-in-one --headless
```

The process prints ready service addresses, including Admin when available, and
stays in the foreground.
Connect an [MCP client](../adapters/README.md#quick-start) to plan work and handle
model decisions. Stop with Ctrl+C
or SIGTERM; local results and credentials are retained under `~/.commander`.

If a coding-agent sandbox stops child processes when a shell command ends, keep
this command running in a long-lived tool session. Run `./commander status` and
`./commander setup --json` from another tool call, using the same `--state-dir PATH`
if supplied. You can also start Commander in a separate terminal. Background
`start` cannot override the sandbox's process lifetime.

Read credentials from another terminal:

```sh
./commander credentials api
./commander credentials mcp --raw
```

To connect Playground to the running local instance, launch `./commander` in a
second terminal. It discovers the endpoints and uses the saved local credentials.
Explicit connection settings and saved remote destinations take precedence.
Exiting this Playground leaves the headless hosts running.

Inspect the local instance without starting Playground:

```sh
./commander status
./commander status --json
```

Use the same `--state-dir PATH` for hosts, Playground, status and credential lookup
when choosing another profile.
For `--temporary`, supply `API_USERNAME`, `API_PASSWORD` and `MCP_TOKEN` before
starting if clients need access; generated temporary credentials are not saved.

### Background local hosts

Start Commander independently of your terminal or MCP client:

```sh
./commander start
./commander status --json
./commander stop
```

Start returns after the API, MCP and spawner are ready, with Admin served by default.
Use `--json` for machine-readable endpoints. Repeated start
uses the running instance and leaves its configuration unchanged. Use
`--state-dir PATH` consistently to manage another profile.

On hosts that allow detached processes, closing Playground or your MCP client
leaves the hosts running. For restricted coding-agent shells, use
[headless all-in-one](#headless-all-in-one) in a long-lived tool session instead.
Stop waits for host and agent cleanup and retains saved results, credentials and
encryption keys. It can also stop a foreground all-in-one instance using that
profile.
Startup failures point to `~/.commander/startup.log`; host details are in
`~/.commander/local/hosts.log`.

Background hosts always use persistent state. They do not start automatically
after reboot. Local MCP
defaults to `http://127.0.0.1:4273/mcp`, keeping the same address across restarts.
The API defaults to `http://127.0.0.1:4274`.

Choose different fixed ports when another deployment already uses those ports:

```sh
MCP_LISTEN_ADDRESS=127.0.0.1:5273 API_LISTEN_ADDRESS=127.0.0.1:5274 ./commander start
```

Use the same listener settings on each restart. A busy configured port fails
startup; Commander does not silently choose another. Port `0` explicitly requests
an available port and is useful for isolated tests.

### Connect a coding agent

Start Commander and get its verified MCP connection details:

```sh
./commander start
./commander setup --json
```

If the coding-agent shell ends background processes, keep
[headless all-in-one](#headless-all-in-one) running and call setup from another
tool session.

Ask your coding agent:

> Connect your MCP client to Commander using these connection details. Show me
> the configuration change for approval, preserve my existing settings, and
> reload the client if needed. Then call `list_missions` through your MCP client
> to verify the connection.

The output describes the Streamable HTTP endpoint, bearer authentication and
verified tool count. Your agent translates those details into its client's
configuration. Commander supports compatible [MCP clients](https://modelcontextprotocol.io/specification/latest/basic/transports)
without a separate installer for each one.

For a client that reads its token from the environment:

```sh
export COMMANDER_MCP_TOKEN="$(./commander credentials mcp --raw)"
```

In PowerShell, set the token with
`$env:COMMANDER_MCP_TOKEN = (.\commander.exe credentials mcp --raw)`.
For another local profile, use the same `--state-dir PATH` on each Commander
command. Keep token values out of chat and configuration previews.

For remote Commander, supply `--mcp-url https://your-host/mcp` and its
`COMMANDER_MCP_TOKEN`. Setup does not reuse local credentials for that address.
Setup reads and verifies the server connection without changing client settings.
Its connection description is not a client configuration file, and server
verification does not prove that your client has loaded or called the tools.
The default local MCP address stays the same across restarts. Rerun setup when
changing listener settings or explicitly using port `0`.

### Saved state

Local and remote Playground sessions save settings, full chat history and entered
keys under `~/.commander`. All-in-one additionally keeps its database, artifacts and agent
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
recognized profiles are reused. One local backend and one Playground client can
share a profile; a second backend or client is rejected.

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

OpenAI responses use a 16,384-token output budget, including reasoning. Explicit
rate-limit rejections before model output retry up to twice, respecting the
provider's retry delay, with at most 30 seconds of cumulative waiting. Stop
cancels the wait. For long conversations, use `/clear`, then `/watch <id>` to
resume review from the saved operation with fresh context.

### Build from source

With Go 1.26.7+ and Node 24+, run from the repository root:

```sh
npm --prefix apps/admin/ui/frontend ci
npm --prefix apps/admin/ui/frontend run build -- --outDir ../backend/static/dist --emptyOutDir
go build -tags embed_ui -o commander ./apps/playground/cmd/commander && ./commander --all-in-one
```

For a Go-only build, omit the frontend steps and `-tags embed_ui`. Missing Admin
assets produce a notice without stopping the local hosts. To serve Admin later,
build the frontend and set
`UI_STATIC_DIR` to its output directory.

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
Use `/secrets` to reopen it, or **Secrets** in Admin's sidebar.
Values go directly to the API, outside the model. Running tasks keep their
deadline while waiting. One-time requests show their operation, agent and expiry;
their values are never saved for reuse. After sending, wait for **Received by agent**.
Use **R** to check an uncertain submission's status; reopening never resends it.
See [secrets](../../docs/features/secrets.md).

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
| Model provider/name | `COMMANDER_LLM_PROVIDER`, `COMMANDER_LLM_MODEL` | `--provider`, `--model` |
| Model API base URL | `COMMANDER_LLM_BASE_URL` | `--base-url` |
| Redacted error details | `COMMANDER_DEBUG=true` | `--debug` |
| Disable colors | `NO_COLOR` | `--no-color` |

Flags override environment settings. Provider keys can use `OPENAI_API_KEY`,
`ANTHROPIC_API_KEY`, `GEMINI_API_KEY` (or `GOOGLE_API_KEY`), `XAI_API_KEY`,
`DEEPSEEK_API_KEY` or `COMMANDER_LLM_API_KEY` for Other.
Gemini keys are saved as `GEMINI_API_KEY`, including keys read from `GOOGLE_API_KEY`.

Connecting to an existing Commander does not start another Admin server.
The standalone Admin link defaults to `http://localhost:4275`; setting
`--admin-url` selects the existing Admin address, not a new listener.
Set the API URL/login for Admin
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
Playground automatically revisits watched questions when model fallback becomes
available. `/stop` pauses review; `/watch <operation-id>` resumes it. See
[Questions](../../docs/features/questions.md) for timing, action waits and other MCP clients.

Tool calls update their pending line when they complete or fail. The progress
area shows up to three recently updated operations; `+N more · /operations`
points to the full list. Watching and model review continue for hidden operations.

Terminal operations show a static marker and list unknown task outcomes.
Playground keeps watching for late results and artifacts. `/stop` pauses model
review while operation updates continue; `/watch <operation-id>` resumes review.
See [Operations](../../docs/features/operations.md) for completion, settlement and recovery.

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

Docker AIO starts Admin automatically.

[Contributor guidance](AGENTS.md) · [Test setup](../../framework/tests/README.md) ·
[MCP workflow](../adapters/README.md#operation-workflow)

Explore [11 practical Commander use cases](https://github.com/defos-ai/commander/blob/main/docs/use-cases.md):
security operations, authorized security testing, compliance monitoring,
multi-agent development, incident response, prospect research and outreach,
competitive intelligence, data processing, ticket resolution, infrastructure
monitoring and long-running research for universities and R&D centers.
