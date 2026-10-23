# S3 provider

Store artifact files and reports in Amazon S3 or MinIO. Metadata stays in the
database. [Compose](../../compose.yaml) configures MinIO and creates its bucket.

## Quick start

For an existing bucket, set:

```sh
STORAGE_URL=s3://your-bucket/commander
```

| Setting | Set it for… |
| --- | --- |
| `AWS_REGION` | Your bucket's region. |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` | Environment credentials, if not using another AWS credential source. |
| `AWS_ENDPOINT_URL_S3` | MinIO or another compatible service. |
| `S3_USE_PATH_STYLE=true` | MinIO. |

Use the AWS default credential chain; keep credentials out of `STORAGE_URL`.
Create the bucket first and grant object read/write access to its prefix.

To embed artifact file storage:

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

## Development and testing

Use a dedicated test bucket or prefix with list/delete permissions too.
From the repository root:

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

Tests skip without `COMMANDER_TEST_S3_URL` and delete their own objects.
Always stop the stack, including after failures.

## Object layout and writes

Keep the configured prefix dedicated to Commander. Files use `artifacts/` and
reports use `reports/` beneath it. Failed work can leave unreferenced uploads;
automatic orphan cleanup is unavailable. See [AGENTS.md](AGENTS.md) before
changing object writes or cleanup.
