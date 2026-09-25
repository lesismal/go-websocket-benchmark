#!/bin/bash

# The servers' ports, read from config.Ports in config/config.go so that the
# port map has one home. Sourced from the repository root; defines functions
# only.

# The framework's benchmark ports, "min:max".
bench_server_port_range() {
    awk -v want="$1" '
        /^const \(/ { inconst = 1; next }
        inconst && /^\)/ { inconst = 0 }
        inconst && $2 == "=" { v = $3; gsub(/"/, "", v); name[$1] = v }
        /^var Ports = map/ { inports = 1; next }
        inports && /^}/ { exit }
        inports && NF >= 2 {
            key = $1; sub(/:$/, "", key)
            v = $2; gsub(/[",]/, "", v)
            if (name[key] == want) { print v; exit }
        }
    ' ./config/config.go
}

# Every server's ports, the control port after each range included, as the
# comma-separated list net.ipv4.ip_local_reserved_ports takes.
#
# A single-node run starts each server just before its turn, after the
# clients of the ones before it have closed their connections. Those sit in
# TIME_WAIT for a minute on the client's side, on ephemeral ports that, with
# the ip_local_port_range of 1024 65535 this benchmark asks for, can be the
# very ports the next server binds; the client sockets carry no SO_REUSEADDR,
# so the bind fails with "address already in use". Reserved ports are never
# handed out as ephemeral ones, which keeps the servers' ports free.
bench_server_reserved_ports() {
    awk '
        /^var Ports = map/ { inports = 1; next }
        inports && /^}/ { exit }
        inports && NF >= 2 {
            v = $2; gsub(/[",]/, "", v)
            split(v, r, ":")
            print r[1], r[2] + 1
        }
    ' ./config/config.go | sort -n | awk '{ printf "%s%s-%s", (NR > 1 ? "," : ""), $1, $2 }'
}
