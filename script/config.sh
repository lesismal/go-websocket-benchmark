#!/bin/bash

Connections=(5000 10000)
BodySize=(512 1024)
BenchTime=(2000000)
SleepTime=10

frameworks=(
    "fasthttp_ws"
    "gobwas"
    "gorilla"
    "gws"
    "gws_basedon_stdhttp"
    "hertz"
    "nbio_basedon_stdhttp"
    "nbio_mod_blocking"
    "nbio_mod_mixed"
    "nbio_mod_nonblocking"
    "nhooyr"
)
