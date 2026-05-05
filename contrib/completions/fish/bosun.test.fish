#!/usr/bin/env fish

function fail
    printf 'FAIL: %s\n' $argv >&2
    exit 1
end

set script_dir (dirname (status --current-filename))
set completion_file "$script_dir/bosun.fish"

if not test -f "$completion_file"
    fail "completion file not found: $completion_file"
end

# Loading the file registers completions for `bosun`. A `type -q bosun` check
# inside the file is gated, so sourcing without bosun on PATH is still safe.
source "$completion_file"
or fail "failed to source $completion_file"

for fn in __bosun_perform_completion __bosun_prepare_completions
    if not functions -q $fn
        fail "expected function $fn to be defined after sourcing"
    end
end

# Verify at least one completion is registered for bosun.
if not complete -c bosun | string length -q
    fail "no completions registered for bosun"
end

printf 'fish completion smoke test passed\n'
