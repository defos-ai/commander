# S3 provider

## Overview

`github.com/defos-ai/commander/providers/s3` stores artifact files and immutable
reports in Amazon S3 or a compatible service such as MinIO. App factories select it for `s3://`
`STORAGE_URL` values. Accepted artifact metadata stays in the database.

## Quick start

Start the complete local stack with RabbitMQ, MongoDB, and MinIO:

```sh
docker compose -f compose.full.yaml up --build -d
```

Compose builds pinned MinIO sources and creates the `commander` bucket. The
first build takes longer than subsequent starts. For an existing S3 bucket,
set `STORAGE_URL=s3://your-bucket/optional-prefix`.

| Setting | Purpose |
| --- | --- |
| `AWS_REGION` | Bucket region; the provider uses `us-east-1` when SDK configuration has no region |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` | Environment credentials, when used |
| `AWS_ENDPOINT_URL_S3` | Custom endpoint for MinIO or another compatible service |
| `S3_USE_PATH_STYLE` | Set `true` for MinIO; defaults to `false` |

The provider uses the AWS SDK's default configuration and credential chain.
Keep credentials out of `STORAGE_URL`. It uses an existing bucket and does not
create buckets or change policies. Hosts need object read/write permissions for
the configured prefix.

```go
import (
    s3artifacts "github.com/defos-ai/commander/providers/s3/artifacts"
    "github.com/defos-ai/commander/providers/s3/store"
)

storage, err := store.Open(ctx, "s3://your-bucket/commander")
if err != nil {
    return err
}
files, err := s3artifacts.NewArtifactFileS3Store(storage)
```

The repository workspace includes this module. Private projects use
[local module replacements](../../apps/agents/README.md#develop-an-agent-in-a-private-repository)
for the framework and this provider.

## Development and testing

See [AGENTS.md](AGENTS.md) for object-storage implementation guidance.

From the repository root, start isolated MinIO services and run integration tests:

```sh
docker compose -f .github/composes/compose.providers.test.yaml up --build --wait minio
docker compose -f .github/composes/compose.providers.test.yaml run --build --rm minio-init
AWS_ENDPOINT_URL_S3=http://127.0.0.1:9000 AWS_REGION=us-east-1 \
AWS_ACCESS_KEY_ID=commander-test AWS_SECRET_ACCESS_KEY=commander-test-secret \
S3_USE_PATH_STYLE=true COMMANDER_TEST_S3_URL=s3://commander-test/provider-tests \
  go test -race -count=1 -timeout=3m ./providers/s3/...
go vet ./providers/s3/...
docker compose -f .github/composes/compose.providers.test.yaml down --volumes
```

Use a dedicated test bucket or prefix. Integration tests skip without
`COMMANDER_TEST_S3_URL` and remove their own objects afterward; cleanup also needs
object listing and deletion permissions. See the
[testing guide](../../framework/tests/README.md) for full-stack checks.
Always stop test services, including after failures.

## Object layout and writes

Object keys are `<prefix>/artifacts/<artifact-id>/<file-id>` and
`<prefix>/reports/<report-id>.json`; omit `<prefix>/` when no prefix is configured.
Uploads use conditional, single-object PUT requests. Multipart and resumable
uploads are not implemented. Artifact reads verify size and SHA-256; repeating a
report write accepts identical content and rejects conflicting content.

Failed or interrupted work can leave uploaded files without accepted metadata;
automatic orphan cleanup is not implemented.
