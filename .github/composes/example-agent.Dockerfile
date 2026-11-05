# syntax=docker/dockerfile:1
FROM golang:1.26.7-bookworm AS build

WORKDIR /src/commander
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOWORK=/src/commander/go.work
COPY . .
COPY --from=example-agent-source . /src/example-agent
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go work use /src/example-agent \
    && go build -trimpath -buildvcs=false -o /out/commander-example-agent ../example-agent/cmd

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /state \
    && chown 1000:1000 /state
COPY --from=build /out/commander-example-agent /usr/local/bin/commander-example-agent
ENV BUS_URL=file:///state/bus STORAGE_URL=file:///state/storage
USER 1000:1000
WORKDIR /state
ENTRYPOINT ["/usr/local/bin/commander-example-agent"]
