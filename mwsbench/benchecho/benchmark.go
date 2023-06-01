package benchmark

import (
	"go-websocket-benchmark/config"

	"github.com/lesismal/nbio/nbhttp/websocket"
)

type Benchmark struct {
}

func onMessage(c *websocket.Conn, mt websocket.MessageType, b []byte) {
	ch, _ := c.Session().(chan config.EchoSession)
	ch <- config.EchoSession{
		MT:    mt,
		Bytes: b,
	}
}
