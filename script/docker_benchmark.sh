#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/.." && pwd)
cd "$repo_root"

usage() {
    cat <<'EOF'
Run the benchmark in a resource-limited Docker container.

Usage:
  bash script/docker_benchmark.sh [options] [benchmark client flags]

Options:
  --smoke       Run a short Gorilla-only validation.
  --rebuild     Rebuild the image without Docker's layer cache.
  -h, --help    Show this help.

Environment overrides:
  BENCH_CLIENT             benchcli-uwscpp (default) or benchcli-go
  BENCH_FRAMEWORKS         Comma-separated framework subset
  BENCH_TASKPOOL           Pool the servers run their callbacks on, and
  BENCH_TASKPOOL_MIN/_MAX/_QUEUE its sizing (see script/config.sh)
  BENCH_REPORT_SORT        Report row order: result (default, best first) or
                           framework (see script/config.sh)
  BENCH_UWS_WORKERS_PER_CPU uwebsockets' pool threads per CPU, and
  BENCH_UWS_LOOPS_PER_CPU  its event loops per CPU (see script/config.sh)
  DOCKER_BENCH_CPUS        Integer CPU count (default: about 75% available)
  DOCKER_BENCH_MEMORY      Docker memory value such as 8g (default: 80%)
  DOCKER_BENCH_IMAGE       Image tag (default: go-websocket-benchmark:local)
  DOCKER_BENCH_OUTPUT      Result directory (default: output/docker/<timestamp>)
  DOCKER_BENCH_SKIP_BUILD  Set to 1 to reuse an existing image
  DOCKER_BENCH_GO_IMAGE    Base image (default: golang:1.27-bookworm)
  DOCKER_BENCH_APT_MIRROR  Replaces http://deb.debian.org in the image's apt
                           sources, e.g. https://mirrors.aliyun.com
  DOCKER_BENCH_GOPROXY     GOPROXY for the image's go mod download
  DOCKER_BENCH_GITHUB_MIRROR Space-separated prefixes tried before
                           https://github.com/ for the image's native
                           dependencies, each download checked by SHA-256
  (script/docker_benchmark_cn.sh sets the last four to mainland China mirrors.)

Examples:
  bash script/docker_benchmark.sh --smoke
  BENCH_FRAMEWORKS=gorilla,nbio_nonblocking \
    bash script/docker_benchmark.sh -c=10000 -en=2000000 -b=1024 -rate=true
  DOCKER_BENCH_CPUS=8 DOCKER_BENCH_MEMORY=12g \
    bash script/docker_benchmark.sh
EOF
}

smoke=false
rebuild=false
while [ "$#" -gt 0 ]; do
    case "$1" in
        --smoke) smoke=true; shift ;;
        --rebuild) rebuild=true; shift ;;
        -h|--help) usage; exit 0 ;;
        --) shift; break ;;
        *) break ;;
    esac
done
benchmark_args=("$@")

. "$repo_root/script/config.sh" || exit 1

if [ "$BENCH_ROLE" != both ]; then
    echo "BENCH_ROLE=$BENCH_ROLE: this script runs one container with --network none," >&2
    echo "so it cannot be one node of a split run; use script/benchmark.sh on each node" >&2
    exit 1
fi
if ! command -v docker >/dev/null 2>&1; then
    echo "docker is required" >&2
    exit 1
fi
if ! docker_info=$(docker info --format '{{.NCPU}} {{.MemTotal}}' 2>/dev/null); then
    echo "cannot connect to the Docker daemon; start Docker and check socket permissions" >&2
    exit 1
fi
read -r daemon_cpu_count daemon_memory_bytes <<< "$docker_info"
if ! [[ "$daemon_cpu_count" =~ ^[0-9]+$ && "$daemon_memory_bytes" =~ ^[0-9]+$ ]]; then
    echo "unexpected Docker hardware information: $docker_info" >&2
    exit 1
fi

image=${DOCKER_BENCH_IMAGE:-go-websocket-benchmark:local}
build_args=(-f script/Dockerfile.benchmark -t "$image")
if [ "$rebuild" = true ]; then
    build_args=(--no-cache "${build_args[@]}")
fi
# Only the ones given, so an unset one keeps the Dockerfile's default.
if [ -n "${DOCKER_BENCH_GO_IMAGE:-}" ]; then build_args+=(--build-arg "GO_IMAGE=$DOCKER_BENCH_GO_IMAGE"); fi
if [ -n "${DOCKER_BENCH_APT_MIRROR:-}" ]; then build_args+=(--build-arg "APT_MIRROR=$DOCKER_BENCH_APT_MIRROR"); fi
if [ -n "${DOCKER_BENCH_GOPROXY:-}" ]; then build_args+=(--build-arg "GO_PROXY=$DOCKER_BENCH_GOPROXY"); fi
if [ -n "${DOCKER_BENCH_GITHUB_MIRROR:-}" ]; then build_args+=(--build-arg "GITHUB_MIRROR=$DOCKER_BENCH_GITHUB_MIRROR"); fi
if [ "${DOCKER_BENCH_SKIP_BUILD:-0}" != 1 ]; then
    echo "Building Docker benchmark image: $image"
    docker build "${build_args[@]}" .
elif ! docker image inspect "$image" >/dev/null 2>&1; then
    echo "DOCKER_BENCH_SKIP_BUILD=1, but image does not exist: $image" >&2
    exit 1
fi

# Docker Desktop may expose fewer CPUs than the physical host. Query the
# daemon's effective cpuset and select from those IDs rather than assuming 0-N.
allowed_cpu_spec=$(docker run --rm "$image" awk '/Cpus_allowed_list/ {print $2}' /proc/self/status)
expanded_cpus=()
IFS=',' read -r -a cpu_parts <<< "$allowed_cpu_spec"
for cpu_part in "${cpu_parts[@]}"; do
    if [[ "$cpu_part" =~ ^([0-9]+)-([0-9]+)$ ]]; then
        cpu_start=${BASH_REMATCH[1]}
        cpu_end=${BASH_REMATCH[2]}
        for ((cpu_id=cpu_start; cpu_id<=cpu_end; cpu_id++)); do
            expanded_cpus+=("$cpu_id")
        done
    elif [[ "$cpu_part" =~ ^[0-9]+$ ]]; then
        expanded_cpus+=("$cpu_part")
    else
        echo "unexpected Docker CPU list: $allowed_cpu_spec" >&2
        exit 1
    fi
done
available_cpus=${#expanded_cpus[@]}
if [ "$daemon_cpu_count" -lt "$available_cpus" ]; then
    available_cpus=$daemon_cpu_count
fi
if [ "$available_cpus" -lt 2 ]; then
    echo "Docker needs at least 2 CPUs for separate server/client affinity" >&2
    exit 1
fi

if [ -n "${DOCKER_BENCH_CPUS:-}" ]; then
    if ! [[ "$DOCKER_BENCH_CPUS" =~ ^[0-9]+$ ]]; then
        echo "DOCKER_BENCH_CPUS must be an integer" >&2
        exit 1
    fi
    allocated_cpus=$DOCKER_BENCH_CPUS
else
    allocated_cpus=$((available_cpus * 3 / 4))
    if [ "$allocated_cpus" -lt 2 ]; then allocated_cpus=2; fi
    if [ "$available_cpus" -gt 2 ] && [ "$allocated_cpus" -ge "$available_cpus" ]; then
        allocated_cpus=$((available_cpus - 1))
    fi
fi
if [ "$allocated_cpus" -lt 2 ] || [ "$allocated_cpus" -gt "$available_cpus" ]; then
    echo "requested $allocated_cpus CPUs, but Docker exposes $available_cpus" >&2
    exit 1
fi

selected_cpus=("${expanded_cpus[@]:0:allocated_cpus}")
server_cpu_count=$((allocated_cpus / 2))
server_cpus=("${selected_cpus[@]:0:server_cpu_count}")
client_cpus=("${selected_cpus[@]:server_cpu_count}")
join_cpus() {
    local joined=""
    local cpu
    for cpu in "$@"; do
        joined="${joined}${joined:+,}${cpu}"
    done
    printf '%s' "$joined"
}
selected_cpu_list=$(join_cpus "${selected_cpus[@]}")
server_cpu_list=$(join_cpus "${server_cpus[@]}")
client_cpu_list=$(join_cpus "${client_cpus[@]}")

if [ -n "${DOCKER_BENCH_MEMORY:-}" ]; then
    memory_limit=$DOCKER_BENCH_MEMORY
    memory_description=$DOCKER_BENCH_MEMORY
else
    memory_limit=$((daemon_memory_bytes * 80 / 100))
    if [ "$memory_limit" -lt 805306368 ]; then
        echo "Docker exposes too little memory for this benchmark: ${daemon_memory_bytes} bytes" >&2
        exit 1
    fi
    memory_description=$(awk -v bytes="$memory_limit" 'BEGIN {printf "%.2f GiB", bytes/1024/1024/1024}')
fi

run_frameworks=${BENCH_FRAMEWORKS:-}
if [ "$smoke" = true ]; then
    run_frameworks=gorilla
    smoke_args=(-c=100 -dc=20 -ec=50 -en=2000 -b=512 -rate=true -rc=10 -rd=1 -rr=20 -ep=false -rp=false)
    if [ "${#benchmark_args[@]}" -gt 0 ]; then
        benchmark_args=("${smoke_args[@]}" "${benchmark_args[@]}")
    else
        benchmark_args=("${smoke_args[@]}")
    fi
fi
# benchmark.sh picks the servers' flags out of these itself now, so there is
# nothing to reorder: it used to hand its first argument to the servers
# whatever it was, and a -nodelay had to be put there.
bench_client=$BENCH_CLIENT

timestamp=$(date +%Y%m%d-%H%M%S)
result_dir=${DOCKER_BENCH_OUTPUT:-"$repo_root/output/docker/$timestamp"}
mkdir -p "$result_dir"
container="go-websocket-benchmark-${timestamp}-$$"
cleanup_container() {
    docker rm -f "$container" >/dev/null 2>&1 || true
}
trap cleanup_container EXIT INT TERM

run_args=(
    --name "$container"
    --init
    --network none
    --cpus "$allocated_cpus"
    --cpuset-cpus "$selected_cpu_list"
    --memory "$memory_limit"
    --memory-swap "$memory_limit"
    --pids-limit 32768
    --ulimit nofile=1048576:1048576
    --sysctl "net.ipv4.ip_local_port_range=1024 65535"
    --sysctl net.ipv4.tcp_tw_reuse=1
    --env "BENCH_CLIENT=$bench_client"
    --env "BENCH_SERVER_CPU_LIST=$server_cpu_list"
    --env "BENCH_CLIENT_CPU_LIST=$client_cpu_list"
    # Without these the container would run the taskpool defaults however the
    # caller set them out here.
    --env "BENCH_TASKPOOL=$BENCH_TASKPOOL"
    --env "BENCH_TASKPOOL_MIN=$BENCH_TASKPOOL_MIN"
    --env "BENCH_TASKPOOL_MAX=$BENCH_TASKPOOL_MAX"
    --env "BENCH_TASKPOOL_QUEUE=$BENCH_TASKPOOL_QUEUE"
    # Likewise the report row order, which the run writes its tables in.
    --env "BENCH_REPORT_SORT=$BENCH_REPORT_SORT"
    # And the uwebsockets thread multipliers, which size themselves against the
    # container's CPUs rather than the host's.
    --env "BENCH_UWS_WORKERS_PER_CPU=$BENCH_UWS_WORKERS_PER_CPU"
    --env "BENCH_UWS_LOOPS_PER_CPU=$BENCH_UWS_LOOPS_PER_CPU"
)
if [ -n "$run_frameworks" ]; then
    run_args+=(--env "BENCH_FRAMEWORKS=$run_frameworks")
fi

# The machine the numbers were measured on, for resources.txt. Every probe
# falls back to "unknown" rather than stopping the run over a missing tool.
host_os="unknown"
host_cpu_model="unknown"
host_cpu_sockets="unknown"
host_cpu_cores="unknown"
host_cpu_threads="unknown"
case "$(uname -s)" in
    Darwin)
        host_os="$(sw_vers -productName 2>/dev/null || echo macOS) $(sw_vers -productVersion 2>/dev/null || true)"
        host_cpu_model=$(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo unknown)
        host_cpu_sockets=$(sysctl -n hw.packages 2>/dev/null || echo unknown)
        host_cpu_cores=$(sysctl -n hw.physicalcpu 2>/dev/null || echo unknown)
        host_cpu_threads=$(sysctl -n hw.logicalcpu 2>/dev/null || echo unknown)
        ;;
    Linux)
        if [ -r /etc/os-release ]; then
            host_os=$(. /etc/os-release && printf '%s' "${PRETTY_NAME:-${NAME:-Linux}}")
        else
            host_os=Linux
        fi
        host_cpu_model=$(awk -F': *' '/^model name/ {print $2; exit}' /proc/cpuinfo 2>/dev/null || true)
        if command -v lscpu >/dev/null 2>&1; then
            lscpu_value() { LC_ALL=C lscpu 2>/dev/null | awk -F': *' -v k="$1" '$1 == k {print $2; exit}' || true; }
            # ARM kernels often have no model name, and lscpu prints "-" for it.
            [ -n "$host_cpu_model" ] || host_cpu_model=$(lscpu_value "Model name")
            if [ -z "$host_cpu_model" ] || [ "$host_cpu_model" = "-" ]; then
                host_cpu_model=$(lscpu_value "Vendor ID")
            fi
            # The parsable listing has a socket and a core for every CPU on x86
            # and ARM alike, where the "Socket(s)" summary line does not.
            cpu_topology=$(LC_ALL=C lscpu -p=SOCKET,CORE 2>/dev/null | grep -v '^#' || true)
            if [ -n "$cpu_topology" ]; then
                host_cpu_sockets=$(printf '%s\n' "$cpu_topology" | cut -d, -f1 | sort -u | wc -l | tr -d ' ')
                host_cpu_cores=$(printf '%s\n' "$cpu_topology" | sort -u | wc -l | tr -d ' ')
            fi
        fi
        if [ -z "$host_cpu_model" ] || [ "$host_cpu_model" = "-" ]; then
            host_cpu_model=unknown
        fi
        host_cpu_threads=$(getconf _NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || echo unknown)
        ;;
esac
host_kernel=$(uname -srm 2>/dev/null || echo unknown)
docker_os=$(docker info --format '{{.OperatingSystem}}, kernel {{.KernelVersion}}, {{.Architecture}}' 2>/dev/null || echo unknown)

cat > "$result_dir/resources.txt" <<EOF
Host OS: $host_os ($host_kernel)
Host CPU model: $host_cpu_model
Host CPU sockets: $host_cpu_sockets
Host CPU physical cores: $host_cpu_cores
Host CPU logical CPUs: $host_cpu_threads
Docker OS: $docker_os
Docker CPUs total: $daemon_cpu_count
Docker CPUs available: $available_cpus ($allowed_cpu_spec)
Docker CPUs allocated: $allocated_cpus ($selected_cpu_list)
Server CPUs: $server_cpu_list
Client CPUs: $client_cpu_list
Docker memory available: $daemon_memory_bytes bytes
Container memory limit: $memory_description
Benchmark client: $bench_client
Frameworks: ${run_frameworks:-all}
EOF
cat "$result_dir/resources.txt"
echo "Results: $result_dir"

set +e
if [ "${#benchmark_args[@]}" -gt 0 ]; then
    docker run "${run_args[@]}" "$image" \
        bash script/benchmark.sh "${benchmark_args[@]}" 2>&1 | tee "$result_dir/console.log"
else
    docker run "${run_args[@]}" "$image" \
        bash script/benchmark.sh 2>&1 | tee "$result_dir/console.log"
fi
benchmark_status=${PIPESTATUS[0]}
set -e

mkdir -p "$result_dir/report" "$result_dir/log"
docker cp "$container:/workspace/output/report/." "$result_dir/report" >/dev/null 2>&1 || true
docker cp "$container:/workspace/output/log/." "$result_dir/log" >/dev/null 2>&1 || true

if [ "$benchmark_status" -eq 0 ]; then
    connection_reports=("$result_dir"/report/*-Connections*.json)
    if [ ! -e "${connection_reports[0]}" ]; then
        echo "Docker benchmark produced no connection report" >&2
        benchmark_status=1
    else
        for connection_report in "${connection_reports[@]}"; do
            echo_report=${connection_report/-Connections/-BenchEcho}
            if ! grep -Eq '"Success":[1-9][0-9]*' "$connection_report" \
                || ! grep -Eq '"Failed":0([,}])' "$connection_report"; then
                echo "Unsuccessful connection report: $connection_report" >&2
                benchmark_status=1
            fi
            if [ ! -f "$echo_report" ] \
                || ! grep -Eq '"Success":[1-9][0-9]*' "$echo_report" \
                || ! grep -Eq '"Failed":0([,}])' "$echo_report"; then
                echo "Unsuccessful or missing Echo report: $echo_report" >&2
                benchmark_status=1
            fi
        done
    fi
fi

if [ "$benchmark_status" -ne 0 ]; then
    echo "Docker benchmark failed with status $benchmark_status; partial output is in $result_dir" >&2
    exit "$benchmark_status"
fi
echo "Docker benchmark completed: $result_dir"
