package frameworks

import (
	"flag"
)

var (
	_ = flag.Int("rr", 100, "benchpipeline: how many request message can be sent to 1 conn every second")
)
