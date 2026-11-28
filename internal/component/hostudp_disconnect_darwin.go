// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"syscall"
	"unsafe"
)

// osDisconnect dissolves the socket's association (ADR 0140): a connect to an AF_UNSPEC address, POSIX's disconnect
// of a datagram socket, as a raw SYS_CONNECT because the syscall package has no AF_UNSPEC sockaddr.
//
// **darwin dissolves the association and then refuses the address** (connect(2): "Datagram sockets may dissolve the
// association by connecting to an invalid address ... the error EAFNOSUPPORT will be harmlessly returned"). Measured
// natively before this was written, with Go's net: the AF_UNSPEC connect here answers EAFNOSUPPORT for both families,
// a connect to a sockaddr of the other family answers EINVAL, and after either getpeername is ENOTCONN. So both errnos
// are read as success.
func osDisconnect(rc syscall.RawConn, fam int) error {
	var sa syscall.RawSockaddrInet6 // large enough for either family; only its length and family are read
	size := uintptr(syscall.SizeofSockaddrInet4)
	if fam == familyIPv6 {
		size = syscall.SizeofSockaddrInet6
	}
	sa.Len, sa.Family = uint8(size), syscall.AF_UNSPEC
	var serr error
	if err := rc.Control(func(fd uintptr) {
		if _, _, e := syscall.RawSyscall(syscall.SYS_CONNECT, fd, uintptr(unsafe.Pointer(&sa)), size); e != 0 {
			serr = e
		}
	}); err != nil {
		return err
	}
	if errors.Is(serr, syscall.EAFNOSUPPORT) || errors.Is(serr, syscall.EINVAL) {
		return nil
	}
	return serr
}
