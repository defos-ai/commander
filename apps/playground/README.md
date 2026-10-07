# Playground

Playground is a terminal chat with one model per session. The model acts as
Commander through MCP: choose missions, assign work and review evidence while
the controller runs automation and agents execute actions. Connect to running
hosts or launch a temporary local demo.

## Quick start

### Docker (recommended)

From the repository root:

```sh
docker compose up --build -d
docker compose run --build --rm playground
```

Export your model provider's key first, or enter it during setup. `/exit` closes
chat; hosts and saved work remain. `docker compose down` stops the hosts. See
[deployment](../../docs/deployment.md) for connections and state.

### Native: one executable

The first release is not published yet. [Native release archives](../../docs/releases.md#native-binaries)
will provide `commander` for Linux, macOS and Windows (amd64/arm64), without Go.
Extract your archive and run `./commander --all-in-one`, or
`.\commander.exe --all-in-one` on Windows.

To build from source with Go 1.26.7+, run from the repository root:

```sh
go build -o commander ./apps/playground/cmd/commander
./commander --all-in-one
```

This starts one MCP host, one controller and one web quality agent using temporary
state and an available loopback port. The executable embeds the mission and needs
no checkout or Go installation. Browser actions still need
[Chromium](../agents/web-quality/README.md#native-agent).
**Exiting stops these hosts and deletes the session's operations, reports and artifacts.**
Use Compose to retain work.

`--all-in-one` chooses its own connections, ignoring `COMMANDER_MCP_URL`, `BUS_URL`,
`STORAGE_URL` and `DATABASE_URL`; it cannot be combined with `--mcp-url`. `--debug`
shows the temporary host log location, which is also deleted on exit.

### Model setup

Choose a provider and model with **↑/↓** and **Enter**; **PgUp/PgDown** scroll long
lists. Select a model supporting tool calls, using **Enter model name…** if needed.
Missing keys are entered privately. **Other** accepts an OpenAI-compatible base
URL, model name and optional key. Discovery failures offer manual setup.

To connect to an existing MCP server:

```sh
./commander --provider openai --model YOUR_MODEL --mcp-url http://localhost:4273/mcp
```

| Setting | Environment | Flag |
| --- | --- | --- |
| MCP endpoint | `COMMANDER_MCP_URL` | `--mcp-url` |
| Provider | `COMMANDER_LLM_PROVIDER` | `--provider` |
| Model | `COMMANDER_LLM_MODEL` | `--model` |
| Model API base URL | `COMMANDER_LLM_BASE_URL` | `--base-url` |
| Error details | `COMMANDER_DEBUG=true` | `--debug` / `--debug=false` |
| Disable colors | `NO_COLOR` | `--no-color` |

Flags override environment values. MCP defaults to `http://localhost:4273/mcp`.
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

> Run review-website for https://defos.ai with max_pages 3. Review the findings
> and produce a report. Before completing, ask whether I want a brief or detailed report.

| Command | Effect |
| --- | --- |
| `/help`, `/status`, `/operations` | Show help, connection/queue state or saved operations. |
| `/watch <operation-id>` | Follow saved work and resume model decisions. |
| `/run <operation-id> <action-name> <json-input>` | Queue a selected action through MCP; input must be a JSON object. Questions and permission checks still apply. |
| `/cancel <operation-id>` | Cancel the operation. Dispatched tasks may finish. |
| `/stop` or **Ctrl+C** | Stop this reply and clear queued messages; server-side work continues. |
| `/clear` | Clear conversation after stopping the reply. |
| `/exit` or **Ctrl+D** | Exit chat; all-in-one hosts also stop. |

Messages queue during replies; slash commands and operation updates remain
responsive. **PgUp/PgDown** scrolls chat.

Questions appear in a selector: use **↑/↓** and **Enter**, type a custom answer,
or press **Tab** to return to chat. The panel shows the latest question, deadline
and count of others waiting. Only operator input records an answer; expiry returns
the decision to model review without approving work. With separate hosts, exiting
leaves operations running, but model decisions wait for a session to follow them.

See [website scenarios](../agents/web-quality/README.md#try-these-in-playground)
and the [MCP workflow](../adapters/README.md#operation-workflow).

## Development and testing

[Contributor guidance](AGENTS.md) · [Test setup](../../framework/tests/README.md)
