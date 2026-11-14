// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import "syscall"

// platformErrnoLabels are Windows' socket errors, which arrive as their WSA numbers rather than as the POSIX
// errnos Go's syscall package invents for Windows, so the POSIX table alone would map every one to `other`
// (ADR 0123).
var platformErrnoLabels = map[syscall.Errno]string{
	10013: "access-denied",      // WSAEACCES
	10048: "address-in-use",     // WSAEADDRINUSE
	10049: "address-in-use",     // WSAEADDRNOTAVAIL
	10051: "remote-unreachable", // WSAENETUNREACH
	10053: "connection-aborted", // WSAECONNABORTED
	10054: "connection-reset",   // WSAECONNRESET
	10060: "timeout",            // WSAETIMEDOUT
	10061: "connection-refused", // WSAECONNREFUSED
	10065: "remote-unreachable", // WSAEHOSTUNREACH
}
