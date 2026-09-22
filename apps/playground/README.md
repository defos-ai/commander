# Playground

## Overview

A terminal chat for trying Commander with one model per session. Choose a provider,
talk to your model, and follow missions while continuing the conversation. The
chat communicates through MCP. Playground has its own Go module and can also
launch the bundled local hosts for a self-contained demo.

## Quick start

### Docker (recommended)

No Go installation is needed. From the repository root:

```sh
docker compose up --build -d && docker compose run --build --rm playground
```

Playground runs interactively in its own container and connects to the Compose
MCP service. There is no host executable to copy or cross-compile.
Export your provider key before running, or enter it in
setup. `/exit` closes the chat; Compose hosts and saved work remain available.
Use `docker compose down` to stop them. See [local setup](../../docs/deployment.md).

### Go: one executable

With Go 1.26.7+, build and run from the repository root:

```sh
go build -o commander ./apps/playground/cmd/commander && ./commander --all-in-one
```

Or run directly: `go run ./apps/playground/cmd/commander --all-in-one`.

This starts **one MCP host, one controller, and one web-quality agent**.
The mission is included in the executable; no checkout
or Go installation is needed after building. Local services use available
loopback ports and isolated temporary state. **Exiting stops them and deletes
that session's operations, reports, and artifacts.** Use Compose to retain work.

The [agent guide](../agents/web-quality/README.md#native-agent) covers action
availability and environment configuration.

### Model setup

Select a provider and model with **↑/↓**, then press **Enter**. Use **PgUp/PgDown**
for long model lists, or select **Enter model name…** for an unlisted model.
Detected API keys are highlighted; missing keys can
be entered privately. **Other** accepts an OpenAI-compatible API base URL, model
name, and optional key. Model discovery failure offers manual setup. Choose a
chat model that supports tool calling; availability depends on your account.

Without `--all-in-one`, MCP defaults to `http://localhost:4273/mcp`. For an
existing server and model configuration:

```sh
./commander --provider openai --model YOUR_MODEL --mcp-url http://localhost:4273/mcp
```

| Setting | Environment | Optional flag |
| --- | --- | --- |
| MCP endpoint | `COMMANDER_MCP_URL` | `--mcp-url` |
| Provider | `COMMANDER_LLM_PROVIDER` | `--provider` |
| Model | `COMMANDER_LLM_MODEL` | `--model` |
| Model API base URL | `COMMANDER_LLM_BASE_URL` | `--base-url` |
| Error details | `COMMANDER_DEBUG=true` | `--debug` / `--debug=false` |
| Disable colors | `NO_COLOR` | `--no-color` |

Flags override environment values. Provider keys are `OPENAI_API_KEY`,
`ANTHROPIC_API_KEY`, `GEMINI_API_KEY` (or `GOOGLE_API_KEY`), `XAI_API_KEY`,
`DEEPSEEK_API_KEY`, and `COMMANDER_LLM_API_KEY` for **Other**. Keys and chat history
stay in memory; the connected Commander hosts hold operation state. Model API usage is billed by the
selected provider, and messages and requested tool results are sent to it.

`--all-in-one` selects its own MCP endpoint and local storage, ignoring
`COMMANDER_MCP_URL`, `BUS_URL`, `STORAGE_URL`, and `DATABASE_URL`. It cannot be
combined with `--mcp-url`. With `--debug`, chat also shows the temporary host log
location; those logs are deleted on exit.

For model API failures, rerun with `./commander --debug` (plus your usual
flags). Debug mode shows bounded provider error responses with the configured
API key and credential fields redacted. It also covers model discovery and
streaming errors. Normal mode shows a short error and a `--debug` hint.

A failed model request ends that turn and displays an error. The playground
keeps saved work and queued messages; it does not automatically retry the failed
request or create an operator question. Send another message or use
`/watch <operation-id>` to review pending work again.

### Chat

Try **“List missions and available agents.”**, then:

> Run review-website for https://defos.ai. Review the pages and summarize the
> release-readiness issues with evidence.

To exercise operator questions, add: **“Before completing the report, ask me whether to
prioritize accessibility or broken links.”**

- `/help`, `/status`, `/operations`: help and current state.
- `/watch <operation-id>`: follow saved work and resume model decisions.
- `/run <operation-id> <action-name> <json-input>`: queue an operator-selected action directly through MCP. Input must be a JSON object; pending questions and permission checks still apply.
- `/cancel <operation-id>`: cancel the operation through Commander.
- `/stop` or **Ctrl+C**: stop the current reply and clear queued messages.
- `/clear`: clear model conversation after stopping a reply.
- `/exit` or **Ctrl+D**: exit the playground.

Assistant replies render Markdown, including lists, links, tables, and code.
Text streams as it arrives. Submitted messages queue during an active model/tool
turn, while operation updates and slash commands remain responsive. Use
**PgUp/PgDown** to scroll. Star loaders use the DEFOS palette; progress shows real
task counts rather than an estimated percentage.

Operator questions appear in a selector: **↑/↓** chooses an option and **Enter**
submits it. Type a custom answer or use **Tab** to switch back to chat without
losing your draft. The panel shows the latest question, a countdown, and how many
others are waiting. Up to four options are offered. Questions expire after two
minutes by default, then the model reviews how to continue.

The watcher brings pending model continuations and expired questions back to the
conversation. Only operator input submits an answer; expiry does not invent one.
With a separate MCP server, exiting
leaves server-side work running; further model decisions wait until a session
follows the operation. All-in-one hosts stop with Playground.
After a failed model turn, send guidance or use `/watch <id>` to retry review.

## Development and testing

`cli/` owns startup; `internal/local/` manages the optional local hosts;
`internal/ui/` renders setup and chat; `internal/chat/` runs model/tool turns,
operator commands, and watchers. `internal/mcp/` uses the MCP Go SDK. Model providers
live in `internal/llm/`: OpenAI uses the Responses API; Gemini, xAI, DeepSeek and
Other use Chat Completions-compatible APIs; Anthropic uses the Messages API.
OpenAI keeps the model's default reasoning setting and replays reasoning context
between tool calls with `store: false`. The local launcher reuses the existing
host applications; chat and UI continue to access Commander through MCP.

```sh
go test -race -count=1 ./apps/playground/...
go vet ./apps/playground/...
```

Tests live under `tests/` and use local model fixtures: no real API keys or
paid model calls. The website acceptance test runs real MCP, controller, agent,
and browser processes through Playground, including an operator answer and a
saved report. Browser test setup is documented in the agent guide.
See the [website scenarios](../agents/web-quality/README.md#try-these-in-playground),
[testing guide](../../framework/tests/README.md) and
[local Commander setup](../../docs/deployment.md).
