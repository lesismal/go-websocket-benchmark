package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"
	"os"
	"os/signal"
	"time"

	"github.com/soypat/peasocket"
)

var (
	readBufferSize = flag.Int("b", 1024, `read buffer size`)
	_              = flag.Int("mrb", 4096, `max read buffer size`)
	_              = flag.Int64("m", 1024*1024*1024*2, `memory limit`)
	_              = flag.Int("mb", 10000, `max blocking online num, e.g. 10000`)
)

func main() {
	addrs, err := config.GetFrameworkServerAddrs(config.Nhooyr)
	if err != nil {
		logging.Fatalf("GetFrameworkBenchmarkAddrs(%v) failed: %v", config.Nhooyr, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	startServers(ctx, addrs)
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	cancel()
}

func startServers(ctx context.Context, addrs []string) {
	for i := 0; i < len(addrs); i++ {
		go peasocket.ListenAndServe(ctx, addrs[i], onWebsocket)
	}
}

func onWebsocket(ctx context.Context, sv *peasocket.Server) {
	size := *readBufferSize
	rawBuf := make([]byte, 2*size)
	backoff := peasocket.ExponentialBackoff{
		MaxWait: 30 * time.Millisecond,
	}

	buf := bytes.NewBuffer(rawBuf[:size])
	scratchBuf := rawBuf[size:]
	defer sv.CloseConn(errors.New("sky is falling"))
	for {
		select {
		case <-ctx.Done():
			return
		default:
			_, n, err := sv.WriteNextMessageTo(buf)
			if err != nil {
				if n != 0 {
					return // bad read.
				}
				backoff.Miss()
				continue
			}
			backoff.Hit()

			_, err = sv.WriteFragmentedMessage(buf, scratchBuf)
			if err != nil {
				return
			}
		}
	}
}
