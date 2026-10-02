# S3 provider

## Overview

`commander/providers/s3` stores artifact files and immutable reports in Amazon S3
or a compatible service such as MinIO. App factories select it for `s3://`
`STORAGE_URL` values. Accepted artifact metadata stays in the database.

[`S3Store`](store/s3_store.go) holds the bucket client and optional key prefix.
Apps share it between `ArtifactFileS3Store` and `ReportS3Store`.

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
    s3artifacts "commander/providers/s3/artifacts"
    "commander/providers/s3/store"
)

storage, err := store.Open(ctx, "s3://your-bucket/commander")
if err != nil {
    return err
}
files, err := s3artifacts.NewArtifactFileS3Store(storage)
```

The repository workspace includes this module. Private projects use
[local module replacements](../../apps/agents/README.md#develop-an-agent-in-a-private-repository)
for `commander/framework` and `commander/providers/s3`.

## Development and testing

From the repository root, start MinIO and run integration tests with the default
local credentials and port:

```sh
docker compose -f compose.full.yaml up --build --wait minio
docker compose -f compose.full.yaml run --build --rm minio-init
AWS_ENDPOINT_URL_S3=http://127.0.0.1:9000 AWS_REGION=us-east-1 \
AWS_ACCESS_KEY_ID=commander AWS_SECRET_ACCESS_KEY=commander-local-secret \
S3_USE_PATH_STYLE=true COMMANDER_TEST_S3_URL=s3://commander/tests \
  go test -race -count=1 -timeout=3m ./providers/s3/...
go vet ./providers/s3/...
```

Use a dedicated test bucket or prefix. Integration tests skip without
`COMMANDER_TEST_S3_URL` and remove their own objects afterward; cleanup also needs
object listing and deletion permissions. CI runs them in
[test-provider-s3.yml](../../.github/workflows/test-provider-s3.yml). See the
[testing guide](../../framework/tests/README.md) for shared conventions.

### Object layout and writes

Object keys are `<prefix>/artifacts/<artifact-id>/<file-id>` and
`<prefix>/reports/<report-id>.json`; omit `<prefix>/` when no prefix is configured.
Uploads use conditional, single-object PUT requests. Multipart and resumable
uploads are not implemented. Artifact reads verify size and SHA-256; repeating a
report write accepts identical content and rejects conflicting content.

Failed or interrupted work can leave uploaded files without accepted metadata;
automatic orphan cleanup is not implemented.
