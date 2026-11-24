//go:build !unix

package recorderclient

import "net"

func receiveBuffer(*net.UDPConn) int { return -1 }
