# Providers

Providers connect framework interfaces to messaging, databases, and file storage.
Hosts choose each backend independently through their settings.

### Existing providers

| Provider | Available implementations | Connection formats |
| --- | --- | --- |
| [Filesystem](fs/README.md) | Bus, database stores, artifact files, reports | Local paths, `~/path`, `file:///path`, `fs:///path` |
| [MongoDB](mongo/README.md) | Database stores | `mongodb://`, `mongodb+srv://` |
| [S3](s3/README.md) | Artifact files and reports | `s3://bucket/optional-prefix` |
| [RabbitMQ](rabbitmq/README.md) | Message bus | `amqp://`, `amqps://` |

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
[`compose.full.yaml`](../compose.full.yaml) uses RabbitMQ, MongoDB, and MinIO.
See [deployment configuration](../docs/deployment.md#configuration) for host
settings, and each provider's guide for constructors and backend setup. The
repository [workspace](../go.work) includes all modules; private projects use
[local module replacements](../apps/agents/README.md#develop-an-agent-in-a-private-repository).

Agents upload bytes through `ArtifactFileStore`; controllers accept metadata
through `ArtifactStore`; MCP uses both to expose accepted artifacts. Uploading
bytes alone does not make an artifact visible through MCP. Failed or interrupted
work can leave unreferenced files; automatic orphan cleanup is not implemented.

## Development and testing

Provider-specific checks live in the [FS](fs/README.md#development-and-testing),
[MongoDB](mongo/README.md#development-and-testing),
[S3](s3/README.md#development-and-testing), and
[RabbitMQ](rabbitmq/README.md#development-and-testing) guides. See
[AGENTS.md](AGENTS.md) for backend implementation guidance.
