#!/bin/bash

Connections=(10000)
BodySize=(1024)
BenchTime=(2000000)
SleepTime=5

frameworks=(
    "fasthttp"
    "nettyws"
    "gobwas"
    "gorilla"
    "gws"
    "gws_std"
    "hertz"
    "nbio_std"
    "nbio_blocking"
    "nbio_mixed"
    "nbio_nonblocking"
    "nhooyr"
)
