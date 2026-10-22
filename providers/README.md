# Providers

Choose messaging, database and file storage independently:

| Host setting | Provider | Connection |
| --- | --- | --- |
| `BUS_URL` | [Filesystem](fs/README.md) or [RabbitMQ](rabbitmq/README.md) | Local path, `file:///`, `fs:///`, `amqp://`, `amqps://` |
| `DATABASE_URL` | [Filesystem](fs/README.md) or [MongoDB](mongo/README.md) | Local path, `file:///`, `fs:///`, `mongodb://`, `mongodb+srv://` |
| `STORAGE_URL` | [Filesystem](fs/README.md) or [S3/MinIO](s3/README.md) | Local path, `file:///`, `fs:///`, `s3://bucket/prefix` |

## Quick start

All-in-one uses the filesystem provider. [Compose](../compose.yaml) configures
RabbitMQ, MongoDB and MinIO automatically. For another deployment, set the URLs
above in each host; see [deployment](../docs/deployment.md#configuration).

Each provider is a separate Go module. Import the domain store or bus you need
from `github.com/defos-ai/commander/providers/<provider>`.

## Development and testing

Follow the setup and test commands for [FS](fs/README.md#development-and-testing),
[MongoDB](mongo/README.md#development-and-testing),
[RabbitMQ](rabbitmq/README.md#development-and-testing) or
[S3](s3/README.md#development-and-testing).

See [AGENTS.md](AGENTS.md) for provider implementation rules.
