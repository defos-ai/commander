# syntax=docker/dockerfile:1
FROM node:24-alpine AS frontend
WORKDIR /src
COPY apps/admin/ui/frontend/package.json apps/admin/ui/frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY apps/admin/ui/frontend/ ./
RUN npm run build

FROM golang:1.26.7-bookworm AS backend
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOWORK=off
COPY apps/admin/go.mod ./
COPY apps/admin/ui/backend/ ./ui/backend/
COPY --from=frontend /src/dist ./ui/backend/static/dist
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -tags embed_ui -trimpath -buildvcs=false -o /out/commander-ui ./ui/backend/cmd/commander-ui

FROM alpine:3
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=backend /out/commander-ui /usr/local/bin/commander-ui
USER 1000:1000
EXPOSE 4275
ENTRYPOINT ["/usr/local/bin/commander-ui"]
