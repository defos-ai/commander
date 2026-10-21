# syntax=docker/dockerfile:1
FROM node:24.14.0-bookworm-slim AS codex
# Keep workspace isolation compatible with default container permissions.
RUN npm install --global --ignore-scripts --omit=dev @openai/codex@0.114.0 \
    && mkdir /opt/codex \
    && cp -a /usr/local/lib/node_modules/@openai/codex/node_modules/@openai/codex-linux-*/vendor/*/. /opt/codex/

FROM golang:1.26.7-bookworm AS build

WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -trimpath -buildvcs=false -o /out/commander-builder-agent ./apps/agents/builder/cmd

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git \
    && rm -rf /var/lib/apt/lists/*
RUN groupadd --gid 1000 commander \
    && useradd --uid 1000 --gid 1000 --create-home commander \
    && install -d -m 700 -o 1000 -g 1000 /home/commander/workspaces
COPY --from=build /out/commander-builder-agent /usr/local/bin/commander-builder-agent
COPY --from=build /usr/local/go /usr/local/go
COPY --from=codex /opt/codex /opt/codex
ENV PATH=/opt/codex/codex:/opt/codex/path:/usr/local/go/bin:${PATH} \
    GOTOOLCHAIN=local \
    BUILDER_WORKSPACE_DIR=/home/commander/workspaces
USER 1000:1000
WORKDIR /home/commander
ENTRYPOINT ["/usr/local/bin/commander-builder-agent"]
