package frameworks

import (
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
)

func HandleCommon(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	mux.HandleFunc("/pid", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%d", os.Getpid())
	})
}
