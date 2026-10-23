# syntax=docker/dockerfile:1
FROM golang:1.26.7-bookworm AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -trimpath -buildvcs=false -o /out/fixture-agent ./apps/agents/spawner/tests/e2e/agent && \
    go build -trimpath -buildvcs=false -o /out/fixture-releases ./apps/agents/spawner/tests/e2e/releases

FROM scratch AS agent
COPY --from=build /out/fixture-agent /fixture-agent
USER 1000:1000
ENTRYPOINT ["/fixture-agent"]

FROM debian:bookworm-slim AS releases
RUN apt-get update && apt-get install -y --no-install-recommends curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/fixture-agent /fixtures/fixture-agent
COPY --from=build /out/fixture-releases /usr/local/bin/fixture-releases
COPY apps/admin/missions/default-missions.json /fixtures/default-missions.json
COPY apps/agents/spawner/tests/e2e/missions.json /fixtures/spawner-missions.json
ENTRYPOINT ["/usr/local/bin/fixture-releases"]
