# Web Quality Agent

## Overview

The web quality agent gathers website evidence. Commander reviews findings and
handles open decisions; the controller runs mission automation and schedules checks.

| Action | Result |
| --- | --- |
| `discover-pages` | Pages on the same hostname and proposed checks. |
| `check-links` | Broken links, HTTP statuses and skipped coverage. |
| `check-accessibility` | Rendered title, language, image/control-name checks and JavaScript errors. |
| `capture-page` | Desktop/mobile PNGs in one artifact; horizontal overflow detection. |
| `check-seo` | Static HTML metadata, headings, canonicals and indexing checks. |
| `detect-technologies` | Passive indicators from headers, metadata and script URLs. |

### Workflow

The [review-website mission](../../controllers/default/data/default-missions.json)
authorizes this flow:

```text
discover-pages → five checks per page → grouped results → Commander review → report
```

Commander assigns discovery once. The controller schedules proposed checks across
ready agents and groups their results for review. Commander resolves the pending
continuation, asks the operator when needed, and explicitly completes the operation.

## Quick start

From the repository root:

```sh
docker compose up --build -d
docker compose run --build --rm playground
```

Choose your model, then ask:

> Run review-website for https://defos.ai with max_pages 3.
> Review the results and report what needs fixing.

Supply a URL reachable from the agents. Registration can take one heartbeat
interval (30 seconds) after startup. Use `get_operation` for results and
`get_report` for the completed report. Images are MCP artifact resources;
Playground shows text and artifact references.

Restart the controller after changing its mission file. New operations use the
updated definition; existing operations keep their saved rules. See
[deployment and state](../../../docs/deployment.md#state).

### Native agent

With Go 1.26.7+, run from the repository root:

```sh
go run ./apps/agents/web-quality/cmd
```

Screenshot and rendered-accessibility actions require Chromium or Google Chrome
on `PATH`, or an absolute executable path in `CHROMIUM_PATH`. They also require
`network-access` and `execute-command` grants. The host grants both by default.
Other actions need only `network-access`; no third-party API key is required.
Without a browser, the two browser actions report unavailable and other actions
remain usable; the full mission still waits for its required-action coverage.

Compose supplies Chromium and sets `CHROMIUM_NO_SANDBOX=true` in its non-root
containers. Native runs use the browser sandbox by default. See
[deployment](../../../docs/deployment.md) for connections and host flags.

## Development and testing

```sh
go test -race -count=1 ./apps/agents/web-quality/...
```

Agent tests cover action behavior and dependency discovery. The
[MCP suite](../../../framework/tests/README.md#mcp-api-tests) runs the full review
with Compose, including real browsers, screenshots, questions and reports; it
needs no model key or host browser.

### Try these in Playground

| Scenario | Action | Expected behavior |
| --- | --- | --- |
| Automatic review | Use the prompt above. | Checks run before grouped model review. |
| Operator input | Add “Ask whether I want a brief or detailed report.” | Answer with ↑/↓ and Enter. |
| Concurrent work | Start another review. | `/operations` lists both. |
| Restart | Restart the controller; use `/watch <operation-id>` in a new chat. | Saved tasks and questions remain available. |
| Cancellation | `/cancel <operation-id>` | New dispatch stops; dispatched checks may finish. |
| Failure | Review a URL returning an HTTP error. | Failed discovery and a pending model decision. |

### Scope

- Discovery follows HTML links on the exact hostname, ignoring case; HTTP/HTTPS
  and port changes are allowed. Subdomains and `www` variants are excluded. It
  defaults to three pages, allows ten, and skips sitemaps and JavaScript-only routes.
- Link checks inspect at most 40 links and report excluded external destinations.
- Browser checks render JavaScript in fresh profiles. Requests use GET/HEAD;
  documents stay on the requested hostname, while external resources may load.
- SEO uses fetched HTML without JavaScript. Technology detection observes exposed
  indicators without probing endpoints; reported versions are unverified hints.

These are bounded quality checks. They do not provide a complete accessibility
audit or prove vulnerabilities. A redirect to another hostname requires an
operator scope decision under the bundled mission.

Use the [agent contribution guide](../README.md) to add actions, and update the
mission when another automatic transition is needed.
