# Web Agent

The `web-agent` discovers pages and checks HTTP responses and static HTML. It needs
no browser or third-party API key.

| Action | Result |
| --- | --- |
| `discover-pages` | Same-hostname pages and proposals for HTTP and browser checks. |
| `discover-pages-no-browser` | The same crawl, proposing HTTP checks only. |
| `check-links` | Broken links, HTTP statuses and skipped coverage. |
| `check-seo` | Metadata, headings, canonicals and indexing directives. |
| `detect-technologies` | Passive indicators from headers, metadata and script URLs. |

## Run

From the repository root:

```sh
go run ./apps/agents/web/cmd
```

Connect the controller to the same `BUS_URL` and `STORAGE_URL`.
See [deployment](../../../docs/deployment.md).

Compose starts two web agents and one [browser agent](../browser/README.md):

```sh
docker compose up --build --wait && docker compose run --build --rm --no-deps playground
```

Ask Playground to run `review-website-no-browser` for `https://defos.ai`, or use
`review-website-with-browser` to include rendered accessibility and screenshots.

## Workflow

The web agent proposes checks for discovered pages. The controller schedules
HTTP checks on web agents and rendering on browser agents, following the
[mission rules](../../controllers/default/data/default-missions.json). Commander
reviews the grouped results and completes the report. The no-browser mission
clearly reports the limits of its static checks.

### Try these in Playground

Add “Ask whether I want a brief or detailed report” to try operator questions.
Use `/operations` to list reviews, `/watch <operation-id>` to resume watching, and
`/cancel <operation-id>` to stop new dispatches. Already dispatched checks may
finish. See the [Playground guide](../../playground/README.md).

## Scope

Crawling stays on the exact hostname, allowing HTTP/HTTPS and port changes. It
defaults to three pages and allows ten. Subdomains, `www` variants, sitemaps and
JavaScript-only routes are excluded. Redirects to another hostname require an
operator scope decision. Link checks inspect at most 40 links.

SEO checks use static HTML. Technology indicators are declarations, not verified
runtime use or vulnerability findings; scripts are neither downloaded nor executed.

## Development

Use the [agent guide](../README.md) to add actions and [AGENTS.md](AGENTS.md) for
implementation guidance.

```sh
go test -race -count=1 ./apps/agents/web/...
```

The [MCP/API suites](../../../framework/tests/README.md#mcp-api-tests) cover both
website missions with local fixtures and no model key.
