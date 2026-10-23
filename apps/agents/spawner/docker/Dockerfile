# syntax=docker/dockerfile:1
FROM golang:1.26.7-bookworm AS build

WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -trimpath -buildvcs=false -o /out/commander-spawner-agent ./apps/agents/spawner/cmd

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN groupadd --gid 1000 commander \
    && useradd --uid 1000 --gid 1000 --create-home commander \
    && install -d -m 700 -o 1000 -g 1000 /home/commander/state/spawner
COPY --from=build /out/commander-spawner-agent /usr/local/bin/commander-spawner-agent
ENV SPAWNER_STATE_DIR=/home/commander/state/spawner
USER 1000:1000
WORKDIR /home/commander
ENTRYPOINT ["/usr/local/bin/commander-spawner-agent"]
