frameworks=(
    "gobwas"
    "gorilla"
    "gws"
    "gws_basedon_stdhttp"
    "nbio_basedon_stdhttp"
    "nbio_mod_blocking"
    "nbio_mod_mixed"
    "nbio_mod_nonblocking"
    "nhooyr"
)

clean() {
    rm -rf ./output
    for f in ${frameworks[@]}; do
        killall -9 "${f}.server" 1>/dev/null 2>&1
    done
}

total_cpu_num=$(getconf _NPROCESSORS_ONLN)
want_cpu_num=$((total_cpu_num >= 16 ? 7 : total_cpu_num / 2 - 1))
limit_cpu="taskset -c 0-$want_cpu_num"

line="--------------------------------"
