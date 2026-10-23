# syntax=docker/dockerfile:1
FROM node:24-alpine AS frontend
WORKDIR /src
COPY apps/admin/ui/frontend/package.json apps/admin/ui/frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY apps/admin/ui/frontend/ ./
RUN npm run build

FROM golang:1.26.7-bookworm AS backend
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY . .
COPY --from=frontend /src/dist ./apps/admin/ui/backend/static/dist
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -tags embed_ui -trimpath -buildvcs=false -o /out/commander-admin ./apps/admin/cmd/commander-admin

FROM alpine:3
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=backend /out/commander-admin /usr/local/bin/commander-admin
USER 1000:1000
EXPOSE 4275
ENTRYPOINT ["/usr/local/bin/commander-admin"]
CMD ["serve"]
