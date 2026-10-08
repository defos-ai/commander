# Working on the S3 provider

Inherit [provider guidance](../AGENTS.md). This module stores artifact files and
reports in S3-compatible object storage; accepted metadata stays in the database.

## Configuration and object writes

- Keep AWS configuration, URL validation and SDK error mapping in
  [`store`](store/s3_store.go). Use the default credential chain and an existing
  bucket; opening a store must not create buckets or change policies.
- Preserve `s3://bucket/optional-prefix`, `AWS_ENDPOINT_URL_S3` and explicit
  `S3_USE_PATH_STYLE` handling. Keep credentials out of URLs and diagnostics;
  wrapper errors must retain their causes without exposing SDK request secrets.
- Keep object keys scoped to the configured prefix and the artifact/report
  namespaces. Validate keys before I/O; test cleanup must remain inside its prefix.
- Use conditional single-object PUT with `If-None-Match: *` and SHA-256 checksums.
  Existing-object conflicts must never overwrite immutable content. Current uploads
  require a seekable body; do not imply multipart or resumable-upload support.
- Artifact uploads calculate size and digest, rewind, then upload. Reads check
  expected size and SHA-256; preserve missing-object mapping to domain errors.
- A report conflict requires reading and comparing the existing report so identical
  retries succeed while different content returns `reports.ErrReportConflict`.
- Unreferenced uploads can remain after failed work. Do not add orphan deletion to
  ordinary reads or writes; metadata acceptance and byte retention have distinct owners.

## Validation

Use a dedicated bucket/prefix and the MinIO startup/bucket initialization commands
in [README.md](README.md#development-and-testing). With that setup and its default
test credentials, run from the repository root:

```sh
AWS_ENDPOINT_URL_S3=http://127.0.0.1:9000 AWS_REGION=us-east-1 \
AWS_ACCESS_KEY_ID=commander-test AWS_SECRET_ACCESS_KEY=commander-test-secret \
S3_USE_PATH_STYLE=true COMMANDER_TEST_S3_URL=s3://commander-test/provider-tests \
  go test -race -count=1 -timeout=3m ./providers/s3/...
go vet ./providers/s3/...
```

Integration tests skip without `COMMANDER_TEST_S3_URL`. Fixtures create unique
prefixes and delete their own objects, requiring list/delete permissions in
addition to reads/writes. Cover corrupt content, conditional write conflicts and
cancellation when changing object I/O. See the
[integration CI](../../.github/workflows/test-integration.yml) for the service gate.
