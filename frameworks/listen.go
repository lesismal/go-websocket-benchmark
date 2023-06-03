package frameworks

import (
	"flag"
	"net"

	"github.com/libp2p/go-reuseport"
)

var (
	reuse = flag.Bool("reuseport", false, `reuse port`)
)

func Listen(network, addr string) (net.Listener, error) {
	if *reuse {
		return reuseport.Listen(network, addr)
	}
	return net.Listen(network, addr)
}
