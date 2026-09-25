#!/bin/bash

# Starts every server at once and leaves them running: what a server node of a
# two-node run does, since the client node cannot start or stop them itself. A
# single-node run starts each server just before its turn instead; see
# script/clients.sh.

# Flags for every server, set by the driver that sources this: the ones the
# servers define, such as -nodelay, with the benchmark client's own filtered
# out. Read from a variable rather than from "$@" because `source file` with no
# arguments leaves the caller's positional parameters in place, which is how
# the client's flags used to reach the servers.
if [ -z "${server_flags+set}" ]; then
    # Invoked directly rather than sourced: take our own arguments.
    server_flags="$*"
fi

. ./script/serverctl.sh || { return 1 2>/dev/null || exit 1; }

servers_failed=()
for f in ${frameworks[@]}; do
    echo
    bench_start_server "$f" || servers_failed+=("$f")
done

if [ "${#servers_failed[@]}" -ne 0 ]; then
    echo
    echo "servers that did not start: ${servers_failed[*]}" >&2
    return 1 2>/dev/null || exit 1
fi
