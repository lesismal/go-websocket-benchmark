#!/bin/bash

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

rm -rf ./output

# run
for f in ${frameworks[@]}; do
    killall -9 "${f}.server" 1>/dev/null 2>&1
done
