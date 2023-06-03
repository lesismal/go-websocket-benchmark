#!/bin/bash

Connections=(5000 10000)
BodySize=(1024)
BenchTime=(1000000)
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
