# HTTP API adapter

## Overview

The API adapter connects HTTP clients to Commander: commands go to the
controller over the bus, and queries read domain stores. The host and HTTP
security boundary are implemented; business endpoints and Swagger come next.

`GET`/`HEAD /healthz` checks liveness; `/readyz` checks metadata database
readability. These probes are public on every host for load balancers. Readiness
does not check ongoing broker connectivity, file storage or controller availability.

## Quick start

From the repository root, with `API_USERNAME` and `API_PASSWORD` set:

```sh
go run ./apps/adapters/api/cmd/commander-api
curl http://127.0.0.1:4274/healthz
```

Listener precedence: `--listen-address`, then `API_LISTEN_ADDRESS`, then
`0.0.0.0:$PORT`, then `127.0.0.1:4274`. Heroku supplies `PORT`; containers can
set `PORT=4274`. The same Dockerfile serves local and hosted deployments.
Container deployments must supply provider URLs and, when using filesystem
providers, writable mounts; the image does not prepare state directories.

Use `BUS_URL`, `DATABASE_URL`, and `STORAGE_URL` for providers; `LOG_LEVEL` and
`LOG_FORMAT` for logging. Their corresponding CLI flags override environment
values. Defaults use local `./state/` directories. Credentials are environment
only and must both be nonblank. Run with `--help` for options.

All other routes require HTTP Basic authentication, including unknown routes.
Use HTTPS at the reverse proxy for hosted deployments. Responses carry
no-cache, content-type, framing and referrer protections; HTTPS responses also
set HSTS. Browser requests must come from the same origin. Native clients may
omit `Origin`.

| Environment variable | CLI flag | Default |
| --- | --- | --- |
| `API_ALLOWED_HOSTS` | `--allowed-hosts` | `localhost,127.0.0.1,::1` |
| `API_TRUSTED_PROXIES` | `--trusted-proxies` | None |
| `API_PROXY_HEADERS` | `--proxy-headers` | `none` |

Allowed hosts are exact hostnames/IPs without ports or wildcards. Add your public
hostname when deploying. Trusted proxies are comma-separated IPs or CIDRs,
matched against the directly connected peer.

Select `forwarded` for `Forwarded`, `x-forwarded` for `X-Forwarded-Host` and
`X-Forwarded-Proto`, or `x-forwarded-proto` to preserve the original `Host`.
Forwarding requires a trusted proxy; configure it to overwrite the selected
headers. Each header must contain a single value; chains are rejected. Missing
headers use the direct request's host and scheme. Other forwarding headers are
ignored. The Dockerfile stays the same for each deployment.

## Development and testing

```sh
go test -race -count=1 ./apps/adapters/api/...
go vet ./apps/adapters/api/...
```

Keep concrete providers in `cli/factory.go` and inject their interfaces into
the adapter. Boundary tests live in `tests/` and use temporary filesystem state.
