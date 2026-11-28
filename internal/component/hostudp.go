// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"syscall"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// WASI 0.3's UDP (#1376's E3, ADR 0140): `wasi:sockets/types@0.3`'s `udp-socket` as a host resource, every one of its
// fifteen functions. ADR 0123 is the design authority for wasi:sockets; ADR 0140 records what it does not decide.
//
// # One OS socket, made at the bind
//
// A `create` mints an unbound socket with no OS socket. The OS socket is made by the bind, explicit or implicit (the
// first `connect` or `send`), with `net.ListenUDP`. An option set before then is recorded and applied when it is
// made, and a get before then is read from a probe socket made for the call (`probeLocked`). The handle owns the OS
// socket: UDP has no derived streams to share it with. `connect` is the OS's connect on that socket, and `disconnect`
// is a connect to `AF_UNSPEC` (hostudp_disconnect_*.go).
//
// # send inline, receive through one reader
//
// send resolves inline, on the guest's goroutine. receive is served by one reader per socket (`(*udpSocket).read`),
// started on demand and counted in the instance's registry: it serves the pending receives in order, and a datagram it
// reads while none is waiting is held for the next. One claim decides who resolves a pending receive: the reader's
// delivery, the guest's cancel (which resolves at the cancel), or Close.
//
// # Locks
//
// The order is ADR 0125's with the UDP socket beside the TCP one: the run slot → `asyncHandles.mu` →
// { `hostResources.mu`, a leaf; `tcpSocket.mu` → `hostNet.mu`; `udpSocket.mu` → `hostNet.mu` }. No resolution is
// applied with a socket's lock held, and `sockPending` is raised before the socket's lock is taken.

// udpSocketResource is `wasi:sockets/types@0.3::udp-socket`. Its destructor closes the OS socket, which ends a read in
// flight.
var udpSocketResource = newHostResource("wasi:sockets/types::udp-socket", destroyUDPSocket)

// udpMaxDatagram is the most bytes a datagram may carry: a send of more is `datagram-too-large` before the kernel is
// asked (wasmtime 49.0.3's reading, udp.reading's sizes-64k), and the reader reads into a buffer this size.
const udpMaxDatagram = 65535

// udpSocket is the object behind a `udp-socket` handle. Its fields are guarded by mu, which is taken before the
// registry's lock and never with a lock other than `asyncHandles.mu` held outside it.
type udpSocket struct {
	net    *hostNet
	family int
	mu     sync.Mutex
	// conn is the OS socket, nil until the bind makes it; rc is its raw connection, for the options and the OS's
	// connect. closed is its destructor's close or Close's.
	conn   *net.UDPConn
	rc     syscall.RawConn
	closed bool
	// connected and remote are the OS's connect: the peer the kernel filters for, and send's default destination.
	connected bool
	remote    netip.AddrPort
	// opts are the options set before the bind, applied when the bind makes the OS socket.
	opts udpOptions
	// pending are the receives waiting for a datagram, in order; held is what the reader read while none was waiting,
	// at most one; reading is the reader running. buf is the reader's buffer.
	pending []*udpRecv
	held    []udpDatagram
	reading bool
	buf     []byte
}

// udpOptions are the three options a socket can set before its bind: each recorded once set.
type udpOptions struct {
	hopSet, rbufSet, sbufSet bool
	hop                      uint8
	rbuf, sbuf               int
}

// udpOpt names one of the three options, which the platform files map to a level and a name (udpSockopt).
type udpOpt int

const (
	udpOptHopLimit udpOpt = iota
	udpOptRecvBuffer
	udpOptSendBuffer
)

// udpDatagram is one read: its bytes and sender, or its error.
type udpDatagram struct {
	data []byte
	from netip.AddrPort
	err  error
}

// udpRecv is one pending receive: the lend of its socket, its resolution, and the table whose sockPending it counts in.
type udpRecv struct {
	h       *asyncHandles
	release func()
	resolve func(canon.Value)
}

// destroyUDPSocket is the handle's destructor: the OS socket is closed and leaves the registry. A socket with a
// receive pending cannot be dropped, since the receive holds a lend of it, so no receive is left waiting.
func destroyUDPSocket(obj any) error {
	s, ok := obj.(*udpSocket)
	if !ok {
		return fmt.Errorf("component: a udp-socket handle held a %T", obj)
	}
	s.mu.Lock()
	c, already := s.conn, s.closed
	s.closed, s.held = true, nil
	s.mu.Unlock()
	if c != nil {
		s.net.forgetUDP(s)
		if !already {
			_ = c.Close() // a close's error has no reader: the handle has gone
		}
	}
	return nil
}

// stop is Close's, from hostNet.stop: the OS socket is closed, which ends the reader's ReadFrom, and every pending
// receive is dropped with its lend released and resolves nothing, as a dial does.
func (s *udpSocket) stop() {
	s.mu.Lock()
	c, already := s.conn, s.closed
	pend := s.pending
	s.closed, s.pending, s.held = true, nil, nil
	s.mu.Unlock()
	if c != nil && !already {
		_ = c.Close() // the instance is closing
	}
	for _, r := range pend {
		r.release()
		r.h.mu.Lock()
		r.h.sockPending--
		r.h.mu.Unlock()
	}
}

// The WIT's types.

// udpMessageType is receive's ok payload, `tuple<list<u8>, ip-socket-address>`.
func udpMessageType() canon.Type {
	u := u8T()
	l := canon.Type{Kind: canon.KindList, Elem: &u}
	return canon.TupleType(l, ipSocketAddressType())
}

// udpReceiveValue is a receive's result: `ok((data, from))`, or the read error's code.
func udpReceiveValue(d udpDatagram) canon.Value {
	mt := udpMessageType()
	if d.err != nil {
		return sockErr(&mt, udpErrorLabel(udpOpReceive, d.err))
	}
	elems := make([]canon.Value, len(d.data))
	for i, b := range d.data {
		elems[i] = canon.U8(b)
	}
	l, err := canon.List(u8T(), elems...)
	if err != nil {
		return sockErr(&mt, "other") // unreachable: every element is a u8
	}
	from, err := socketAddressValue(d.from)
	if err != nil {
		return sockErr(&mt, "other") // unreachable: an address always builds
	}
	m, err := canon.Record(mt, map[string]canon.Value{"0": l, "1": from})
	if err != nil {
		return sockErr(&mt, "other") // unreachable: the record is the type's
	}
	v, err := canon.Variant(sockResultType(&mt), "ok", &m)
	if err != nil {
		return sockErr(&mt, "other") // unreachable
	}
	return v
}

// numericZone is ap with a named IPv6 zone, as Go's net reports a sender's, replaced by its interface index, which is
// what an ip-socket-address's scope-id carries. A zone that names no interface is dropped.
func numericZone(ap netip.AddrPort) netip.AddrPort {
	z := ap.Addr().Zone()
	if z == "" {
		return ap
	}
	if _, err := strconv.ParseUint(z, 10, 32); err == nil {
		return ap
	}
	a := ap.Addr().WithZone("")
	if ifi, err := net.InterfaceByName(z); err == nil {
		a = a.WithZone(strconv.Itoa(ifi.Index))
	}
	return netip.AddrPortFrom(a, ap.Port())
}

// sameUDPAddr reports whether two addresses are one peer, the zone aside.
func sameUDPAddr(a, b netip.AddrPort) bool {
	return a.Port() == b.Port() && a.Addr().WithZone("") == b.Addr().WithZone("")
}

// Errors (ADR 0140): the WIT's per-function lists, POSIX and Windows' WSA numbers in one table, since a POSIX kernel
// never returns a number past 10000. Anything the lists do not name is ADR 0123's table, then `other`.

// udpOp names the function whose error is mapped.
type udpOp int

const (
	udpOpBind udpOp = iota
	udpOpConnect
	udpOpSend
	udpOpReceive
	udpOpOption
)

// Windows' WSA numbers the UDP lists name, beside wsaEADDRNOTAVAIL (hostsock.go).
const (
	wsaEINVAL       syscall.Errno = 10022
	wsaEDESTADDRREQ syscall.Errno = 10039
	wsaEMSGSIZE     syscall.Errno = 10040
	wsaEAFNOSUPPORT syscall.Errno = 10047
	wsaENETDOWN     syscall.Errno = 10050
	wsaENETRESET    syscall.Errno = 10052
	wsaECONNRESET   syscall.Errno = 10054
	wsaENOBUFS      syscall.Errno = 10055
	wsaEISCONN      syscall.Errno = 10056
	wsaEHOSTDOWN    syscall.Errno = 10064
)

// udpErrnoLabels is every function's list: the datagram's size, the remote's unreachability, and memory.
var udpErrnoLabels = map[syscall.Errno]string{
	syscall.EMSGSIZE: "datagram-too-large", wsaEMSGSIZE: "datagram-too-large",
	syscall.ECONNRESET: "remote-unreachable", wsaECONNRESET: "remote-unreachable",
	syscall.ENETRESET: "remote-unreachable", wsaENETRESET: "remote-unreachable",
	syscall.EHOSTDOWN: "remote-unreachable", wsaEHOSTDOWN: "remote-unreachable",
	syscall.ENETDOWN: "remote-unreachable", wsaENETDOWN: "remote-unreachable",
	syscall.ENOBUFS: "out-of-memory", wsaENOBUFS: "out-of-memory", syscall.ENOMEM: "out-of-memory",
}

// udpAddressErrnoLabels is connect's and send's: an address the kernel refuses is `invalid-argument`.
var udpAddressErrnoLabels = map[syscall.Errno]string{
	syscall.EADDRNOTAVAIL: "invalid-argument", wsaEADDRNOTAVAIL: "invalid-argument",
	syscall.EAFNOSUPPORT: "invalid-argument", wsaEAFNOSUPPORT: "invalid-argument",
	syscall.EDESTADDRREQ: "invalid-argument", wsaEDESTADDRREQ: "invalid-argument",
	syscall.EISCONN: "invalid-argument", wsaEISCONN: "invalid-argument",
	syscall.EINVAL: "invalid-argument", wsaEINVAL: "invalid-argument",
}

// udpErrorLabel maps a Go network error from op to its `error-code` case. An OS call this engine has no way to make on
// the platform (errors.ErrUnsupported, hostudp_other.go and hostudp_disconnect_unix.go) is `not-supported`.
func udpErrorLabel(op udpOp, err error) string {
	if errors.Is(err, errors.ErrUnsupported) {
		return "not-supported"
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch op {
		case udpOpBind:
			if errno == syscall.EADDRNOTAVAIL || errno == wsaEADDRNOTAVAIL {
				return "address-not-bindable"
			}
			if errno == syscall.EAFNOSUPPORT || errno == wsaEAFNOSUPPORT {
				return "invalid-argument"
			}
		case udpOpConnect, udpOpSend:
			if l, ok := udpAddressErrnoLabels[errno]; ok {
				return l
			}
		case udpOpReceive, udpOpOption:
			// No list of their own: every function's list below, then ADR 0123's table.
		}
		if l, ok := udpErrnoLabels[errno]; ok {
			return l
		}
	}
	return sockErrorLabel(err)
}

// The address rules, before any OS call.

// udpRemoteOK reports whether ap may be a remote address of a socket of family fam: its family, a port, an address
// that is not the unspecified one, and on an IPv6 socket not IPv4-mapped (V6ONLY). Each refusal is `invalid-argument`,
// udp.reading's connect-refusals and send-refusals.
//
// NEUTER, run: check the family alone, and on darwin TestUDPMatchesWasmtime's connect-refusals reads 0x82828200 (a
// connect to 0.0.0.0 ok, the kernel's "this host") and send-refusals 0x82820088 (a send to 127.0.0.1:0 ok, and one to
// 0.0.0.0 remote-unreachable), each in both gate states. The IPv4-mapped byte of both rows stays invalid-argument,
// darwin's kernel refusing a mapped address on a V6ONLY socket itself, so that check is vacuous here.
func udpRemoteOK(fam, apFam int, ap netip.AddrPort) bool {
	ip := ap.Addr()
	return apFam == fam && ap.Port() != 0 && !ip.IsUnspecified() && !ip.Is4In6()
}

// udpLocalOK reports whether ap may be bound by a socket of family fam: its family, and on an IPv6 socket not
// IPv4-mapped. The unspecified address and port 0 are allowed. udp.reading's bind-refusals and bind-more.
func udpLocalOK(fam, apFam int, ap netip.AddrPort) bool {
	return apFam == fam && !ap.Addr().Is4In6()
}

// network is the socket's Go network name.
func (s *udpSocket) network() string {
	if s.family == familyIPv6 {
		return "udp6"
	}
	return "udp4"
}

// bindLocked makes the OS socket at addr (ADR 0140): `net.ListenUDP`, which sets IPV6_V6ONLY on an IPv6 socket, then
// SO_BROADCAST cleared and the recorded options applied, then registered so Close closes it. The socket is bound, or
// left unbound with the error's code. The caller holds s.mu and has checked the address.
func (s *udpSocket) bindLocked(addr netip.AddrPort) string {
	if s.closed {
		return "invalid-state" // Close has stopped the instance's sockets
	}
	c, err := net.ListenUDP(s.network(), net.UDPAddrFromAddrPort(addr))
	if err != nil {
		return udpErrorLabel(udpOpBind, err)
	}
	rc, err := c.SyscallConn()
	if err == nil {
		err = s.configure(rc)
	}
	if err != nil {
		_ = c.Close() // the bind failed; nobody holds the socket
		return udpErrorLabel(udpOpOption, err)
	}
	if !s.net.adoptUDP(s) {
		_ = c.Close() // the instance is closing
		return "invalid-state"
	}
	s.conn, s.rc = c, rc
	return ""
}

// configure is what a new OS socket gets before anything can use it: SO_BROADCAST cleared, since Go's net sets it on
// every UDP socket and wasmtime's socket has none (no WASI option grants it), then the recorded options.
//
// NEUTER, run: set SO_BROADCAST rather than clear it, which is Go's net's own default, and
// TestEveryUDPSocketHasNoBroadcast reads it set on the bound and the implicitly bound socket. TestUDPMatchesWasmtime's
// broadcast row was not run under it, deliberately: it would put a datagram for 255.255.255.255 on the network.
func (s *udpSocket) configure(rc syscall.RawConn) error {
	if err := osSetInt(rc, udpBroadcastLevel, udpBroadcastName, 0); err != nil {
		return err
	}
	// NEUTER, run: return without applying the recorded options, and TestUDPMatchesWasmtime's options-carry row reads
	// 0x00010000 in both gate states, its hop limit of 42 lost at the bind. The row's receive-buffer byte stays 0 under
	// it, since darwin's default receive buffer is already past the 65536 asked for, so that half is vacuous here.
	return s.applyOptions(rc)
}

// applyOptions sets the recorded options on rc. A buffer's ENOBUFS is the kernel declining a size it may clamp, which
// the WIT accepts.
func (s *udpSocket) applyOptions(rc syscall.RawConn) error {
	if s.opts.hopSet {
		lvl, name := udpSockopt(s.family, udpOptHopLimit)
		if err := osSetInt(rc, lvl, name, int(s.opts.hop)); err != nil {
			return err
		}
	}
	for _, o := range []struct {
		set  bool
		opt  udpOpt
		size int
	}{{s.opts.rbufSet, udpOptRecvBuffer, s.opts.rbuf}, {s.opts.sbufSet, udpOptSendBuffer, s.opts.sbuf}} {
		if !o.set {
			continue
		}
		lvl, name := udpSockopt(s.family, o.opt)
		if err := osSetInt(rc, lvl, name, o.size); err != nil && !isNoBufs(err) {
			return err
		}
	}
	return nil
}

// isNoBufs reports ENOBUFS, darwin's answer to a buffer size past its kern.ipc.maxsockbuf.
func isNoBufs(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == syscall.ENOBUFS || errno == wsaENOBUFS)
}

// boundLocked binds the socket implicitly if it is not bound: its family's unspecified address, port 0, the WIT's
// implicit bind for connect and send.
func (s *udpSocket) boundLocked() string {
	if s.conn != nil {
		if s.closed {
			return "invalid-state"
		}
		return ""
	}
	return s.bindLocked(unspecifiedAddr(s.family))
}

// probeLocked reads an option before the bind (ADR 0140): from a socket made for the call on the family's unspecified
// address, with the recorded options applied, so the kernel's default and its rounding are the kernel's own. The
// caller holds s.mu.
//
// NEUTER, run: answer 0 for every get before the bind, with no probe (getOptionLocked), and TestUDPMatchesWasmtime's
// hop-limit row reads 0x00008200 (its read-back 42 lost), hop-defaults 0xff004000 (both fresh sockets' 0), and rbuf and
// sbuf 0x01008201 (a zero buffer, and the 65536 read back below 65536), each in both gate states. options-carry passes:
// its gets come after the bind.
func (s *udpSocket) probeLocked(o udpOpt) (int, error) {
	c, err := net.ListenUDP(s.network(), net.UDPAddrFromAddrPort(unspecifiedAddr(s.family)))
	if err != nil {
		return 0, err
	}
	defer c.Close()
	rc, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	if err := s.applyOptions(rc); err != nil {
		return 0, err
	}
	lvl, name := udpSockopt(s.family, o)
	return osGetInt(rc, lvl, name)
}

// getOptionLocked is an option's value: the OS socket's, or the probe's before the bind.
func (s *udpSocket) getOptionLocked(o udpOpt) (int, error) {
	if s.conn == nil {
		return s.probeLocked(o)
	}
	if s.closed {
		return 0, net.ErrClosed
	}
	lvl, name := udpSockopt(s.family, o)
	return osGetInt(s.rc, lvl, name)
}

// The functions.

// udpCreate is `[static]udp-socket.create: func(address-family) -> result<udp-socket, error-code>`, flattened to
// (family, ret). **With the network not granted it is `err(access-denied)`** (ADR 0140, Scott's ruling on #1376):
// wasmtime 49.0.3's reading with no network flag, and ADR 0123's point for TCP.
//
// NEUTER, run: drop the grant check, and TestUDPMatchesWasmtime's denied row reads both creates ok (0x00000000) in both
// gate states, as does the root package's TestInheritNetworkGrantsUDPOnBothConfiguredPaths' LoadComponent row.
// TestP3UdpMatchesWasmtime's denied rows, and cmd/burroughs' TestRunAComponentWithInheritNetworkUsesUDP's, pass under it:
// bind's own check answers access-denied, which std reads as the same PermissionDenied.
func (h *Host) udpCreate(res *hostResources) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("component: udp-socket.create: got %d core args, want 2 (address-family, ret)", len(args))
		}
		fam := uint32(args[0].Bits)
		if fam > familyIPv6 {
			return nil, &interp.Trap{Reason: fmt.Sprintf("udp-socket.create: ip-address-family case %d out of range", fam)}
		}
		own := canon.OwnType(udpSocketResource.rt)
		ret := uint64(uint32(args[1].Bits))
		if !h.InheritNetwork {
			return nil, lowerResult(c, ret, sockErr(&own, "access-denied"), nil)
		}
		idx := res.mint(udpSocketResource, &udpSocket{net: res.net, family: int(fam)})
		handle := canon.Own(udpSocketResource.rt, uint32(idx))
		v, err := canon.Variant(sockResultType(&own), "ok", &handle)
		return nil, lowerResult(c, ret, v, err)
	}
}

// udpResult lowers `result<_, error-code>` at ret: ok for an empty label, else its code.
func udpResult(c *interp.CanonCaller, ret uint64, label string) error {
	if label != "" {
		return lowerResult(c, ret, sockErr(nil, label), nil)
	}
	return lowerResult(c, ret, sockOK(), nil)
}

// udpBind is `[method]udp-socket.bind: func(local-address: ip-socket-address) -> result<_, error-code>`, flattened to
// (self, the address's 12 words, ret). In order: the grant, the address's family, a bound socket's `invalid-state`,
// then the OS's bind.
func (h *Host) udpBind(_ *hostResources, c *interp.CanonCaller, self *udpSocket, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 13 {
		return nil, fmt.Errorf("component: udp-socket.bind: got %d core args, want 14 (self, local-address, ret)", len(args)+1)
	}
	fam, addr, err := decodeSocketAddressOf("udp-socket.bind", args[:12])
	if err != nil {
		return nil, err
	}
	var label string
	switch {
	case !h.InheritNetwork:
		label = "access-denied" // unreachable while create refuses: the grant is checked at each
	case !udpLocalOK(self.family, fam, addr):
		label = "invalid-argument"
	default:
		self.mu.Lock()
		if self.conn != nil {
			label = "invalid-state"
		} else {
			label = self.bindLocked(addr)
		}
		self.mu.Unlock()
	}
	return nil, udpResult(c, uint64(uint32(args[12].Bits)), label)
}

// udpConnect is `[method]udp-socket.connect: func(remote-address: ip-socket-address) -> result<_, error-code>`, the
// OS's connect on the socket (ADR 0140), flattened to (self, the address's 12 words, ret). The kernel then filters
// what arrives, surfaces ICMP errors, and picks the local address. A connected socket is disconnected first, as the
// WIT's implementors' note asks, and an unbound one is bound implicitly. A held datagram from anyone but the new peer
// is dropped, the WIT's promise for receive.
//
// NEUTER, run: skip the dropHeldFromOthersLocked call, and TestAHeldDatagramFromAnotherIsDroppedAtConnect's receive is
// C's datagram. **TestUDPMatchesWasmtime and TestP3UdpMatchesWasmtime pass under it**: no row holds a datagram at a
// connect, and connect-after-arrival's straggler is in the kernel's queue, which the reader's filter drops.
func (h *Host) udpConnect(_ *hostResources, c *interp.CanonCaller, self *udpSocket, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 13 {
		return nil, fmt.Errorf("component: udp-socket.connect: got %d core args, want 14 (self, remote-address, ret)", len(args)+1)
	}
	fam, addr, err := decodeSocketAddressOf("udp-socket.connect", args[:12])
	if err != nil {
		return nil, err
	}
	var label string
	switch {
	case !h.InheritNetwork:
		label = "access-denied" // unreachable while create refuses
	case !udpRemoteOK(self.family, fam, addr):
		label = "invalid-argument"
	default:
		self.mu.Lock()
		label = self.boundLocked()
		// NEUTER, run: skip this disconnect, and TestUDPMatchesWasmtime passes, its reconnect row included: **vacuous on
		// darwin**, whose connect replaces a datagram socket's association by itself, as that row shows. It is the WIT's
		// implementors' note, for a kernel that requires the disconnect, and no kernel measured here does (a native probe
		// in a Linux container reconnected with no disconnect between, keeping the port). On Linux this disconnect is
		// what unbinds a port the kernel chose, which osDisconnect binds again.
		if label == "" && self.connected {
			dissolved, derr := osDisconnect(self.rc, self.family)
			if dissolved {
				self.connected, self.remote = false, netip.AddrPort{}
			}
			if derr != nil {
				label = udpErrorLabel(udpOpConnect, derr)
			}
		}
		if label == "" {
			if cerr := osConnect(self.rc, addr); cerr != nil {
				label = udpErrorLabel(udpOpConnect, cerr)
			} else {
				self.connected, self.remote = true, addr
				self.dropHeldFromOthersLocked()
			}
		}
		self.mu.Unlock()
	}
	return nil, udpResult(c, uint64(uint32(args[12].Bits)), label)
}

// dropHeldFromOthersLocked drops a held datagram from anyone but the connected peer. A held error stays: it is the
// kernel's report to whoever receives next.
func (s *udpSocket) dropHeldFromOthersLocked() {
	kept := s.held[:0]
	for _, d := range s.held {
		if d.err != nil || sameUDPAddr(d.from, s.remote) {
			kept = append(kept, d)
		}
	}
	s.held = kept
}

// udpDisconnect is `[method]udp-socket.disconnect: func() -> result<_, error-code>`, flattened to (self, ret): a socket
// that is not connected is `invalid-state`, and otherwise the OS's connect to AF_UNSPEC (osDisconnect).
//
// NEUTER, run: answer ok and forget the peer without osDisconnect, and TestUDPMatchesWasmtime's disconnect-receive row
// waits until the hang guard in both gate states, the kernel still filtering for B so C's datagram never arrives; and
// on darwin disconnect-local reads 0x01010000 and disconnect-any 0x01000000, each local address left 127.0.0.1. The
// disconnect row passes under it: what it reads is the host's own bookkeeping.
func (h *Host) udpDisconnect(_ *hostResources, c *interp.CanonCaller, self *udpSocket, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("component: udp-socket.disconnect: got %d core args, want 2 (self, ret)", len(args)+1)
	}
	label := ""
	self.mu.Lock()
	switch {
	case !self.connected || self.closed:
		label = "invalid-state"
	default:
		// A disconnect that dissolved the association and then failed to keep the port (Linux's rebind, when another
		// socket took the port in the window) leaves the socket unconnected and reports the bind's error.
		dissolved, err := osDisconnect(self.rc, self.family)
		if dissolved {
			self.connected, self.remote = false, netip.AddrPort{}
		}
		if err != nil {
			label = udpErrorLabel(udpOpConnect, err)
		}
	}
	self.mu.Unlock()
	return nil, udpResult(c, uint64(uint32(args[0].Bits)), label)
}

// udpSend is `[method]udp-socket.send: async func(data: list<u8>, remote-address: option<ip-socket-address>) ->
// result<_, error-code>`, resolved inline on the guest's goroutine (ADR 0140). Its 16 flat parameters are self, the
// list's (ptr, len), and the option's case and the address's 12 words: spilled past MAX_FLAT_ASYNC_PARAMS for an
// async lower and loaded flat (ADR 0123), and flat for the blocking sync lower (ADR 0126). The list is lifted first, so
// one past the end of memory is the guest's trap at the pin's place.
func (h *Host) udpSend(_ *asyncHandles, res *hostResources) asyncLowerImpl {
	return func(c *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
		args := onStart()
		if len(args) != 16 {
			return nil, fmt.Errorf("component: udp-socket.send: got %d flat parameters, want 16 (self, data, remote-address)", len(args))
		}
		sock, release, err := lendAs[*udpSocket](res, udpSocketResource, args[0].Int32())
		if err != nil {
			return nil, err
		}
		data, err := canon.LoadListU8(guestHeap{c}, int(uint32(args[1].Bits)), int(uint32(args[2].Bits)))
		if err != nil {
			release()
			return nil, &interp.Trap{Reason: fmt.Sprintf("udp-socket.send: lifting the data: %v", err)}
		}
		var to netip.AddrPort
		toFam, has := 0, false
		switch uint32(args[3].Bits) {
		case 0:
		case 1:
			has = true
			if toFam, to, err = decodeSocketAddressOf("udp-socket.send", args[4:16]); err != nil {
				release()
				return nil, err
			}
		default:
			release()
			return nil, &interp.Trap{Reason: fmt.Sprintf("udp-socket.send: option case %d out of range", uint32(args[3].Bits))}
		}
		label := sock.send(h, data, has, toFam, to)
		release()
		if label != "" {
			onResolve(sockErr(nil, label))
		} else {
			onResolve(sockOK())
		}
		return nil, nil
	}
}

// send is one datagram, in send's order: the grant, the size, the address's refusals, the connected socket's peer or
// the unconnected one's missing address, the implicit bind, then the write: `Write` on a connected socket, the OS's
// send to its peer, and `WriteToUDPAddrPort` on an unconnected one. (Go's net, which did not make the connect, would
// pass a connected socket an address, which POSIX lets a kernel refuse with EISCONN.) The write is made with no lock
// held.
//
// NEUTER, run: drop the two connection-state refusals in one run, and TestUDPMatchesWasmtime's send-mismatch row reads
// 0 (the datagram meant for C written to the peer, B), and send-none 0x00008e8e and disconnect 0x8e858500 (Go's
// "missing address", `other`, where wasmtime reads invalid-argument), each in both gate states.
//
// NEUTER, run: drop the size check, and TestUDPMatchesWasmtime's sizes-64k row reads 0x8d8d0000 in both gate states
// on darwin: its two ipv6 sends of 65536 and 65537 bytes are ok, darwin's kernel taking them from a socket with a 1 MiB
// send buffer, where wasmtime reads datagram-too-large. Its ipv4 pair is the kernel's refusal either way.
func (s *udpSocket) send(h *Host, data []byte, has bool, toFam int, to netip.AddrPort) string {
	if !h.InheritNetwork {
		return "access-denied" // unreachable while create refuses
	}
	if len(data) > udpMaxDatagram {
		return "datagram-too-large"
	}
	if has && !udpRemoteOK(s.family, toFam, to) {
		return "invalid-argument"
	}
	s.mu.Lock()
	switch {
	case s.connected && has && !sameUDPAddr(to, s.remote):
		s.mu.Unlock()
		return "invalid-argument" // EISCONN: a connected socket sends only to its peer
	case !s.connected && !has:
		s.mu.Unlock()
		return "invalid-argument" // EDESTADDRREQ
	}
	if l := s.boundLocked(); l != "" {
		s.mu.Unlock()
		return l
	}
	conn, connected := s.conn, s.connected
	s.mu.Unlock()
	var err error
	if connected {
		_, err = conn.Write(data)
	} else {
		_, err = conn.WriteToUDPAddrPort(data, to)
	}
	if err != nil {
		return udpErrorLabel(udpOpSend, err)
	}
	return ""
}

// udpReceive is `[method]udp-socket.receive: async func() -> result<tuple<list<u8>, ip-socket-address>, error-code>`,
// its one flat parameter self (ADR 0140). In order: the grant; a socket that is not bound is `invalid-state`; a held
// datagram is the result, inline; otherwise the receive is pending, holds its lend of self, and counts in sockPending
// as a waker, and the socket's reader is started, if it is not running, as the impl's host window closes. It returns
// STARTED, as wasmtime 49.0.3 does even for a datagram that has arrived (udp.reading's queued-status, #1414).
//
// The reader is a goroutine of its own, the one receive site, authorised by ADR 0140 in the census
// (TestEveryEngineGoroutineIsAtASiteADecisionAuthorises). It runs no guest code, and its result reaches the guest
// through onResolve, ADR 0101's discipline. It ends when no receive is pending, or when the socket is closed.
func (h *Host) udpReceive(async *asyncHandles, res *hostResources) asyncLowerImpl {
	return func(c *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
		args := onStart()
		if len(args) != 1 {
			return nil, fmt.Errorf("component: udp-socket.receive: got %d flat parameters, want 1 (self)", len(args))
		}
		sock, release, err := lendAs[*udpSocket](res, udpSocketResource, args[0].Int32())
		if err != nil {
			return nil, err
		}
		mt := udpMessageType()
		fail := func(label string) (func(), error) {
			release()
			onResolve(sockErr(&mt, label))
			return nil, nil
		}
		if !h.InheritNetwork {
			return fail("access-denied") // unreachable while create refuses
		}
		// Raised before the socket's lock is taken, the permitted order, and lowered again if the receive does not pend.
		async.mu.Lock()
		async.sockPending++
		async.mu.Unlock()
		unpend := func() {
			async.mu.Lock()
			async.sockPending--
			async.mu.Unlock()
		}
		sock.mu.Lock()
		if sock.conn == nil || sock.closed {
			sock.mu.Unlock()
			unpend()
			return fail("invalid-state")
		}
		// NEUTER, run: skip the held datagram, and TestACancelledReceiveTakesNothing's receive waits until the hang guard,
		// the datagram held and never delivered. **TestUDPMatchesWasmtime's cancel row, TestP3UdpMatchesWasmtime and
		// TestAHeldDatagramFromAnotherIsDroppedAtConnect pass under it**: the row's next receive usually reaches the socket
		// while the reader is still reading, so the reader delivers to it; wasi-libc never cancels a receive; and the
		// connect test's held datagram is one it wants dropped.
		if len(sock.held) > 0 {
			d := sock.held[0]
			sock.held = sock.held[1:]
			sock.mu.Unlock()
			unpend()
			release()
			onResolve(udpReceiveValue(d))
			return nil, nil
		}
		r := &udpRecv{h: async, release: release, resolve: onResolve}
		sock.pending = append(sock.pending, r)
		var ended func()
		if !sock.reading {
			e, ok := sock.net.track()
			if !ok {
				sock.pending = sock.pending[:len(sock.pending)-1]
				sock.mu.Unlock()
				unpend()
				return fail("invalid-state") // Close has stopped the instance's sockets
			}
			ended, sock.reading = e, true
			if sock.buf == nil {
				sock.buf = make([]byte, udpMaxDatagram)
			}
		}
		conn := sock.conn
		sock.mu.Unlock()
		if ended != nil {
			// **The reader starts as the impl's host window closes, never inside it** (#1414). A resolution that arrives
			// while the window is open is queued and applied by leaveHost before the lower reads whether the subtask has
			// resolved, so a reader that read a datagram already in the kernel's queue made the lower return RETURNED,
			// where wasmtime 49.0.3 always returns STARTED. Started from the window's queue instead, the reader's first
			// resolution finds the window closed and waits for the run slot, which the caller holds until the lower has
			// returned STARTED. Outside any window (a test's direct call), it starts at once.
			//
			// NEUTER, run in a Linux container: start the reader here at once, as before #1414, and
			// TestUDPMatchesWasmtime's queued-status reads 0x01000200 (RETURNED) in 35 of 400 runs under -race (200 per
			// gate state: 14 with cm-threads off, 21 on), and in none of 2400 plain runs there; unmodified main had read it
			// in 1 of 400 plain. With the reader started here, 0 of 400 under -race and 0 of 2400 plain.
			async.afterHost(c, func() {
				go func() {
					defer ended()
					sock.read(conn)
				}()
			})
		}
		return r.cancelSubtask(sock), nil
	}
}

// read is the reader's body: one ReadFrom at a time with no lock held, each result to the first pending receive, or
// held for the next when none is waiting; it returns when no receive is pending, or the socket is closed. A connected
// socket's reader drops a datagram from anyone but its peer.
//
// NEUTER, run: hand each datagram to the last pending receive rather than the first, and TestReceivesAreServedInOrder's
// first receive gets the second datagram and its second the first. **TestUDPMatchesWasmtime and TestP3UdpMatchesWasmtime
// pass under it**: no row and no std call has two receives pending at once.
func (s *udpSocket) read(conn *net.UDPConn) {
	for {
		n, from, err := conn.ReadFromUDPAddrPort(s.buf)
		s.mu.Lock()
		if s.closed || errors.Is(err, net.ErrClosed) {
			s.reading = false
			s.mu.Unlock()
			return
		}
		d := udpDatagram{err: err}
		if err == nil {
			// NEUTER, run: keep the datagram in the reader's buffer rather than copy it out, and every UDP test passes:
			// **vacuous by construction**. A delivered datagram is built into its value before this reader reads again,
			// and a held one is taken before another reader can start, so nothing overwrites the buffer under it. The
			// copy stays so that the buffer's reuse is not a property of the call order.
			d.data, d.from = append([]byte(nil), s.buf[:n]...), numericZone(from)
			// NEUTER, run: drop this filter, and TestUDPMatchesWasmtime's connect-after-arrival row reads wasmtime's
			// 0x01000000 (C's datagram) in both gate states, where Burroughs asserts the WIT's 0x00010000. connect-filter
			// passes under it: what arrives after the connect is the kernel's to drop.
			if s.connected && !sameUDPAddr(d.from, s.remote) {
				s.mu.Unlock()
				continue
			}
		}
		if len(s.pending) == 0 {
			s.held = append(s.held, d)
			s.reading = false
			s.mu.Unlock()
			return
		}
		r := s.pending[0]
		s.pending = s.pending[1:]
		more := len(s.pending) > 0
		if !more {
			s.reading = false
		}
		s.mu.Unlock()
		r.finish(d)
		if !more {
			return
		}
	}
}

// finish resolves a receive the reader claimed: its lend released, its result stored by the lower's onResolve, and its
// waker lowered once that is done.
//
// NEUTER, run: skip the resolve, and TestUDPMatchesWasmtime's sendto-v4, cancel, blocked and queued-status rows (asked
// with cm-threads off, as a sample) each wait until the hang guard, as does TestReceivesAreServedInOrder's first
// receive. receive-unbound, the control, passes: its invalid-state resolves inline, not through the reader.
func (r *udpRecv) finish(d udpDatagram) {
	r.release()
	r.resolve(udpReceiveValue(d))
	r.h.mu.Lock()
	r.h.sockPending--
	r.h.mu.Unlock()
}

// cancelSubtask is a pending receive's onCancel (ADR 0140), sockDial.cancelSubtask's shape: it runs as host code on the
// canceller's goroutine and claims the receive from the socket's queue, so its CANCELLED resolution is queued and
// applied before the canceller reads the subtask's state, and the cancel reads CANCELLED_BEFORE_RETURNED at once. A
// receive the reader or Close claimed first is left to them. The reader runs on, and what it reads is held for the
// next receive, so the cancelled one takes nothing.
//
// NEUTER, run: return without claiming or resolving, and TestUDPMatchesWasmtime's cancel row waits until the hang guard
// in both gate states, its cancel BLOCKED and the receive still pending for a datagram the row sends only after the
// cancel returns. TestACancelledReceiveTakesNothing and TestAHeldDatagramFromAnotherIsDroppedAtConnect wait at their
// guards too: the datagram goes to the receive that was not cancelled, and none is held. TestP3UdpMatchesWasmtime
// passes: wasi-libc never cancels a receive.
func (r *udpRecv) cancelSubtask(s *udpSocket) func() {
	return func() {
		s.mu.Lock()
		at := -1
		for i, p := range s.pending {
			if p == r {
				at = i
				break
			}
		}
		if at < 0 {
			s.mu.Unlock()
			return
		}
		s.pending = append(s.pending[:at], s.pending[at+1:]...)
		s.mu.Unlock()
		r.release()
		r.resolve(canon.NoResolution())
		r.h.mu.Lock()
		r.h.sockPending--
		r.h.mu.Unlock()
	}
}

// udpGetLocalAddress is `get-local-address: func() -> result<ip-socket-address, error-code>`, flattened to (self,
// ret): `invalid-state` until the socket is bound, and then the kernel's getsockname, read at the call, since a
// connect and a disconnect move it.
//
// NEUTER, run: answer the conn's LocalAddr, which Go caches at the bind, rather than getsockname, and
// TestUDPMatchesWasmtime's connect-implicit (0x00000000), dial-v4 and dial-v6 (0x00010001), connect-any (0x01000000)
// and, on darwin, disconnect-local (0x01010000) rows fail in both gate states: a connect's move of the local address
// is not seen.
func (h *Host) udpGetLocalAddress(_ *hostResources, c *interp.CanonCaller, self *udpSocket, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("component: udp-socket.get-local-address: got %d core args, want 2 (self, ret)", len(args)+1)
	}
	self.mu.Lock()
	var ap netip.AddrPort
	label := ""
	switch {
	case self.conn == nil || self.closed:
		label = "invalid-state"
	default:
		var err error
		if ap, err = osLocalAddr(self.rc); err != nil {
			label = udpErrorLabel(udpOpOption, err)
		}
	}
	self.mu.Unlock()
	return nil, udpAddressResult(c, uint64(uint32(args[0].Bits)), ap, label, "get-local-address")
}

// udpGetRemoteAddress is `get-remote-address: func() -> result<ip-socket-address, error-code>`: the connected peer,
// or `invalid-state`.
func (h *Host) udpGetRemoteAddress(_ *hostResources, c *interp.CanonCaller, self *udpSocket, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("component: udp-socket.get-remote-address: got %d core args, want 2 (self, ret)", len(args)+1)
	}
	self.mu.Lock()
	ap, label := self.remote, ""
	if !self.connected {
		label = "invalid-state"
	}
	self.mu.Unlock()
	return nil, udpAddressResult(c, uint64(uint32(args[0].Bits)), ap, label, "get-remote-address")
}

// udpAddressResult lowers `result<ip-socket-address, error-code>` at ret.
func udpAddressResult(c *interp.CanonCaller, ret uint64, ap netip.AddrPort, label, name string) error {
	addrT := ipSocketAddressType()
	if label != "" {
		return lowerResult(c, ret, sockErr(&addrT, label), nil)
	}
	av, err := socketAddressValue(ap)
	if err != nil {
		return fmt.Errorf("component: udp-socket.%s: %w", name, err)
	}
	v, verr := canon.Variant(sockResultType(&addrT), "ok", &av)
	return lowerResult(c, ret, v, verr)
}

// udpGetAddressFamily is `get-address-family: func() -> ip-address-family`, one flat i32: create's.
func (h *Host) udpGetAddressFamily(_ *hostResources, _ *interp.CanonCaller, self *udpSocket, _ []interp.Value) ([]interp.Value, error) {
	return []interp.Value{interp.I32(int32(self.family))}, nil
}

// udpGetHopLimit is `get-unicast-hop-limit: func() -> result<u8, error-code>`, flattened to (self, ret): IP_TTL or
// IPV6_UNICAST_HOPS read as a u8, so darwin's IPv6 −1 is 255, as wasmtime 49.0.3 reads it.
func (h *Host) udpGetHopLimit(_ *hostResources, c *interp.CanonCaller, self *udpSocket, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("component: udp-socket.get-unicast-hop-limit: got %d core args, want 2 (self, ret)", len(args)+1)
	}
	ret := uint64(uint32(args[0].Bits))
	u := u8T()
	self.mu.Lock()
	v, err := self.getOptionLocked(udpOptHopLimit)
	self.mu.Unlock()
	if err != nil {
		return nil, lowerResult(c, ret, sockErr(&u, udpErrorLabel(udpOpOption, err)), nil)
	}
	val := canon.U8(uint8(v))
	r, verr := canon.Variant(sockResultType(&u), "ok", &val)
	return nil, lowerResult(c, ret, r, verr)
}

// udpSetHopLimit is `set-unicast-hop-limit: func(value: u8) -> result<_, error-code>`, flattened to (self, value,
// ret): 0 is `invalid-argument`, as the WIT says; before the bind the value is recorded, and after it set.
func (h *Host) udpSetHopLimit(_ *hostResources, c *interp.CanonCaller, self *udpSocket, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("component: udp-socket.set-unicast-hop-limit: got %d core args, want 3 (self, value, ret)", len(args)+1)
	}
	v := uint8(args[0].Bits) // def:lift_flat_unsigned keeps the low 8 bits
	label := ""
	if v == 0 {
		label = "invalid-argument"
	} else {
		self.mu.Lock()
		switch {
		case self.conn == nil:
			self.opts.hopSet, self.opts.hop = true, v
		case self.closed:
			label = "invalid-state"
		default:
			lvl, name := udpSockopt(self.family, udpOptHopLimit)
			if err := osSetInt(self.rc, lvl, name, int(v)); err != nil {
				label = udpErrorLabel(udpOpOption, err)
			}
		}
		self.mu.Unlock()
	}
	return nil, udpResult(c, uint64(uint32(args[1].Bits)), label)
}

// udpGetBufferSize is get-receive-buffer-size's and get-send-buffer-size's: `func() -> result<u64, error-code>`,
// flattened to (self, ret), SO_RCVBUF or SO_SNDBUF as the kernel reports it.
func (h *Host) udpGetBufferSize(o udpOpt) func(*hostResources, *interp.CanonCaller, *udpSocket, []interp.Value) ([]interp.Value, error) {
	return func(_ *hostResources, c *interp.CanonCaller, self *udpSocket, args []interp.Value) ([]interp.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("component: udp-socket buffer size: got %d core args, want 2 (self, ret)", len(args)+1)
		}
		ret := uint64(uint32(args[0].Bits))
		u := canon.Type{Kind: canon.KindU64}
		self.mu.Lock()
		v, err := self.getOptionLocked(o)
		self.mu.Unlock()
		if err != nil {
			return nil, lowerResult(c, ret, sockErr(&u, udpErrorLabel(udpOpOption, err)), nil)
		}
		val := canon.U64(uint64(max(v, 0)))
		r, verr := canon.Variant(sockResultType(&u), "ok", &val)
		return nil, lowerResult(c, ret, r, verr)
	}
}

// udpSetBufferSize is set-receive-buffer-size's and set-send-buffer-size's: `func(value: u64) -> result<_,
// error-code>`, flattened to (self, value, ret). 0 is `invalid-argument`; anything else is clamped to [1, 2^31−1] and
// accepted, a kernel's ENOBUFS included, the WIT's "accepted without error, but may be clamped".
func (h *Host) udpSetBufferSize(o udpOpt) func(*hostResources, *interp.CanonCaller, *udpSocket, []interp.Value) ([]interp.Value, error) {
	return func(_ *hostResources, c *interp.CanonCaller, self *udpSocket, args []interp.Value) ([]interp.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("component: udp-socket buffer size: got %d core args, want 3 (self, value, ret)", len(args)+1)
		}
		v := args[0].Bits
		label := ""
		if v == 0 {
			label = "invalid-argument"
		} else {
			size := int(min(v, math.MaxInt32))
			self.mu.Lock()
			switch {
			case self.conn == nil && o == udpOptRecvBuffer:
				self.opts.rbufSet, self.opts.rbuf = true, size
			case self.conn == nil:
				self.opts.sbufSet, self.opts.sbuf = true, size
			case self.closed:
				label = "invalid-state"
			default:
				lvl, name := udpSockopt(self.family, o)
				if err := osSetInt(self.rc, lvl, name, size); err != nil && !isNoBufs(err) {
					label = udpErrorLabel(udpOpOption, err)
				}
			}
			self.mu.Unlock()
		}
		return nil, udpResult(c, uint64(uint32(args[1].Bits)), label)
	}
}
