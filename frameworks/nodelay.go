package frameworks

import (
	"net"
)

func SetNoDelay(c net.Conn, nodelay bool) {
	cc, ok := c.(interface{ SetNodelay(bool) error })
	if ok {
		cc.SetNodelay(nodelay)
	}
}
