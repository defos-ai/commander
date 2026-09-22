# syntax=docker/dockerfile:1
FROM golang:1.26.7-bookworm AS build

WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -trimpath -buildvcs=false -o /out/commander-web-quality-agent ./apps/agents/web-quality/cmd

FROM debian:bookworm-slim
# Browser dependencies belong to the agent image; runtime configuration comes
# from Compose or the hosting environment.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates chromium fonts-liberation \
    && rm -rf /var/lib/apt/lists/*
# Chromium needs a real non-root user's writable home for its crash handler.
RUN groupadd --gid 1000 commander \
    && useradd --uid 1000 --gid 1000 --create-home commander
COPY --from=build /out/commander-web-quality-agent /usr/local/bin/commander-web-quality-agent
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/commander-web-quality-agent"]
