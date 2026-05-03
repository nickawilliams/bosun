#!/usr/bin/env zsh
set -euo pipefail

script_dir=${0:A:h}
completion_file="${script_dir}/bosun.zsh"

if [[ ! -f "${completion_file}" ]]; then
  print -u2 -- "FAIL: completion file not found: ${completion_file}"
  exit 1
fi

# Stub compdef. Cobra's zsh script invokes it at top level to register the
# completion function, but compdef is only available after compinit has been
# loaded. The smoke test just needs the script to source cleanly.
compdef() { :; }

# Load the completion script. The script ends with a guard that only invokes
# `_bosun` when zsh is calling it as a completion function, so sourcing it
# here just defines the functions.
source "${completion_file}"

for fn in _bosun __bosun_debug; do
  if ! whence -w "${fn}" >/dev/null; then
    print -u2 -- "FAIL: expected function ${fn} to be defined after sourcing"
    exit 1
  fi
done

# Verify the #compdef directive registered bosun (zsh writes it as part of the
# generated header — re-check the file directly since #compdef is a parser
# directive consumed by compinit, not a runtime function).
if ! grep -q '^#compdef bosun$' "${completion_file}"; then
  print -u2 -- "FAIL: missing #compdef bosun directive"
  exit 1
fi

print -- "zsh completion smoke test passed"
