// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build linux && !386

package component

import (
	"syscall"
	"unsafe"
)

// osDisconnect dissolves the socket's association (ADR 0140): a connect to an AF_UNSPEC address, POSIX's disconnect
// of a datagram socket. The syscall package has no AF_UNSPEC sockaddr, so the connect is a raw SYS_CONNECT of one:
// with hostudp_disconnect_darwin.go's, the package's only use of unsafe. linux/386 has no SYS_CONNECT (its socket
// calls go through socketcall), so it takes hostudp_disconnect_unix.go's answer.
//
// **Linux's disconnect also unbinds a port the kernel chose**, when the bind asked for port 0, and leaves the socket
// at port 0, where nothing sent to its old port reaches it. An explicitly chosen port is kept, and darwin keeps
// either. Measured with a native Go probe in a Linux container, independently of this engine, before this was written:
// bound to 127.0.0.1:0 and to 0.0.0.0:0, getsockname after the disconnect reads port 0 and a datagram to the old port
// is lost; bound to 127.0.0.1:47123 the port is kept; and a bind to the old port after the disconnect succeeds and
// receives. So when the disconnect has left the socket portless, it is bound again to the address the kernel left and
// the port it had: a disconnected socket stays bound, as the WIT's bound state and darwin's kernel have it. A port
// another socket took in the window between is the bind's error, with the association already dissolved.
func osDisconnect(rc syscall.RawConn, _ int) (dissolved bool, err error) {
	var serr error
	if cerr := rc.Control(func(fd uintptr) {
		before, gerr := syscall.Getsockname(int(fd))
		if gerr != nil {
			serr = gerr
			return
		}
		var sa syscall.RawSockaddrInet6 // large enough for either family; only its family is read
		sa.Family = syscall.AF_UNSPEC
		if _, _, e := syscall.RawSyscall(syscall.SYS_CONNECT, fd, uintptr(unsafe.Pointer(&sa)), uintptr(syscall.SizeofSockaddrInet4)); e != 0 {
			serr = e
			return
		}
		dissolved = true
		after, gerr := syscall.Getsockname(int(fd))
		if gerr != nil {
			serr = gerr
			return
		}
		// NEUTER, run in a Linux container: skip the rebind, and TestUDPMatchesWasmtime's reconnect reads 0x01000000 (C
		// receives A's datagram from a port A was never bound to), disconnect-receive waits until the hang guard, as CI's
		// first Linux run of both did, and disconnect-any reads 0x00010000 (the bound port lost), each in both gate states.
		// The disconnect row passes. **Vacuous on darwin**, which does not build this file, and whose kernel keeps the port.
		if rebind := udpRebindTo(before, after); rebind != nil {
			serr = syscall.Bind(int(fd), rebind)
		}
	}); cerr != nil {
		return false, cerr
	}
	return dissolved, serr
}

// udpRebindTo is the sockaddr to bind again after a disconnect that left the socket portless: the address the kernel
// left, with the port the socket had. Nil when the disconnect kept the port.
func udpRebindTo(before, after syscall.Sockaddr) syscall.Sockaddr {
	switch b := before.(type) {
	case *syscall.SockaddrInet4:
		if a, ok := after.(*syscall.SockaddrInet4); ok && a.Port == 0 && b.Port != 0 {
			return &syscall.SockaddrInet4{Port: b.Port, Addr: a.Addr}
		}
	case *syscall.SockaddrInet6:
		if a, ok := after.(*syscall.SockaddrInet6); ok && a.Port == 0 && b.Port != 0 {
			return &syscall.SockaddrInet6{Port: b.Port, Addr: a.Addr, ZoneId: a.ZoneId}
		}
	}
	return nil
}
