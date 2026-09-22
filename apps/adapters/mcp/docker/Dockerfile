# syntax=docker/dockerfile:1
FROM golang:1.26.7-bookworm AS build

WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd apps/adapters/mcp && go build -trimpath -buildvcs=false -o /out/commander-mcp ./cmd/commander-mcp

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/commander-mcp /usr/local/bin/commander-mcp
USER 1000:1000
EXPOSE 4273
ENTRYPOINT ["/usr/local/bin/commander-mcp"]
CMD ["-listen-address", ":4273"]
