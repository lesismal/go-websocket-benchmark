package frameworks

import (
	"go-websocket-benchmark/config"
	"go-websocket-benchmark/logging"
	"go-websocket-benchmark/taskpool"
)

// Name is the framework a server built for framework is running as, which
// picks the ports it listens on: framework itself, or its inline entry in
// config.Inlines when -taskpool=inline. The inline entry is the same binary
// under another name, so the pool it was started with is the one thing that
// tells the two apart; a framework with no inline entry cannot be run inline.
// Call it after flag.Parse.
func Name(framework string) string {
	if taskpool.FlagConfig().Name != taskpool.Inline {
		return framework
	}
	inline, ok := config.Inlines[framework]
	if !ok {
		logging.Fatalf("%v has no inline entry in config.Inlines, so it cannot run -taskpool=%v", framework, taskpool.Inline)
	}
	return inline
}
