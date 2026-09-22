# Playground

## Overview

A terminal chat for trying Commander with one model per session. Choose a provider,
talk to your model, and follow missions while continuing the conversation. The
playground communicates only through MCP and has its own Go module.

## Quick start

Start Commander with `docker compose up --build -d`, then, from the repository root:

```sh
go build -o bin/commander ./apps/playground/cmd/commander
./bin/commander
```

Add `bin/` to your `PATH` to invoke it as `commander`.

Select a provider and model with **↑/↓**, then press **Enter**. Use **PgUp/PgDown**
for long model lists, or select **Enter model name…** for an unlisted model.
Detected API keys are highlighted; missing keys can
be entered privately. **Other** accepts an OpenAI-compatible API base URL, model
name, and optional key. Model discovery failure offers manual setup. Choose a
chat model that supports tool calling; availability depends on your account.

MCP defaults to `http://localhost:4273/mcp`. For an existing configuration:

```sh
./bin/commander --provider openai --model YOUR_MODEL --mcp-url http://localhost:4273/mcp
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
stay in memory; Commander persists operation state. Model API usage is billed by the
selected provider, and messages and requested tool results are sent to it.

For model API failures, rerun with `./bin/commander --debug` (plus your usual
flags). Debug mode shows bounded provider error responses with the configured
API key and credential fields redacted. It also covers model discovery and
streaming errors. Normal mode shows a short error and a `--debug` hint.

A failed model request ends that turn and displays an error. The playground
keeps saved work and queued messages; it does not automatically retry the failed
request or create an operator question. Send another message or use
`/watch <operation-id>` to review pending work again.

### Chat

Try **“List missions and available agents.”**, then:

> Run review-website for http://sample-website:8080/. Review the pages and summarize the
> release-readiness issues with evidence.

The bundled local website is reachable from the agents in Compose. To exercise
operator questions, add: **“Before completing the report, ask me whether to
prioritize accessibility or broken links.”**

- `/help`, `/status`, `/operations`: help and current state.
- `/watch <operation-id>`: follow saved work and resume model decisions.
- `/run <operation-id> <action-name> <json-input>`: queue an operator-selected action directly through MCP. Input must be a JSON object; pending questions and permission checks still apply.
- `/answer <question-id> <text>`: answer a question, or enter its option number.
- `/cancel <operation-id>`: cancel the operation through Commander.
- `/stop` or **Ctrl+C**: stop the current reply and clear queued messages.
- `/clear`: clear model conversation after stopping a reply.
- `/exit` or **Ctrl+D**: exit the playground.

Text streams as it arrives. Submitted messages queue during an active model/tool
turn, while operation updates and slash commands remain responsive. Use
**PgUp/PgDown** to scroll. Star loaders use the DEFOS palette; progress shows real
task counts rather than an estimated percentage.

The watcher brings pending model continuations back to the conversation. Only an
operator can submit an answer with `/answer`. Exiting leaves server-side work
running; further model decisions wait until a session follows the operation.
After a failed model turn, send guidance or use `/watch <id>` to retry review.

## Development and testing

`cli/` owns startup; `internal/ui/` renders setup and chat; `internal/chat/` runs
model/tool turns and watchers. `internal/mcp/` uses the MCP Go SDK. Model providers
live in `internal/llm/`: OpenAI uses the Responses API; Gemini, xAI, DeepSeek and
Other use Chat Completions-compatible APIs; Anthropic uses the Messages API.
OpenAI keeps the model's default reasoning setting and replays reasoning context
between tool calls with `store: false`. No agent framework or persistence provider
dependencies are required.

```sh
go test -race -count=1 ./apps/playground/...
go vet ./apps/playground/...
```

Tests live under `tests/` and use local model fixtures: no real API keys or
paid model calls. The website acceptance test runs real MCP, controller, agent,
and browser processes through Playground, including an operator answer and a
saved report. It requires Chromium/Chrome on `PATH` or at `CHROMIUM_PATH`.
See the [website scenarios](../agents/web-quality/README.md#try-these-in-playground),
[testing guide](../../framework/tests/README.md) and
[local Commander setup](../../docs/deployment.md).
