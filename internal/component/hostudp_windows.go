// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"
	"net/netip"
	"strconv"
	"syscall"
)

// The OS calls a UDP socket makes beside Go's net on Windows (ADR 0140), hostudp_unix.go's over a socket Handle.

// The SO_BROADCAST option configure clears.
const (
	udpBroadcastLevel = syscall.SOL_SOCKET
	udpBroadcastName  = syscall.SO_BROADCAST
)

// udpSockopt is an option's level and name for a socket of family fam.
func udpSockopt(fam int, o udpOpt) (level, name int) {
	switch o {
	case udpOptHopLimit:
		if fam == familyIPv6 {
			return syscall.IPPROTO_IPV6, syscall.IPV6_UNICAST_HOPS
		}
		return syscall.IPPROTO_IP, syscall.IP_TTL
	case udpOptRecvBuffer:
		return syscall.SOL_SOCKET, syscall.SO_RCVBUF
	default:
		return syscall.SOL_SOCKET, syscall.SO_SNDBUF
	}
}

// osGetInt is getsockopt of an int option.
func osGetInt(rc syscall.RawConn, level, name int) (int, error) {
	var v int
	var serr error
	if err := rc.Control(func(fd uintptr) { v, serr = syscall.GetsockoptInt(syscall.Handle(fd), level, name) }); err != nil {
		return 0, err
	}
	return v, serr
}

// osSetInt is setsockopt of an int option.
func osSetInt(rc syscall.RawConn, level, name, v int) error {
	var serr error
	if err := rc.Control(func(fd uintptr) { serr = syscall.SetsockoptInt(syscall.Handle(fd), level, name, v) }); err != nil {
		return err
	}
	return serr
}

// osConnect is the OS's connect of the socket to ap.
func osConnect(rc syscall.RawConn, ap netip.AddrPort) error {
	sa := udpSockaddr(ap)
	var serr error
	if err := rc.Control(func(fd uintptr) { serr = syscall.Connect(syscall.Handle(fd), sa) }); err != nil {
		return err
	}
	return serr
}

// osDisconnect dissolves the socket's association: Winsock's connect to the all-zero address of the socket's family,
// which its documentation names as a disconnect for a datagram socket. Unmeasured: ADR 0140's slice used no Windows
// host, and Windows is the unsupported lane.
func osDisconnect(rc syscall.RawConn, fam int) (dissolved bool, err error) {
	var sa syscall.Sockaddr = &syscall.SockaddrInet4{}
	if fam == familyIPv6 {
		sa = &syscall.SockaddrInet6{}
	}
	var serr error
	if cerr := rc.Control(func(fd uintptr) { serr = syscall.Connect(syscall.Handle(fd), sa) }); cerr != nil {
		return false, cerr
	}
	return serr == nil, serr
}

// osLocalAddr is the socket's getsockname.
func osLocalAddr(rc syscall.RawConn) (netip.AddrPort, error) {
	var sa syscall.Sockaddr
	var serr error
	if err := rc.Control(func(fd uintptr) { sa, serr = syscall.Getsockname(syscall.Handle(fd)) }); err != nil {
		return netip.AddrPort{}, err
	}
	if serr != nil {
		return netip.AddrPort{}, serr
	}
	switch sa := sa.(type) {
	case *syscall.SockaddrInet4:
		return netip.AddrPortFrom(netip.AddrFrom4(sa.Addr), uint16(sa.Port)), nil
	case *syscall.SockaddrInet6:
		a := netip.AddrFrom16(sa.Addr)
		if sa.ZoneId != 0 {
			a = a.WithZone(strconv.FormatUint(uint64(sa.ZoneId), 10))
		}
		return netip.AddrPortFrom(a, uint16(sa.Port)), nil
	default:
		return netip.AddrPort{}, fmt.Errorf("component: udp-socket: getsockname gave a %T", sa)
	}
}

// udpSockaddr is ap as the syscall package's sockaddr, its zone the scope id.
func udpSockaddr(ap netip.AddrPort) syscall.Sockaddr {
	a := ap.Addr()
	if a.Is4() {
		return &syscall.SockaddrInet4{Port: int(ap.Port()), Addr: a.As4()}
	}
	zone, _ := strconv.ParseUint(a.Zone(), 10, 32) //nolint:errcheck // a named zone has no scope id here; 0
	return &syscall.SockaddrInet6{Port: int(ap.Port()), Addr: a.As16(), ZoneId: uint32(zone)}
}
