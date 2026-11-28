// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

//go:build unix && !darwin && !(linux && !386)

package component

import (
	"errors"
	"syscall"
)

// osDisconnect is `not-supported` on a POSIX system this engine has no raw AF_UNSPEC connect for (ADR 0140): the BSDs
// but darwin, linux/386, and the rest. udpErrorLabel maps errors.ErrUnsupported to it, and the association stands.
func osDisconnect(syscall.RawConn, int) (dissolved bool, err error) {
	return false, errors.ErrUnsupported
}
