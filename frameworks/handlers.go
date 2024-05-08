package frameworks

import (
	"encoding/json"
	"fmt"
	"go-websocket-benchmark/logging"
	"io"
	"net/http"
	"net/http/pprof"
	"os"
	"time"

	"github.com/lesismal/perf"
)

var psCounter *perf.PSCounter

func HandleCommon(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	var err error
	psCounter, err = perf.NewPSCounter(os.Getpid())
	if err != nil {
		logging.Fatalf("perf.NewPSCounter failed: %v", err)
	}

	mux.HandleFunc("/init", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			logging.Fatalf("perf.NewPSCounter failed: %v", err)
			return
		}
		var args InitArgs
		json.Unmarshal(body, &args)
		go func() {
			psCounter.Start(perf.PSCountOptions{
				CountCPU: true,
				CountMEM: true,
				CountIO:  true,
				CountNET: true,
				Interval: args.PsInterval,
			})
			time.Sleep(args.PsInterval)
		}()

		fmt.Fprintf(w, "%d", os.Getpid())
	})

	mux.HandleFunc("/ps", func(w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal(psCounter)
		w.Write(b)
	})
}

type InitArgs struct {
	PsInterval time.Duration
}
