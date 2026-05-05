#!/usr/bin/env bash
set -euo pipefail

current=$(make --no-print-directory version)
prev_tag=$(git describe --tags --abbrev=0 HEAD 2>/dev/null || echo v0.0.0)

clean_prev=${prev_tag#v}
clean_curr=${current#v}

prev_major=$(printf '%s' "$clean_prev" | cut -d. -f1)
prev_minor=$(printf '%s' "$clean_prev" | cut -d. -f2)
prev_patch=$(printf '%s' "$clean_prev" | cut -d. -f3)

curr_major=$(printf '%s' "$clean_curr" | cut -d. -f1)
curr_minor=$(printf '%s' "$clean_curr" | cut -d. -f2)
curr_patch=$(printf '%s' "$clean_curr" | cut -d. -f3)

prev_major=${prev_major:-0}; prev_minor=${prev_minor:-0}; prev_patch=${prev_patch:-0}
curr_major=${curr_major:-0}; curr_minor=${curr_minor:-0}; curr_patch=${curr_patch:-0}

bump=patch
if [ "$curr_major" != "$prev_major" ]; then
    bump=major
elif [ "$curr_minor" != "$prev_minor" ]; then
    bump=minor
elif [ "$curr_patch" != "$prev_patch" ]; then
    bump=patch
fi

echo "$bump"
