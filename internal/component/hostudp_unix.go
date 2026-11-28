// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build unix

package component

import (
	"fmt"
	"net/netip"
	"strconv"
	"syscall"
)

// The OS calls a UDP socket makes beside Go's net (ADR 0140), each on the socket's fd inside RawConn.Control, so a
// close meanwhile cannot free the fd and let another socket take its number.

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
	if err := rc.Control(func(fd uintptr) { v, serr = syscall.GetsockoptInt(int(fd), level, name) }); err != nil {
		return 0, err
	}
	return v, serr
}

// osSetInt is setsockopt of an int option.
func osSetInt(rc syscall.RawConn, level, name, v int) error {
	var serr error
	if err := rc.Control(func(fd uintptr) { serr = syscall.SetsockoptInt(int(fd), level, name, v) }); err != nil {
		return err
	}
	return serr
}

// osConnect is the OS's connect of the socket to ap.
func osConnect(rc syscall.RawConn, ap netip.AddrPort) error {
	sa := udpSockaddr(ap)
	var serr error
	if err := rc.Control(func(fd uintptr) { serr = syscall.Connect(int(fd), sa) }); err != nil {
		return err
	}
	return serr
}

// osLocalAddr is the socket's getsockname.
func osLocalAddr(rc syscall.RawConn) (netip.AddrPort, error) {
	var sa syscall.Sockaddr
	var serr error
	if err := rc.Control(func(fd uintptr) { sa, serr = syscall.Getsockname(int(fd)) }); err != nil {
		return netip.AddrPort{}, err
	}
	if serr != nil {
		return netip.AddrPort{}, serr
	}
	return udpAddrPortOf(sa)
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

// udpAddrPortOf is a sockaddr as an address, the scope id its zone.
//
// NEUTER, run: answer every ipv6 local address as ::, and TestUDPMatchesWasmtime's bind-v6 row reads 0x00010000 (the
// address not ::1) in both gate states, while sendto-v6, dial-v6 and max-v6 wait until the hang guard, each sending to
// the :: its peer reported, where nothing receives; TestP3UdpMatchesWasmtime's granted runs wait at the v6 recv_from.
// Every ipv4 row passes. The first run of this neuter found the harness closing a row's instance only on its success,
// so a row that failed its guard left its reader to fail every later row's goroutine check; it closes on every exit now.
func udpAddrPortOf(sa syscall.Sockaddr) (netip.AddrPort, error) {
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
