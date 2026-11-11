package fakes

const InstallerCurl = `#!/bin/sh
set -eu
output=
write_out=
https=false
redirect_https=false
head=false
url=
for argument in "$@"; do
    printf '%s\n' "$argument" >> "$INSTALLER_TEST_CALLS"
done
while [ "$#" -gt 0 ]; do
    case "$1" in
        --output) output=$2; shift 2 ;;
        --write-out) write_out=$2; shift 2 ;;
        --proto) [ "$2" = '=https' ]; https=true; shift 2 ;;
        --proto-redir) [ "$2" = '=https' ]; redirect_https=true; shift 2 ;;
        --connect-timeout|--max-time) shift 2 ;;
        --head) head=true; shift ;;
        --fail|--silent|--show-error|--location) shift ;;
        https://*) url=$1; shift ;;
        *) exit 90 ;;
    esac
done
"$https" && "$redirect_https"
[ -n "$url" ]
printf '%s\n' "$url" >> "$INSTALLER_TEST_URLS"
if "$head"; then
    [ "$url" = 'https://github.com/defos-ai/commander/releases/latest' ]
    [ "$write_out" = '%{redirect_url}' ]
    printf '%s' "$INSTALLER_TEST_LATEST"
else
    [ "$url" = "https://github.com/defos-ai/commander/releases/download/v1.2.3/${url##*/}" ]
    cp "$INSTALLER_TEST_ASSETS/${url##*/}" "$output"
fi
`

const InstallerUname = `#!/bin/sh
set -eu
case "$1" in
    -s) printf '%s\n' "$INSTALLER_TEST_OS" ;;
    -m) printf '%s\n' "$INSTALLER_TEST_ARCH" ;;
    *) exit 91 ;;
esac
`
