#!/bin/bash

Connections=(5000 50000)
BodySize=(512 1024)
BenchTime=(2000000)
SleepTime=5

frameworks=(
    "fasthttp"
    "gobwas"
    "gorilla"
    "gws"
    "gws_std"
    "hertz"
    "hertz_std"
    "nbio_std"
    "nbio_blocking"
    "nbio_mixed"
    "nbio_nonblocking"
    "nettyws"
    "nhooyr"
)
