#!/bin/bash

Connections=(1000 10000)
BodySize=(128 1024)
BenchTime=(1000000)

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
