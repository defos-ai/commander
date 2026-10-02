# Providers

## Overview

Providers connect framework interfaces to messaging, databases, and file storage.
Apps construct them in `cli/factory.go` and inject framework interfaces into
agents, controllers, and adapters.

### Existing providers

| Provider | Available implementations | Connection formats |
| --- | --- | --- |
| [Filesystem](fs/README.md) | Bus, database stores, artifact files, reports | Local paths, `~/path`, `file:///path`, `fs:///path` |
| [MongoDB](mongo/README.md) | Missions, actions, agent inventory, executions, operations, artifact metadata | `mongodb://`, `mongodb+srv://` |
| [S3](s3/README.md) | Artifact files and reports | `s3://bucket/optional-prefix` |
| [RabbitMQ](rabbitmq/README.md) | Message bus | `amqp://`, `amqps://` |

Controller, MCP, and agent factories select RabbitMQ for `amqp://` and `amqps://`
bus URLs. Compose stacks still configure the FS bus.

Each backend is a separate Go module: `commander/providers/fs`,
`commander/providers/mongo`, `commander/providers/s3`, or
`commander/providers/rabbitmq`. Import only the modules your host needs; there is
no shared `commander/providers` module.

## Quick start

Choose backends independently through the host settings:

| Setting | Selects | Available backends |
| --- | --- | --- |
| `BUS_URL` | Messages between apps | FS, RabbitMQ |
| `DATABASE_URL` | Catalogs, inventory, executions, operations, artifact metadata | FS, MongoDB |
| `STORAGE_URL` | Artifact bytes and reports | FS, S3 |

The [local stack](../README.md#quick-start) uses FS throughout.
[`compose.full.yaml`](../compose.full.yaml) uses MongoDB and MinIO with an FS bus.
See [deployment configuration](../docs/deployment.md#configuration) for host
settings, and each provider's guide for constructors and backend setup. The
repository [workspace](../go.work) includes all modules; private projects use
[local module replacements](../apps/agents/README.md#develop-an-agent-in-a-private-repository).

Agents upload bytes through `ArtifactFileStore`; controllers accept metadata
through `ArtifactStore`; MCP uses both to expose accepted artifacts. Uploading
bytes alone does not make an artifact visible through MCP. Failed or interrupted
work can leave unreferenced files; automatic orphan cleanup is not implemented.

### Filesystem behavior

See the FS guide for [delivery and recovery](fs/README.md#delivery-and-recovery)
and [atomic operation records](fs/README.md#atomic-operation-records).

## Development and testing

To add a backend:

1. Create `providers/<backend>/` as its own module. Implement interfaces from
   [`framework/comms`](../framework/comms/README.md) or
   [`framework/domain`](../framework/domain/), keeping driver dependencies and
   storage helpers inside the provider.
2. Export concrete constructors. Add selection to each affected app's
   `cli/factory.go`; unsupported schemes must fail explicitly. Apps share clients
   across stores and close resources after their consumers stop.
3. Preserve acknowledgement, artifact ownership, immutable report and execution
   outcomes, and atomic operation transitions. Apply transitions to the latest
   saved state; keep locks and revisions internal. Follow the
   [`OperationStore` contract](../framework/domain/operations/operation_store.go)
   for retries, conflicts, recovery, and uncertain writes. Reconcile uncertain
   writes before dispatching further work.
4. Test public behavior, concurrent updates, retries, and storage failures in the
   provider module. Follow the [testing guide](../framework/tests/README.md).

Provider-specific checks live in the [FS](fs/README.md#development-and-testing),
[MongoDB](mongo/README.md#development-and-testing),
[S3](s3/README.md#development-and-testing), and
[RabbitMQ](rabbitmq/README.md#development-and-testing) guides. Run affected
framework and app tests when shared behavior changes.
