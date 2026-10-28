package fakes

const ComposeCredentialsConsumer = `#!/bin/sh
set -eu
if [ "${1:-}" = credentials ]; then
  test "$2" = --raw
  case "$0" in
    *commander-mcp)
      value="$MCP_TOKEN"
      case "$value" in ''|*[!a-zA-Z0-9._~+/-]*) exit 1 ;; esac
      ;;
    *commander-api)
      value="$API_PASSWORD"
      test -n "$(printf '%s' "$value" | tr -d '[:space:]')"
      case "$value" in *'
'*|*'	'*) exit 1 ;; esac
      ;;
  esac
  printf '%s\n' "$value"
else
  case "$0" in
    *commander-mcp) printf '%s' "$MCP_TOKEN" > "$FIXTURE_DIR/observed-mcp" ;;
    *commander-api) printf '%s' "$API_PASSWORD" > "$FIXTURE_DIR/observed-api" ;;
    *commander-admin) printf '%s' "$COMMANDER_API_PASSWORD" > "$FIXTURE_DIR/observed-api" ;;
    *)
      printf '%s' "$COMMANDER_MCP_TOKEN" > "$FIXTURE_DIR/observed-mcp"
      printf '%s' "$COMMANDER_API_PASSWORD" > "$FIXTURE_DIR/observed-api"
      ;;
  esac
  printf '%s\n' "$@" > "$FIXTURE_DIR/observed-arguments"
  printf '%s' "$$" > "$FIXTURE_DIR/after-pid"
fi
`

const ComposeSecretsConsumer = `#!/bin/sh
set -eu
printf '%s' "$SECRETS_ENCRYPTION_KEY" > "$FIXTURE_DIR/observed-key"
printf '%s' "$API_PASSWORD" > "$FIXTURE_DIR/observed-password"
printf '%s' "$$" > "$FIXTURE_DIR/after-pid"
printf '%s\n' "$@" > "$FIXTURE_DIR/observed-arguments"
`

const NativeGitHub = `#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >> "$STATE/gh.log"
case "$1 $2" in
  'api --paginate')
    [[ "$3 $4 $5" == 'repos/defos-ai/commander/releases?per_page=100 --jq .[]' ]]
    printf '%s\n' "$RELEASES"
    exit "$GH_STATUS"
    ;;
  'release create')
    [[ "$*" == "release create $RELEASE_TAG --verify-tag --draft --title $RELEASE_TITLE --generate-notes --latest=false" ]]
    ;;
  'release download')
    [[ "$3" == "$RELEASE_TAG" && "$4" == '--dir' && $# == 5 ]]
    [[ "${FAIL_DOWNLOAD:-0}" == 0 ]] || exit 1
    cp -a "$STATE/published/." "$5/"
    ;;
  'release upload')
    [[ "${3}" == "$RELEASE_TAG" && "${@: -1}" == '--clobber' ]]
    [[ "${FAIL_UPLOAD:-0}" == 0 ]] || exit 1
    mkdir "$STATE/uploaded"
    shift 3
    for asset in "$@"; do
      [[ "$asset" == '--clobber' ]] || cp "$asset" "$STATE/uploaded/"
    done
    ;;
  'release edit')
    [[ "$*" == "release edit $RELEASE_TAG --draft=false --latest=false" ]]
    ;;
  *) exit 1 ;;
esac
`

const NativeGoBuild = `#!/bin/bash
set -euo pipefail
[[ "$1" == build ]]
case "${@: -1}" in
  ./apps/playground/cmd/commander)
    [[ " $* " == *' -tags embed_ui '* ]]
    prefix=commander
    ;;
  ./apps/admin/cmd/commander-admin)
    [[ " $* " == *' -tags embed_ui '* ]]
    prefix=commander-admin
    ;;
  ./apps/agents/spawner/cmd) prefix=commander-spawner-agent ;;
  ./apps/agents/builder/cmd) prefix=commander-builder-agent ;;
  *) exit 1 ;;
esac
while [[ "$1" != '-o' ]]; do shift; done
[[ "$2" == "dist/${prefix}_${GOOS}_${GOARCH}"* ]]
cat > "$2" <<'BINARY'
#!/bin/bash
[[ "$*" == '--help' ]]
printf 'help called\n' > "$STATE/help-${0##*/}"
BINARY
chmod +x "$2"
`

const StableGitHub = `#!/bin/bash
set -euo pipefail
case "$1" in
  api)
    [[ "$*" == 'api --paginate repos/defos-ai/commander/releases?per_page=100' ]]
    printf '%s\n' "$RELEASES"
    exit "${GH_STATUS:-0}"
    ;;
  release)
    [[ "$*" == "release edit $VERSION --latest" ]]
    printf '%s\n' "$*" >> "$STATE/gh.log"
    printf 'gh %s\n' "$*" >> "$STATE/operations.log"
    exit "${LATEST_STATUS:-0}"
    ;;
  *) exit 1 ;;
esac
`

const StableDocker = `#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >> "$STATE/docker.log"
printf 'docker %s\n' "$*" >> "$STATE/operations.log"
[[ "$1 $2" == 'buildx imagetools' ]]
case "$3" in
  create)
    [[ $# == 7 && "$4 $5" == '--prefer-index=false --tag' ]]
    name="${6##*/}"; name="${name%:stable}"
    if [[ "$name" == "${FAIL_IMAGE:-}" && ! -e "$STATE/failed" ]]; then
      touch "$STATE/failed"
      exit 1
    fi
    printf '%s\n' "${7##*@}" > "$STATE/$name"
    ;;
  inspect)
    [[ $# == 6 && "$5" == '--format' && "$6" == '{{json .Manifest}}' ]]
    name="${4##*/}"; name="${name%:stable}"
    digest=$(cat "$STATE/$name")
    [[ "$name" != "${MISMATCH_IMAGE:-}" ]] || digest=sha256:wrong
    printf '{"digest":"%s"}\n' "$digest"
    ;;
  *) exit 1 ;;
esac
`

const AgentTagGit = `#!/bin/bash
set -euo pipefail
[[ "$*" == "rev-parse refs/tags/$TAG^{commit}" ]]
[[ "${MISSING_TAG:-0}" == 0 ]] || exit 1
printf '%s\n' "$TAG_SHA"
`

const AgentManifestGo = `#!/bin/bash
set -euo pipefail
[[ "$1 $2" == 'run ./.github/scripts/agent-manifest.go' ]]
shift 2
exec "$GENERATOR" "$@"
`
