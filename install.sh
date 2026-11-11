#!/bin/sh
set -eu

repository=https://github.com/defos-ai/commander
version=latest
bin_dir=${HOME:+$HOME/.local/bin}
dry_run=false
force=false
temporary=
staged=

fail() {
    printf 'commander installer: %s\n' "$1" >&2
    exit 1
}

usage() {
    cat <<'USAGE'
Usage: sh install.sh [--version latest|vX.Y.Z] [--bin-dir directory] [--dry-run] [--force]

Install a checksum-verified Commander binary for Linux or macOS.
The default destination is ~/.local/bin/commander. No services or client settings change.
Use --dry-run to review the release and destination before installing.
Use --force to replace a different existing regular file.
USAGE
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version|--bin-dir)
            [ "$#" -ge 2 ] && [ -n "$2" ] || fail 'an option value is missing'
            if [ "$1" = --version ]; then version=$2; else bin_dir=$2; fi
            shift 2
            ;;
        --dry-run) dry_run=true; shift ;;
        --force) force=true; shift ;;
        --help|-h) usage; exit 0 ;;
        *) fail 'unknown option; use --help' ;;
    esac
done

[ -n "$bin_dir" ] || fail 'HOME is unavailable; select --bin-dir'
case "$bin_dir" in /*) ;; *) bin_dir=$(pwd)/$bin_dir ;; esac
destination=$bin_dir/commander

case $(uname -s) in
    Linux) platform=linux ;;
    Darwin) platform=darwin ;;
    *) fail 'supported systems are Linux and macOS' ;;
esac
case $(uname -m) in
    x86_64|amd64) architecture=amd64 ;;
    aarch64|arm64) architecture=arm64 ;;
    *) fail 'supported architectures are amd64 and arm64' ;;
esac
command -v curl >/dev/null 2>&1 || fail 'curl is required'

if [ "$version" = latest ]; then
    location=$(curl --fail --silent --show-error --head --proto '=https' --proto-redir '=https' \
        --connect-timeout 15 --max-time 30 --output /dev/null --write-out '%{redirect_url}' \
        "$repository/releases/latest") || fail 'could not resolve the latest release'
    case "$location" in
        "$repository/releases/tag/"*) version=${location#"$repository/releases/tag/"} ;;
        *) fail 'latest did not resolve to a Commander release tag' ;;
    esac
fi
case "$version" in *[!v0-9.]*|'') fail 'version must be latest or vMAJOR.MINOR.PATCH' ;; esac
printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || fail 'version must be latest or vMAJOR.MINOR.PATCH'

asset=commander_${platform}_${architecture}
release=$repository/releases/download/$version

check_destination() {
    [ ! -L "$destination" ] || fail 'destination is a symbolic link; choose another --bin-dir'
    if [ -e "$destination" ] && [ ! -f "$destination" ]; then
        fail 'destination must be a regular file'
    fi
}

check_destination
printf 'Version: %s\nPlatform: %s/%s\nDownload: %s/%s\nInstall to: %s\n' \
    "$version" "$platform" "$architecture" "$release" "$asset" "$destination"
if "$dry_run"; then
    if [ -f "$destination" ] && ! "$force"; then
        printf 'An existing file will be kept unless its checksum matches or --force is supplied.\n'
    fi
    printf 'Preview only; no files installed.\n'
    exit 0
fi

if command -v sha256sum >/dev/null 2>&1; then
    checksum_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
    checksum_tool=shasum
else
    fail 'sha256sum or shasum is required'
fi

cleanup() {
    if [ -n "$staged" ]; then rm -f "$staged"; fi
    if [ -n "$temporary" ]; then rm -rf "$temporary"; fi
}
trap cleanup 0
trap 'exit 1' 1 2 3 15
temporary=$(mktemp -d "${TMPDIR:-/tmp}/commander-install.XXXXXX") || fail 'could not create a temporary directory'

download() {
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --connect-timeout 15 --max-time 180 --output "$2" "$1"
}

file_checksum() {
    if [ "$checksum_tool" = sha256sum ]; then
        sha256sum < "$1"
    else
        shasum -a 256 < "$1"
    fi | awk '{print tolower($1)}'
}

download "$release/checksums.txt" "$temporary/checksums.txt" || fail 'could not download release checksums'
expected=$(awk -v name="$asset" '
    {
        references_asset = 0
        for (field = 1; field <= NF; field++) {
            if ($field == name || $field == "*" name) references_asset = 1
        }
        if (references_asset) {
            count++
            hash = $1
            if (NF != 2 || ($2 != name && $2 != "*" name) || length(hash) != 64 || hash !~ /^[0-9a-fA-F]+$/) invalid = 1
        }
    }
    END {
        if (count != 1 || invalid) exit 1
        print tolower(hash)
    }
' "$temporary/checksums.txt") || fail 'release checksums must contain exactly one valid entry for this binary'

check_destination
if [ -f "$destination" ]; then
    if [ "$(file_checksum "$destination")" = "$expected" ]; then
        chmod 755 "$destination"
        printf 'Commander %s is already installed at %s.\n' "$version" "$destination"
        exit 0
    fi
    "$force" || fail 'a different file already exists; review it and rerun with --force to replace it'
fi

download "$release/$asset" "$temporary/$asset" || fail 'could not download the Commander binary'
[ "$(file_checksum "$temporary/$asset")" = "$expected" ] || fail 'binary checksum does not match the release'

mkdir -p "$bin_dir" || fail 'cannot create the install directory; choose a writable --bin-dir'
staged=$(mktemp "$bin_dir/.commander.XXXXXX") || fail 'cannot stage the binary in the install directory'
cat "$temporary/$asset" > "$staged"
chmod 755 "$staged"
check_destination
if "$force"; then
    mv -f "$staged" "$destination" || fail 'could not replace the destination'
else
    ln "$staged" "$destination" || fail 'destination changed during installation; existing file was preserved'
    rm -f "$staged"
fi
staged=
printf 'Installed Commander %s at %s.\nAdd %s to PATH if needed.\n' "$version" "$destination" "$bin_dir"
