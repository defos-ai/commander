# S3 provider

Keep mission artifacts and final reports in your organization's Amazon S3 or
MinIO storage for operational review and reuse across agent hosts. File bytes
remain independent of agent runtimes; accepted artifact metadata stays in the
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

Without URL credentials, the AWS default credential chain supplies authentication.
For a static key pair, you can instead set:

```sh
STORAGE_URL='s3://ACCESS_KEY_ID:SECRET_ACCESS_KEY@your-bucket/commander'
```

The username is the access key ID and the password is the secret access key.
AWS secret keys can be pasted unchanged: `/`, `+`, and `=` need no encoding.
Existing percent-encoded credentials also work. Encode URL delimiters in either
credential, such as `@` as `%40`, `?` as `%3F`, `#` as `%23`, and a literal `%` as
`%25`. URL credentials override other credential sources for this S3 client and
do not use `AWS_SESSION_TOKEN`. For temporary credentials, use the AWS credential
chain with the matching token; on AWS, prefer an IAM role.
Keep the full credential-bearing `STORAGE_URL` in your deployment's secret
configuration. Region, endpoint and path-style settings still use the environment
variables above.

Spawned agents receive the full storage URL and must support this credential
format, including raw `/` in secret keys. Update older agents, or use a
credential-free URL with AWS environment credentials when running them.

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

Explore [11 practical Commander use cases](https://github.com/defos-ai/commander/blob/main/docs/use-cases.md):
security operations, authorized security testing, compliance monitoring,
multi-agent development, incident response, prospect research and outreach,
competitive intelligence, data processing, ticket resolution, infrastructure
monitoring and long-running research for universities and R&D centers.
