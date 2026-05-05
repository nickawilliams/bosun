#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPLETION_FILE="${SCRIPT_DIR}/bosun.bash"

if [[ ! -f "${COMPLETION_FILE}" ]]; then
  echo "FAIL: completion file not found: ${COMPLETION_FILE}" >&2
  exit 1
fi

# Load the completion script. Cobra V2 generators expect bash-completion's
# helpers to exist; provide minimal stubs so the script loads cleanly even on
# a stripped-down runner (CI ubuntu-latest has bash-completion, but local
# macOS bash 3.x does not).
_get_comp_words_by_ref() { :; }
__bosun_init_completion() { COMPREPLY=(); }
complete() { :; }

# shellcheck disable=SC1090
source "${COMPLETION_FILE}"

# Verify the entrypoint and helpers exist after sourcing.
for fn in __start_bosun __bosun_get_completion_results __bosun_process_completion_results; do
  if ! declare -F "${fn}" >/dev/null; then
    echo "FAIL: expected function ${fn} to be defined after sourcing" >&2
    exit 1
  fi
done

echo "bash completion smoke test passed"
