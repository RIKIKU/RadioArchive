//go:build unix

package recorderclient

import (
	"net"
	"syscall"
)

// receiveBuffer is the UDP receive buffer the kernel actually gave (Linux
// reports twice the usable size), or -1.
func receiveBuffer(c *net.UDPConn) int {
	rc, err := c.SyscallConn()
	if err != nil {
		return -1
	}
	n := -1
	_ = rc.Control(func(fd uintptr) {
		if v, err := syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF); err == nil {
			n = v
		}
	})
	return n
}
