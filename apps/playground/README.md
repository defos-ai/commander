# Playground

Chat with a model to run Commander missions and review results. Start a temporary
all-in-one session, or connect to existing hosts.

## Quick start

### Docker (recommended)

```sh
docker run --name commander --rm -it -p 127.0.0.1:4275:4275 ghcr.io/defos-ai/commander-aio:stable
```

Choose a provider, enter its API key and ask:

> Install the web agent, then review defos.ai.

The image includes Admin, builder and spawner, plus the tools needed to build
agents and run browser checks. Other agents install on demand.

Type `/admin` to open [Admin](http://localhost:4275) and `/credentials` for the
API login. Completed operation rows link to their Admin details.

**All-in-one deletes results, artifacts, downloaded agents and builder workspaces
on exit.** Use Compose to retain work.

### Native: one executable

Download a [release binary](../../README.md#binary), then run:

```sh
./commander --all-in-one
```

On Windows: `.\commander.exe --all-in-one`.

To build from source with Go 1.26.7+ and Node 24+, run from the repository root:

```sh
npm --prefix apps/admin/ui/frontend ci
npm --prefix apps/admin/ui/frontend run build -- --outDir ../backend/static/dist --emptyOutDir
go build -tags embed_ui -o commander ./apps/playground/cmd/commander && ./commander --all-in-one
```

For a Go-only build, omit `-tags embed_ui` and run with `--no-admin`.

Native agents use tools installed on your host: Git for checkout/publication,
Go and Codex for builds, and [Chromium](https://github.com/defos-ai/commander-browser-agent#connect-to-commander)
for browser actions. Missing tools appear in agent readiness.

### Docker Compose

From the repository root:

```sh
docker compose up --build --wait && docker compose run --build --rm --no-deps playground
```

Playground connects automatically. Builder and spawner start with the stack;
additional agents run as sibling containers. `/exit` leaves hosts and results
running. Follow [deployment teardown](../../docs/deployment.md#stopping-and-resetting-agents)
to stop the stack and its spawned agents.

Compose sets up persistent authentication and secret encryption. For API access,
retrieve the login with `docker compose exec api commander-api credentials`.
See [deployment](../../docs/deployment.md) for ports, state and overrides.

### Installing agents

Ask in chat to install an agent, using its catalog name or GitHub repository:

> Install https://github.com/defos-ai/commander-browser-agent

Omit the version or use `@stable` for the promoted Latest release; append
`@v0.2.0` to pin a version. Installation needs Internet access. Use packages
from authors you trust: native agents run as your OS user.

Installing an agent does not install host dependencies. Check its readiness
before starting a mission. See the [agent catalog](../../catalog/README.md)
for available packages.

### Credentials

In all-in-one, type `/credentials` to see the API login and MCP token.
From another terminal, for the Docker session above:

```sh
docker exec commander commander credentials api
docker exec commander commander credentials mcp
```

Add `--raw` for only the password or token. Generated credentials change each
session. Export `API_USERNAME`, `API_PASSWORD` or `MCP_TOKEN` before native
launch to override them; Docker also needs the matching `-e VARIABLE` argument.

For agent secrets, enter values in the masked form that opens when needed.
**Tab** moves between fields, **Enter** advances or saves, and **Esc** postpones.
Use `/secrets` to reopen it, or **Agents → Manage secrets** in Admin.
Values go directly to the API, outside the model. Running tasks keep their
deadline while waiting.

### Model setup

Choose a provider and model with **↑/↓** and **Enter**. Key prompts link to the
provider's API key page. **Other** supports an OpenAI-compatible base URL and model.
Messages and requested tool results go to that provider and incur its API charges.

To connect to an existing MCP server:

```sh
./commander --mcp-url http://localhost:4273/mcp
```

Paste the MCP token when prompted, or set `COMMANDER_MCP_TOKEN` before launch.

| Setting | Environment | Flag |
| --- | --- | --- |
| MCP endpoint | `COMMANDER_MCP_URL` | `--mcp-url` |
| MCP token | `COMMANDER_MCP_TOKEN` | Masked prompt |
| API for agent secrets | `COMMANDER_API_URL` | `--api-url` |
| API login | `COMMANDER_API_USERNAME`, `COMMANDER_API_PASSWORD` | Local form |
| Admin browser address | `COMMANDER_ADMIN_URL` | `--admin-url` |
| Model provider/name | `COMMANDER_LLM_PROVIDER`, `COMMANDER_LLM_MODEL` | `--provider`, `--model` |
| Model API base URL | `COMMANDER_LLM_BASE_URL` | `--base-url` |
| Redacted error details | `COMMANDER_DEBUG=true` | `--debug` |
| Disable colors | `NO_COLOR` | `--no-color` |

Flags override environment settings. Provider keys can use `OPENAI_API_KEY`,
`ANTHROPIC_API_KEY`, `GEMINI_API_KEY` (or `GOOGLE_API_KEY`), `XAI_API_KEY`,
`DEEPSEEK_API_KEY` or `COMMANDER_LLM_API_KEY` for Other.

The standalone Admin link defaults to `http://localhost:4275`; setting
`--admin-url` changes the link, not the server. Supply the API URL/login in the
secret form if not configured. All-in-one supplies its own connections.

### Chat

| Command | Use it to… |
| --- | --- |
| `/help`, `/status`, `/operations` | Read help, connection status or saved operations. |
| `/credentials`, `/admin` | Get the all-in-one login or Admin link, including during setup. |
| `/secrets` | Reopen the masked agent-secret form. |
| `/watch <operation-id>` | Follow work and resume model review. |
| `/run <operation-id> <action-name> <json-input>` | Queue an action through MCP. |
| `/cancel <operation-id>` | Cancel queued operation work. Dispatched tasks may finish. |
| `/stop` or **Ctrl+C** | Stop the reply; server-side work continues. |
| `/clear` | Clear the conversation. |
| `/exit` or **Ctrl+D** | Exit; all-in-one hosts stop too. |

Scroll with the wheel, trackpad or **PgUp/PgDown**. Drag to select, then use your
terminal's copy command, such as **Ctrl+Shift+C** in GNOME Terminal.
Answer questions with **↑/↓** and **Enter**, or type your own answer.
**Tab** returns to chat.

For failures, use `--debug` for redacted details and inspect saved work before
retrying. Use `/watch` to resume review of an existing operation.

## Development and testing

Build the all-in-one image locally from the repository root:

```sh
docker build -f apps/playground/docker/Dockerfile.aio -t commander:aio .
```

[Contributor guidance](AGENTS.md) · [Test setup](../../framework/tests/README.md) ·
[MCP workflow](../adapters/README.md#operation-workflow)
