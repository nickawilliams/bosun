#!/usr/bin/env bash
set -euo pipefail

event="${EVENT:-}"
bump="${BUMP:-}"
immediate="${IMMEDIATE_BUMPS:-major}"

if [ -z "$event" ]; then
    echo "ERROR: EVENT is required" >&2
    exit 1
fi

# Schedule and manual dispatch always proceed; downstream
# tag-exists check no-ops if there's nothing new to release.
if [ "$event" != "push" ]; then
    echo "release"
    exit 0
fi

# Push events only release when the bump type is in the
# configured immediate-release list.
if [ -z "$bump" ]; then
    echo "ERROR: BUMP is required for push events" >&2
    exit 1
fi
case ",${immediate}," in
    *",${bump},"*) echo "release" ;;
    *)             echo "skip"    ;;
esac
