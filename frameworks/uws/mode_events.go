//go:build !stdio

package main

import "go-websocket-benchmark/config"

const frameworkName = config.UwsEvents
const maxBufferSize = 1024 * 16
