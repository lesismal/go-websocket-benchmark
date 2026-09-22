package frameworks

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/pprof"
	"os"
	"sync/atomic"
	"time"

	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"
	"go-websocket-benchmark/taskpool"

	"github.com/lesismal/perf"
)

var (
	psCounter *perf.PSCounter
	psStarted atomic.Bool
)

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
		var args config.InitArgs
		json.Unmarshal(body, &args)
		// Once, however many times /init arrives: a client that retried the
		// request because its own read failed can deliver it twice, and a
		// second Start would reset the sample slices under the goroutines
		// already appending to them. The uwebsockets server guards its
		// sampler the same way.
		if psStarted.CompareAndSwap(false, true) {
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
		} else {
			logging.Printf("/init called again; the ps counter is already running")
		}

		fmt.Fprintf(w, "%d", os.Getpid())
	})

	mux.HandleFunc("/ps", func(w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal(psCounter)
		w.Write(b)
	})

	// The pool this server installed, for the Pool column of the reports. It
	// is empty in the frameworks that take no -taskpool flag, which the
	// clients show as "-": see config.GetFrameworkTaskPool.
	mux.HandleFunc("/taskpool", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, taskpool.Installed())
	})
}
