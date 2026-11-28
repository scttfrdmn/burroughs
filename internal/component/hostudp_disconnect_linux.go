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
func osDisconnect(rc syscall.RawConn, _ int) error {
	var sa syscall.RawSockaddrInet6 // large enough for either family; only its family is read
	sa.Family = syscall.AF_UNSPEC
	var serr error
	if err := rc.Control(func(fd uintptr) {
		if _, _, e := syscall.RawSyscall(syscall.SYS_CONNECT, fd, uintptr(unsafe.Pointer(&sa)), uintptr(syscall.SizeofSockaddrInet4)); e != 0 {
			serr = e
		}
	}); err != nil {
		return err
	}
	return serr
}
