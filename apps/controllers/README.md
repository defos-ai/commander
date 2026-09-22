# Controllers

## Overview

Controllers coordinate agents and track mission operations, tasks, and results.
The bundled [default controller](default/) receives adapter commands through
the message bus and restores saved operations after restart. It opens no HTTP port.

## Quick start

With Go 1.26.7+, run from the repository root:

```sh
go run ./apps/controllers/default/cmd
```

No missions are loaded by default. To upsert definitions from a JSON file:

```sh
go run ./apps/controllers/default/cmd --with-missions ./apps/controllers/default/data/default-missions.json
```

The file is read on each startup; matching missions are updated and other missions
are preserved. Compose mounts the [example definitions](default/data/default-missions.json)
and supplies this flag.

See [local setup](../../docs/deployment.md) for shared connection settings and
[controller recovery](../../docs/overview.md#state-and-restart-recovery) for restart
behavior and current scaling limits.

## Development and testing

- Configure the default host in [`default/cli/`](default/cli/).
- Add another controller under `<controller-type>/` with its own module, CLI,
  entrypoint, and tests. Inject providers into the framework runtime.
- Shared coordination and recovery behavior belongs in the
  [framework](../../framework/README.md); test host behavior in `default/tests/`.

Automation belongs to each mission definition, not the controller host. See
[mission automation](../../docs/abstractions.md#mission-automation) for rules and examples.

### Run tests

From the repository root:

```sh
go test -race -count=1 ./apps/controllers/default/...
```

Follow the shared [testing conventions](../../framework/tests/README.md#writing-tests).
