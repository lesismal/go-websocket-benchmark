package frameworks

import (
	"net"
	"syscall"

	"go-websocket-benchmark/logging"
)

// SetNoDelay sets TCP_NODELAY on c to nodelay, whatever the framework
// wrapped the socket in: a SetNoDelay method, a syscall.Conn or an Fd method.
// A conn that offers none of them is fatal rather than skipped. Skipping is
// how -nodelay=false used to leave some servers at TCP_NODELAY=1 while the
// rest ran with Nagle on, and a framework whose conn cannot be reached this
// way has to unwrap it to its socket itself.
//
// A failure to set it is ignored: it happens only once the peer has gone.
func SetNoDelay(c net.Conn, nodelay bool) {
	switch cc := c.(type) {
	case interface{ SetNoDelay(bool) error }:
		_ = cc.SetNoDelay(nodelay)
	case syscall.Conn:
		rc, err := cc.SyscallConn()
		if err != nil {
			return
		}
		_ = rc.Control(func(fd uintptr) { _ = SetNoDelayFD(int(fd), nodelay) })
	case interface{ Fd() int }:
		_ = SetNoDelayFD(cc.Fd(), nodelay)
	default:
		logging.Fatalf("SetNoDelay: %T exposes no way to set TCP_NODELAY", c)
	}
}

// SetNoDelayFD sets TCP_NODELAY on the socket fd to nodelay.
func SetNoDelayFD(fd int, nodelay bool) error {
	v := 0
	if nodelay {
		v = 1
	}
	return syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_NODELAY, v)
}
