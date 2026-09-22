# Web Quality Agent

## Overview

Review a website before release: find broken links, flag basic accessibility
and SEO issues, capture desktop/mobile screenshots, and identify visible
technology indicators. The agent runs checks; the model reviews evidence and
asks for operator guidance when needed.

| Action | Result |
| --- | --- |
| `discover-pages` | A bounded set of pages on the same hostname and proposed checks. |
| `check-links` | HTTP link status, broken links, and skipped/limited coverage. |
| `check-accessibility` | Basic rendered-page checks for titles, language, image descriptions, and control names; JavaScript errors. |
| `capture-page` | Desktop/mobile PNG artifacts and horizontal overflow detection. |
| `check-seo` | Static HTML checks for page titles, descriptions, canonicals, headings, indexing directives, viewport and social metadata. |
| `detect-technologies` | Passive technology indicators from response headers, generator metadata and script URLs, with versions only when explicitly exposed. |

### Workflow

```text
discover-pages
  ├─ page 1: check-links + check-accessibility + capture-page + check-seo + detect-technologies
  ├─ page 2: check-links + check-accessibility + capture-page + check-seo + detect-technologies
  └─ ...
       → wait for all results → model review → optional operator question → report
```

The [`review-website` mission](../../controllers/default/data/default-missions.json)
authorizes the checks and groups their results. The model assigns discovery once;
the controller schedules checks across available agents. Completed checks never
require separate model approval. Operation completion remains explicit.

## Quick start

From the repository root:

```sh
docker compose up --build -d && docker compose run --build --rm playground
```

Choose your model in Playground, then ask:

> Run review-website for https://defos.ai with max_pages 3.
> Review the results and produce a concise report of what needs fixing.

For another website, supply a URL reachable from the agents.
Agent registration can take one heartbeat interval (30 seconds) on first startup.

For each discovered page, the controller schedules five checks, including two
screenshots. Results depend on the website's content. Inspect them through
`get_operation` and the final report through `get_report`; image artifacts are
available through MCP resources.
Playground displays text and artifact references, not inline screenshots.

When updating an existing installation, restart the controller to load the
bundled mission changes for new operations. Existing operations keep their saved
mission rules; see [deployment and state](../../../docs/deployment.md#state).

### Native agent

Run the standalone agent:

```sh
go run ./apps/agents/web-quality/cmd
```

Only screenshot and rendered-accessibility actions use Chromium or Google Chrome.
They find it on `PATH`; `CHROMIUM_PATH` can specify an absolute executable path.
These actions report unavailable if it is missing; discovery,
link checks, SEO checks and technology detection remain usable with only
`network-access` permission. No third-party API key is needed by this agent.

Compose supplies `CHROMIUM_NO_SANDBOX=true` for its non-root browser containers.
Native runs use Chromium's sandbox by default. Connection settings and generic
host flags follow the [local setup guide](../../../docs/deployment.md).

## Development and testing

```sh
go test -race -count=1 ./apps/agents/web-quality/...
go test -race -count=1 ./apps/adapters/mcp/...
go test -race -count=1 ./apps/playground/...
```

Browser tests use local fixtures and skip when no browser is installed. Set
`CHROMIUM_PATH` to exercise them explicitly; CI installs/locates Chrome so they
run there. Test model replies are fixtures, so tests need no paid model access.
Actions and host tests cover validation, discovery, permissions, bounded work,
screenshots, cancellation, result replay and shutdown. The MCP workflow covers
two agents, controller recovery, grouped review, operator answers and reports.

### Try these in Playground

| Scenario | How to try it | What to expect |
| --- | --- | --- |
| Automatic review | Use the prompt above. | Checks run in the background; one review follows all results. |
| Operator input | Add “Before completing, ask whether I want a brief or detailed report.” | Select an answer with ↑/↓ and Enter in Playground to resume review. |
| Concurrent work | Start another review while one is running. | `/operations` shows both; you can keep chatting. |
| Restart | Restart the controller while work is running or a question is pending. | Saved tasks/questions remain available. `/watch <operation-id>` reconnects a new Playground session. |
| Cancellation | `/cancel <operation-id>` while checks run. | No new dispatch; already dispatched checks may finish. |
| Ordinary failure | Review a URL you know returns an HTTP error. | A failed discovery task, visible error, and a model decision about what to do next. |

### Scope

Discovery follows HTML links on the exact same hostname, compared without case,
defaults to three pages, and allows at most ten. HTTP/HTTPS and port changes are
allowed; subdomains, including a `www` variant, are different hostnames and are
excluded. Discovery does not discover JavaScript-only routes or use a sitemap.
Link checks inspect at most 40 links and report excluded external destinations.
Browser checks render JavaScript, use fresh profiles, and permit GET/HEAD
requests. Document navigation uses the same hostname rule; navigation to other
hostnames and form submissions are blocked. External fonts, images and styles
may load.

SEO checks inspect the fetched HTML without executing JavaScript. Technology
detection observes the page and response headers without probing additional
endpoints or executing scripts. Exposed versions are evidence from the page,
not verified installations, and detection makes no vulnerability claims. These
are basic quality checks, not a complete accessibility certification or a
substitute for manual review.

Add actions using the [agent contribution guide](../README.md), and update the
mission when a new automatic transition is needed.
