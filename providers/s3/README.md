# S3 provider

## Overview

An independent Go module for artifact files and immutable reports in Amazon S3
or an S3-compatible service such as MinIO. Artifact metadata stays in the database.

`S3Store` owns the bucket connection and optional key prefix. Apps inject it into
`ArtifactFileS3Store` and `ReportS3Store`; framework users import only the provider
modules they need.

## Quick start

Start the complete local stack with MongoDB and MinIO:

```sh
docker compose -f compose.full.yaml up --build -d
```

Compose builds pinned upstream MinIO sources and creates the `commander` bucket.
The first build takes longer than subsequent starts. For an existing S3 bucket, configure the
hosts with `STORAGE_URL=s3://your-bucket/optional-prefix`. Object keys are:

- `<prefix>/artifacts/<artifact-id>/<file-id>`
- `<prefix>/reports/<report-id>.json`

| Setting | Purpose |
| --- | --- |
| `AWS_REGION` | Bucket region; defaults to `us-east-1` when unset in SDK configuration |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` | Optional environment credentials |
| `AWS_ENDPOINT_URL_S3` | Custom endpoint for MinIO or another compatible service |
| `S3_USE_PATH_STYLE` | Set `true` for MinIO; defaults to `false` |

The [AWS SDK credential chain](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-gosdk.html)
also supports shared profiles and workload roles. Keep credentials out of
`STORAGE_URL`. The provider opens an existing bucket; it does not create buckets
or change their policies. Hosts need object read/write permissions for their
configured prefix.

```go
import (
    s3artifact "commander/providers/s3/artifact"
    "commander/providers/s3/store"
)

storage, err := store.Open(ctx, "s3://your-bucket/commander")
if err != nil {
    return err
}
files, err := s3artifact.NewArtifactFileS3Store(storage)
```

The workspace includes `commander/providers/s3`. Private projects currently use
local module replacements, as described in the [agent guide](../../apps/agents/README.md#develop-an-agent-in-a-private-repository).

## Development and testing

Run against the local MinIO service:

```sh
docker compose -f compose.full.yaml up --build --wait minio
docker compose -f compose.full.yaml run --build --rm minio-init
AWS_ENDPOINT_URL_S3=http://127.0.0.1:9000 AWS_REGION=us-east-1 \
AWS_ACCESS_KEY_ID=commander AWS_SECRET_ACCESS_KEY=commander-local-secret \
S3_USE_PATH_STYLE=true COMMANDER_TEST_S3_URL=s3://commander/tests \
  go test -race -count=1 -timeout=3m ./providers/s3/...
```

Use a dedicated test bucket or prefix. Integration tests skip without
`COMMANDER_TEST_S3_URL` and remove their own objects afterward. CI runs them in
[test-provider.yml](../../.github/workflows/test-provider.yml).

Uploads use single-object PUT requests with conditional writes; multipart and
resumable uploads are not implemented. Artifact reads verify
size and SHA256; repeating a report write accepts identical content and rejects
conflicting content. A failed action can leave uploaded files without accepted
metadata; automatic orphan cleanup is not implemented.
