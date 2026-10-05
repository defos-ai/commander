# HTTP API adapter

## Overview

The API adapter will expose Commander through HTTP, sending commands to the
controller over the bus and querying domain stores. This first increment adds
the host and provider wiring. It serves only public `GET /healthz` (liveness)
and `GET /readyz` (metadata database readability). Readiness does not yet check
ongoing broker connectivity, file storage, or controller availability.

Business endpoints, Basic authentication enforcement, host/proxy policies and
Swagger are subsequent increments. Credentials are already required at startup;
this increment exposes no operation or artifact data.

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

## Development and testing

```sh
go test -race -count=1 ./apps/adapters/api/...
go vet ./apps/adapters/api/...
```

Keep concrete providers in `cli/factory.go` and inject their interfaces into
the adapter. Boundary tests live in `tests/` and use temporary filesystem state.
