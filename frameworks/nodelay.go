package frameworks

import (
	"net"
)

func SetNoDelay(c net.Conn, nodelay bool) {
	cc, ok := c.(interface{ SetNoDelay(bool) error })
	if ok {
		cc.SetNoDelay(nodelay)
	}
}
