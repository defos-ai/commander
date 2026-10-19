# Playground

Playground is a terminal chat with one model per session. The model acts as
Commander through MCP: choose missions, assign work and review evidence while
the controller applies mission rules and agents execute actions. Connect to running
hosts or launch a temporary local demo.

## Quick start

### Docker (recommended)

For a temporary all-in-one session, build and run from the repository root:

```sh
docker build -f apps/playground/docker/Dockerfile.aio -t commander:aio .
docker run --name commander --rm -it -p 127.0.0.1:4275:4275 commander:aio
```

This image includes Admin, MCP, the API, a controller, builder and spawner agents,
with Chromium, fonts and Git. At startup, the spawner downloads and starts the
web and browser agents as native processes inside the container; it needs no
Docker socket. See [startup agents](#startup-agents) for package selection and
Internet requirements. Completed operations show a review link to Admin at
`http://localhost:4275`, with the local connection prefilled.
Type `/admin` for the general Admin link anytime.
If an operation shows **secrets needed**, open its Admin link and provide the
requested values under **Secrets**. Without a link, use `/admin` for setup help.
Keep secret values out of chat; work waits for agents to report ready.
Use `/credentials` for the API login and MCP token, or use another terminal:

```sh
docker exec commander commander credentials api
docker exec commander commander credentials mcp
```

Generated credentials change when the container restarts. Pass `-e API_USERNAME`,
`-e API_PASSWORD` or `-e MCP_TOKEN` to use values exported in your shell.
Secret management also works automatically: a separate encryption key stays in
memory for the temporary session. Pass `-e SECRETS_ENCRYPTION_KEY` to use your own.

Admin reaches the API inside the container; publishing API port `4274` is optional.
Add `-p 127.0.0.1:4274:4274` if other tools need API access. For a different
published port or host, pass `--admin-url http://YOUR_HOST:PORT` after the image
name to override the default link. To skip Admin, pass `--no-admin`.
Ports must be published when creating the container.
`/exit` stops everything and deletes the session's state. Add `-e OPENAI_API_KEY`
before the image name to use an exported key, or enter your provider's key during setup.

For separate hosts and retained results, use Compose:

```sh
docker compose up --build --wait &&
docker compose run --build --rm --no-deps playground
```

The `mcp-credentials` initializer saves a shared token in a named Docker volume.
MCP and Playground mount it read-only and export it to their process environments,
so Playground connects automatically. The token survives restarts. Set `MCP_TOKEN`
before starting Compose to replace it; `COMMANDER_MCP_TOKEN` overrides it for
Playground. To retrieve the running token:

```sh
docker compose exec mcp commander-mcp credentials
```

Add `--raw` to print only the token.
`/exit` closes chat; hosts and saved work remain.
Compose also configures `/admin` for `http://localhost:4275`, following
`COMMANDER_UI_PORT` when changed. Set `COMMANDER_ADMIN_URL` for another browser address.
API secret management is configured automatically with a separate persistent
[encryption key](../../docs/deployment.md#secret-encryption).
`docker compose down` stops the hosts. See
[deployment](../../docs/deployment.md) for connections and state.

To use the lightweight image with your own hosts:

```sh
docker build -f apps/playground/docker/Dockerfile -t commander:playground .
docker run --rm -it -e COMMANDER_MCP_TOKEN commander:playground --mcp-url https://YOUR_COMMANDER_HOST/mcp
```

### Native: one executable

[Native release binaries](../../docs/releases.md#native-binaries)
provide `commander` for Linux, macOS and Windows (amd64/arm64), without Go.
Download it as `commander` and run `chmod +x commander && ./commander --all-in-one`
(`.\commander.exe --all-in-one` on Windows), or build and run with Go 1.26.7+ and Node 24+
from the repository root:

```sh
npm --prefix apps/admin/ui/frontend ci
npm --prefix apps/admin/ui/frontend run build -- --outDir ../backend/static/dist --emptyOutDir
go build -tags embed_ui -o commander ./apps/playground/cmd/commander && ./commander --all-in-one
```

This starts Admin, MCP, the HTTP API, one controller, builder and spawner agents
using temporary FS state. The spawner downloads and starts web and browser agents
as child processes. The API listens at `http://127.0.0.1:4274` with username
`commander` and a random password for this session. MCP gets its own random token,
shared directly with Playground. During setup or chat, type `/credentials` to
view the API login and MCP token. Credentials stay local and are never sent to the
model. Set `API_USERNAME`, `API_PASSWORD` or `MCP_TOKEN` to override them; unset or
empty values use their defaults. Generated credentials change each session.
API and builder share a separate encryption key generated in memory for each
session. Set `SECRETS_ENCRYPTION_KEY` (64 hex characters) to supply your own;
an invalid key prevents startup. The session's saved secrets are deleted on exit.
Type `/admin` for a link with the local API URL and username prefilled; enter the
password shown by `/credentials`. Admin defaults to **http://127.0.0.1:4275**.
Use `/status` to see the running addresses.
`--no-admin` disables Admin; `UI_LISTEN_ADDRESS` changes its address.
If Admin cannot start, Playground continues with a notice.
`API_LISTEN_ADDRESS` changes the address (`127.0.0.1:0` chooses a free port).
The packaged executable embeds Admin and both website missions and needs no checkout
or Go installation. For a Go-only source build, omit `-tags embed_ui` and use
`--no-admin`, or set `UI_STATIC_DIR` to a built frontend directory.
Start with `review-website-no-browser` for links, SEO and technology checks.
`review-website-with-browser` adds screenshots and rendered accessibility, requiring
[Chromium](../agents/browser/README.md#native-agent).
The builder's `checkout-template` action requires Git on `PATH`; without it,
website missions still work. See the [builder guide](../agents/builder/README.md).
**Exiting stops these hosts and child agents, then deletes operations, reports,
artifacts, builder workspaces and downloaded executables.**
Use Compose to retain work.

`--all-in-one` chooses its own connections, ignoring `COMMANDER_MCP_URL`,
`COMMANDER_MCP_TOKEN`, `BUS_URL`, `STORAGE_URL` and `DATABASE_URL`; it cannot be
combined with `--mcp-url`. `--debug`
shows the temporary host log location, which is also deleted on exit.

### Startup agents

Native and Docker all-in-one sessions start these packages by default:

- `github.com/defos-ai/commander-web-agent@v0.2.0`
- `github.com/defos-ai/commander-browser-agent@v0.2.0`

Each temporary session downloads the release manifests and native executables
from GitHub, requiring Internet access. The spawner verifies checksums before
launching; a failed download or launch prevents startup. Downloads and child
working directories stay within the temporary session and are removed after
child shutdown.

Repeat `--spawn-agents` to replace the default package list with explicit public
GitHub releases, for example:

```sh
./commander --all-in-one --spawn-agents github.com/defos-ai/commander-web-agent@v0.2.0
```

Use `--spawn-agents=` to start without package downloads. The embedded builder
and spawner remain available; website missions require the corresponding agents.
The flag is all-in-one only. For Docker AIO, place it after the image name.
Use packages from authors you trust; native agents run as the current OS user.

### Credentials

For native sessions, use `/credentials` inside Playground to see generated
credentials. These shell commands read only credentials configured in that shell:

```sh
./commander credentials api
./commander credentials mcp
```

On Windows, use `.\commander.exe`. Each command prints the selected credentials
from `API_USERNAME` / `API_PASSWORD` or `MCP_TOKEN` and exits. In Docker, use the
`docker exec` commands above to retrieve the running container's credentials.
Add `--raw` to print only the API password or MCP token.

### Model setup

Choose a provider and model with **↑/↓** and **Enter**; mouse wheel, trackpad or
**PgUp/PgDown** scroll long lists. Select a model supporting tool calls, using
**Enter model name…** if needed.
Missing keys are entered privately. **Other** accepts an OpenAI-compatible base
URL, model name and optional key. Key prompts link to the provider's API key page.
Rejected keys can be corrected in place; other discovery failures offer manual setup.

To connect to an existing MCP server, paste its token when prompted or set
`COMMANDER_MCP_TOKEN` beforehand:

```sh
./commander --provider openai --model YOUR_MODEL --mcp-url http://localhost:4273/mcp
```

| Setting | Environment | Flag |
| --- | --- | --- |
| MCP endpoint | `COMMANDER_MCP_URL` | `--mcp-url` |
| MCP bearer token | `COMMANDER_MCP_TOKEN` | Hidden setup prompt |
| Admin browser address | `COMMANDER_ADMIN_URL` | `--admin-url` |
| Provider | `COMMANDER_LLM_PROVIDER` | `--provider` |
| Model | `COMMANDER_LLM_MODEL` | `--model` |
| Model API base URL | `COMMANDER_LLM_BASE_URL` | `--base-url` |
| Error details | `COMMANDER_DEBUG=true` | `--debug` / `--debug=false` |
| Disable colors | `NO_COLOR` | `--no-color` |

Flags override environment values. MCP defaults to `http://localhost:4273/mcp`.
For existing hosts, add `--admin-url https://YOUR_ADMIN_HOST` to enable `/admin`;
Playground does not infer Admin's address or API connection from MCP.
MCP credentials are sent only to the configured endpoint; redirects are not followed.
An HTTP `401` stops requests until you reconnect with the correct token. During
setup, paste the token into the hidden input and press **Enter** to reconnect.
**Need a token?** shows a lookup command when deployment metadata is available.
Tokens entered here stay in memory for this session.
Provider keys are `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY` (or
`GOOGLE_API_KEY`), `XAI_API_KEY`, `DEEPSEEK_API_KEY` and `COMMANDER_LLM_API_KEY`
for **Other**. Keys and chat history stay in memory; hosts save operation state.
Messages and requested tool results go to the selected model provider, which bills
API usage.

For model failures, rerun with `--debug` to see redacted provider errors. Requests
are not retried automatically; saved work and queued messages remain. Send
guidance or `/watch <operation-id>` to resume review.

### Chat

Try “List missions and available agents,” then:

> Run review-website-no-browser for https://defos.ai with max_pages 3. Review the findings
> and produce a report. Before completing, ask whether I want a brief or detailed report.

With the Docker image or a configured browser, use `review-website-with-browser`
to include rendered accessibility and screenshots.

| Command | Effect |
| --- | --- |
| `/help`, `/status`, `/operations` | Show help, connection/queue state or saved operations. |
| `/credentials` | Show the all-in-one API login and MCP token. Also available during model setup. |
| `/admin` | Show the Admin link. Also available during model setup. |
| `/watch <operation-id>` | Follow saved work and resume model decisions. |
| `/run <operation-id> <action-name> <json-input>` | Queue a selected action through MCP; input must be a JSON object. Pending questions and action readiness still apply. |
| `/cancel <operation-id>` | Cancel the operation. Dispatched tasks may finish. |
| `/stop` or **Ctrl+C** | Stop this reply and clear queued messages; server-side work continues. |
| `/clear` | Clear conversation after stopping the reply. |
| `/exit` or **Ctrl+D** | Exit chat; all-in-one hosts also stop. |

Messages queue during replies; slash commands and operation updates remain
responsive. Scroll chat with **PgUp/PgDown**. Wheel and trackpad scrolling are
unavailable in terminals that translate those gestures into arrow keys.
Drag to select output, then use your terminal's usual copy command:
**Ctrl+Shift+C** in GNOME Terminal,
**Cmd+C** in macOS Terminal, or **Copy** from the terminal menu.
Selection stays in the terminal; **Ctrl+C** still stops the reply.

Completed operation rows include **Review on Admin UI** links to that operation.
Local all-in-one links prefill the connection or reuse a saved one; sign in if
needed. Remote sessions need a configured Admin address, then ask you to choose
the API connection. Passwords and tokens never appear in links.

Questions appear in a selector: use **↑/↓** and **Enter**, type a custom answer,
or press **Tab** to return to chat. The panel shows the latest question, deadline
and count of others waiting. Only operator input records an answer; expiry returns
the decision to model review without approving work. With separate hosts, exiting
leaves operations running, but model decisions wait for a session to follow them.

See [website scenarios](../agents/web/README.md#try-these-in-playground)
and the [MCP workflow](../adapters/README.md#operation-workflow).

## Development and testing

[Contributor guidance](AGENTS.md) · [Test setup](../../framework/tests/README.md)
