# syntax=docker/dockerfile:1
# Development only. Compose supplies the package and binary for each service.
FROM golang:1.26.7-bookworm AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
ARG DELVE_VERSION=v1.27.2
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go install github.com/go-delve/delve/cmd/dlv@${DELVE_VERSION}
COPY . .
ARG PACKAGE
ARG BINARY
# Retain /src paths and disable optimization/inlining for VS Code breakpoints.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -buildvcs=false -gcflags="all=-N -l" -o "/out/${BINARY}" "${PACKAGE}"

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl openssl bash \
    && rm -rf /var/lib/apt/lists/*
RUN groupadd --gid 1000 commander \
    && useradd --uid 1000 --gid 1000 --create-home commander
COPY --from=build /go/bin/dlv /usr/local/bin/dlv
COPY --from=build /out/ /usr/local/bin/
USER 1000:1000
WORKDIR /home/commander
EXPOSE 2345
ENTRYPOINT ["/usr/local/bin/dlv"]
