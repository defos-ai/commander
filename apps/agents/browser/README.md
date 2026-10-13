# Browser Agent

The `browser-agent` renders pages and collects browser evidence. The
[web agent](../web/README.md) handles crawling, links, SEO and HTTP technology checks.

| Action | Result |
| --- | --- |
| `check-accessibility` | Rendered title, language, image/control-name checks and JavaScript errors. |
| `capture-page` | Desktop/mobile PNGs in one artifact; horizontal overflow detection. |

## Run

Compose includes one browser agent and two web agents:

```sh
docker compose up --build --wait && docker compose run --build --rm --no-deps playground
```

Ask Playground to run `review-website-with-browser` for `https://defos.ai`.
The web agent discovers pages; the controller sends browser checks here and HTTP
checks to web agents. Commander reviews the results and completes the report.

### Native agent

From the repository root, with Go 1.26.7+:

```sh
go run ./apps/agents/browser/cmd
```

Connect the controller to the same `BUS_URL` and `STORAGE_URL`. Chromium or Google
Chrome must be on `PATH`, or set an absolute executable path in `CHROMIUM_PATH`.
Missing browser configuration leaves these actions unavailable; the web agent
can still run `review-website-no-browser` independently.

Compose supplies Chromium and sets `CHROMIUM_NO_SANDBOX=true` in its non-root
container. Native runs keep sandboxing enabled by default. See
[deployment](../../../docs/deployment.md) for connection settings.

## Scope

Each check uses a fresh browser profile. Requests use GET/HEAD; documents stay on
the requested hostname, while external resources needed to render pages may load.
These bounded checks do not constitute a complete accessibility audit.

Screenshots are grouped artifact files. Use the API or MCP artifact resources to
retrieve them; Playground displays text and artifact references.

## Development

Use the [agent guide](../README.md) to add actions and [AGENTS.md](AGENTS.md) for
browser implementation guidance.

```sh
go test -race -count=1 ./apps/agents/browser/...
```

The [MCP/API suites](../../../framework/tests/README.md#mcp-api-tests) exercise
browser execution, artifacts and the full mission with local fixtures. No model
key or host browser is needed.
