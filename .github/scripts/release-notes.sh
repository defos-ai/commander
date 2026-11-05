#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 2 ]]; then
  echo 'Usage: release-notes.sh <markdown-file> <section-title>' >&2
  exit 1
fi

awk -v heading="## $2" '
  /^## / {
    selected = ($0 == heading)
    if (selected) matches++
    next
  }
  selected {
    lines[++count] = $0
    if ($0 ~ /[^[:space:]]/) {
      if (!first) first = count
      last = count
    }
  }
  END {
    if (matches != 1 || !first) {
      printf "Expected one nonempty release-notes section: %s\n", heading > "/dev/stderr"
      exit 1
    }
    for (i = first; i <= last; i++) print lines[i]
  }
' "$1"
