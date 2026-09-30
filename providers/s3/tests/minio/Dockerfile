# syntax=docker/dockerfile:1

FROM golang:1.26.7-bookworm AS minio-build
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go install github.com/minio/minio@RELEASE.2025-10-15T17-29-55Z

FROM golang:1.26.7-bookworm AS mc-build
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go install github.com/minio/mc@RELEASE.2025-08-13T08-35-41Z

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

FROM runtime AS minio
COPY --from=minio-build /go/bin/minio /usr/local/bin/minio
ENTRYPOINT ["minio"]

FROM runtime AS mc
COPY --from=mc-build /go/bin/mc /usr/local/bin/mc
ENTRYPOINT ["mc"]
