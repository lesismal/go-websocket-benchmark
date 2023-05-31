#!/bin/bash

Connections=(1000 2000)
BodySize=(128 512)
BenchTime=(500000)
SleepTime=10

frameworks=(
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
