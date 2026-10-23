#!/bin/sh
set -eu

API_USERNAME="${API_USERNAME:-commander}"
API_PASSWORD="${API_PASSWORD:-$(openssl rand -hex 32)}"
MCP_TOKEN="${MCP_TOKEN:-$(openssl rand -hex 32)}"
export API_USERNAME API_PASSWORD MCP_TOKEN

exec /usr/bin/tini -- /usr/local/bin/commander --all-in-one --serve-admin-ui "$@"
