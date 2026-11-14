// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"syscall"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// WASI 0.3's TCP client (#1235's E7a, ADR 0123): `wasi:sockets/types@0.3`'s `tcp-socket` as a host resource,
// its async `connect`, `send` and `receive` over streams, and the getters Rust std reaches. `bind`, `listen` and
// name lookup are E7b's and E7c's (#1277, #1278).
//
// # One OS socket, shared
//
// `sockets.wit` gives the handle and the streams derived from it one OS socket, which closes only when the last
// of them goes. So the connection is reference-counted (`sockConn`): connect's success gives the handle one
// reference, `receive` and `send` take one each and release it when their stream is finished, and the last
// release closes the `net.Conn`.
//
// # Goroutines, and Close
//
// Three kinds run per socket: the dial, the send writer, and the receive producer's Read (hostproducer.go).
// Each is counted in the instance's registry (`hostNet`) before it starts, and the registry refuses to count one
// after `Close` has stopped it. `Close` cancels every pending dial and closes every connection, which unblocks
// every Read and Write, and then **waits for every one of them** (walker.close). This is where a socket differs
// from stdin, whose Read is abandoned (#1274): a socket is the instance's own to close.
//
// # Locks
//
// `asyncHandles.mu` may be held while the registry's lock or a socket's is taken, never the reverse. A resource
// lend is taken and released with neither held.

// tcpSocketResource is `wasi:sockets/types@0.3::tcp-socket`. Its destructor releases the handle's reference to
// the connection, which closes it if no stream holds one.
var tcpSocketResource = newHostResource("wasi:sockets/types::tcp-socket", destroyTCPSocket)

// sendBufCap bounds the bytes a send holds that the guest has written and the connection has not yet taken.
const sendBufCap = 64 << 10

// The address families, in `ip-address-family`'s case order, which is its flat encoding.
const (
	familyIPv4 = 0
	familyIPv6 = 1
)

type tcpState int

const (
	tcpUnbound tcpState = iota
	tcpConnecting
	tcpConnected
	tcpClosed
)

// tcpSocket is the object behind a `tcp-socket` handle. Its fields are guarded by mu, a leaf lock.
type tcpSocket struct {
	net    *hostNet
	family int
	mu     sync.Mutex
	state  tcpState
	conn   *sockConn // the handle's reference, once connected
	local  netip.AddrPort
	remote netip.AddrPort
	// received and sent record that receive and send were called: each may be called once.
	received, sent bool
}

// sockConn is one OS connection and its reference count, guarded by the registry's lock.
type sockConn struct {
	c      *net.TCPConn
	refs   int
	closed bool
}

// sockDial is one pending connect, in its instance's registry until its dial returns, the guest cancels it, or
// Close stops it: E5's claim pattern (hostwait.go), so exactly one of the three resolves it.
type sockDial struct {
	h       *asyncHandles
	sock    *tcpSocket
	cancel  context.CancelFunc
	release func() // the connect's lend of self
	resolve func(canon.Value)
}

// hostNet is one component instance's sockets: its live connections and pending dials, and every socket
// goroutine it has started. It lives in the instance's hostResources, so a nested instance has its own.
type hostNet struct {
	mu      sync.Mutex
	conns   map[*sockConn]struct{}
	dials   map[*sockDial]struct{}
	stopped bool
	wg      sync.WaitGroup
}

func newHostNet() *hostNet {
	return &hostNet{conns: map[*sockConn]struct{}{}, dials: map[*sockDial]struct{}{}}
}

// track counts a socket goroutine about to start, and returns the call that marks it ended. After stop, none is
// counted and none may start.
func (n *hostNet) track() (ended func(), ok bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped {
		return nil, false
	}
	n.wg.Add(1)
	return n.wg.Done, true
}

// startDial registers a pending dial and counts its goroutine.
func (n *hostNet) startDial(d *sockDial) (ended func(), ok bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped {
		return nil, false
	}
	n.dials[d] = struct{}{}
	n.wg.Add(1)
	return n.wg.Done, true
}

// claim removes d from the registry and reports whether this caller removed it.
func (n *hostNet) claim(d *sockDial) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.dials[d]; !ok {
		return false
	}
	delete(n.dials, d)
	return true
}

// adopt registers a new connection with one reference, the handle's. After stop it is closed instead.
func (n *hostNet) adopt(c *net.TCPConn) (*sockConn, bool) {
	n.mu.Lock()
	if n.stopped {
		n.mu.Unlock()
		_ = c.Close() // the instance is closing; nobody reads the close's error
		return nil, false
	}
	sc := &sockConn{c: c, refs: 1}
	n.conns[sc] = struct{}{}
	n.mu.Unlock()
	return sc, true
}

// retain takes another reference to a live connection.
func (n *hostNet) retain(sc *sockConn) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if sc.closed {
		return false
	}
	sc.refs++
	return true
}

// release drops one reference, and the last closes the connection, with the lock released.
//
// NEUTER: close at every release, and TestTheConnectionOutlivesTheHandleWhileAStreamHoldsIt's read after the
// handle's drop is DROPPED with nothing.
func (n *hostNet) release(sc *sockConn) {
	n.mu.Lock()
	sc.refs--
	last := sc.refs <= 0 && !sc.closed
	if last {
		sc.closed = true
		delete(n.conns, sc)
	}
	n.mu.Unlock()
	if last {
		_ = sc.c.Close() // a close's error has no reader: every holder has gone
	}
}

// stop is Close's: no socket goroutine starts after it, every pending dial is cancelled and its lend released,
// and every connection is closed, which ends every Read and Write in flight. wait then waits them out.
//
// NEUTER: return before closing the connections, and TestCloseEndsASocketsGoroutines' Close hangs on the
// receive reader blocked in Read.
func (n *hostNet) stop() {
	n.mu.Lock()
	n.stopped = true
	dials := n.dials
	n.dials = map[*sockDial]struct{}{}
	conns := make([]*sockConn, 0, len(n.conns))
	for sc := range n.conns {
		sc.closed = true
		conns = append(conns, sc)
	}
	n.conns = map[*sockConn]struct{}{}
	n.mu.Unlock()
	for d := range dials {
		d.cancel()
		d.release()
		d.h.mu.Lock()
		d.h.sockPending--
		d.h.mu.Unlock()
	}
	for _, sc := range conns {
		_ = sc.c.Close() // the instance is closing
	}
}

// wait returns once every socket goroutine has ended.
func (n *hostNet) wait() { n.wg.Wait() }

// destroyTCPSocket is the handle's destructor: the socket is closed, and its reference to the connection goes.
func destroyTCPSocket(obj any) error {
	s, ok := obj.(*tcpSocket)
	if !ok {
		return fmt.Errorf("component: a tcp-socket handle held a %T", obj)
	}
	s.mu.Lock()
	sc := s.conn
	s.conn, s.state = nil, tcpClosed
	s.mu.Unlock()
	if sc != nil {
		s.net.release(sc)
	}
	return nil
}

// The WIT's types, built through the codec's constructors.

// sockErrorCodeLabels are `wasi:sockets/types@0.3`'s `error-code` cases, in order; the last, `other`, carries an
// `option<string>`.
var sockErrorCodeLabels = []string{
	"access-denied", "not-supported", "invalid-argument", "out-of-memory", "timeout", "invalid-state",
	"address-not-bindable", "address-in-use", "remote-unreachable", "connection-refused", "connection-broken",
	"connection-reset", "connection-aborted", "datagram-too-large", "other",
}

func sockErrorCodeType() canon.Type {
	other := canon.OptionType(canon.Type{Kind: canon.KindString})
	cases := make([]canon.Case, len(sockErrorCodeLabels))
	for i, l := range sockErrorCodeLabels {
		cases[i] = canon.Case{Name: l}
	}
	cases[len(cases)-1].Type = &other
	return canon.VariantType(cases...)
}

// sockResultType is `result<ok, error-code>`; nil ok is `result<_, error-code>`, the futures' payload and
// connect's result.
func sockResultType(ok *canon.Type) canon.Type {
	ec := sockErrorCodeType()
	return canon.ResultType(ok, &ec)
}

func sockFuturePayloadType() (canon.Type, error) { return sockResultType(nil), nil }

// sockErr is `result::err(code)` at `result<ok, error-code>`. `other` carries none: a string would be lowered
// through the guest's realloc, from a goroutine's store (ADR 0123).
func sockErr(ok *canon.Type, label string) canon.Value {
	ec := sockErrorCodeType()
	var code canon.Value
	var err error
	if label == "other" {
		none, nerr := canon.None(canon.OptionType(canon.Type{Kind: canon.KindString}))
		if nerr != nil {
			panic(nerr) // unreachable: a constant option type
		}
		code, err = canon.Variant(ec, "other", &none)
	} else {
		code, err = canon.Variant(ec, label, nil)
	}
	if err != nil {
		panic(err) // unreachable: every label passed is one of sockErrorCodeLabels
	}
	v, err := canon.Variant(sockResultType(ok), "error", &code)
	if err != nil {
		panic(err)
	}
	return v
}

// sockOK is `result::ok` at `result<_, error-code>`.
func sockOK() canon.Value {
	v, err := canon.Variant(sockResultType(nil), "ok", nil)
	if err != nil {
		panic(err) // unreachable: a constant type
	}
	return v
}

func u8T() canon.Type  { return canon.Type{Kind: canon.KindU8} }
func u16T() canon.Type { return canon.Type{Kind: canon.KindU16} }
func u32T() canon.Type { return canon.Type{Kind: canon.KindU32} }

func ipv4SocketAddressType() canon.Type {
	return canon.RecordType(canon.Field{Name: "port", Type: u16T()},
		canon.Field{Name: "address", Type: canon.TupleType(u8T(), u8T(), u8T(), u8T())})
}

func ipv6SocketAddressType() canon.Type {
	u := u16T()
	return canon.RecordType(canon.Field{Name: "port", Type: u16T()}, canon.Field{Name: "flow-info", Type: u32T()},
		canon.Field{Name: "address", Type: canon.TupleType(u, u, u, u, u, u, u, u)},
		canon.Field{Name: "scope-id", Type: u32T()})
}

func ipSocketAddressType() canon.Type {
	v4, v6 := ipv4SocketAddressType(), ipv6SocketAddressType()
	return canon.VariantType(canon.Case{Name: "ipv4", Type: &v4}, canon.Case{Name: "ipv6", Type: &v6})
}

// socketAddressValue builds an `ip-socket-address`.
func socketAddressValue(ap netip.AddrPort) (canon.Value, error) {
	tuple := func(t canon.Type, vs ...canon.Value) (canon.Value, error) {
		m := make(map[string]canon.Value, len(vs))
		for i, v := range vs {
			m[strconv.Itoa(i)] = v
		}
		return canon.Record(t, m)
	}
	a := ap.Addr()
	if a.Is4() {
		b := a.As4()
		addr, err := tuple(canon.TupleType(u8T(), u8T(), u8T(), u8T()), canon.U8(b[0]), canon.U8(b[1]), canon.U8(b[2]), canon.U8(b[3]))
		if err != nil {
			return canon.Value{}, err
		}
		rec, err := canon.Record(ipv4SocketAddressType(), map[string]canon.Value{"port": canon.U16(ap.Port()), "address": addr})
		if err != nil {
			return canon.Value{}, err
		}
		return canon.Variant(ipSocketAddressType(), "ipv4", &rec)
	}
	b := a.As16()
	ws := make([]canon.Value, 8)
	ts := make([]canon.Type, 8)
	for i := range 8 {
		ws[i], ts[i] = canon.U16(binary.BigEndian.Uint16(b[2*i:])), u16T()
	}
	addr, err := tuple(canon.TupleType(ts...), ws...)
	if err != nil {
		return canon.Value{}, err
	}
	scope, _ := strconv.ParseUint(a.Zone(), 10, 32) //nolint:errcheck // a named zone has no scope id to report; 0
	rec, err := canon.Record(ipv6SocketAddressType(), map[string]canon.Value{
		"port": canon.U16(ap.Port()), "flow-info": canon.U32(0), "address": addr, "scope-id": canon.U32(uint32(scope)),
	})
	if err != nil {
		return canon.Value{}, err
	}
	return canon.Variant(ipSocketAddressType(), "ipv6", &rec)
}

// decodeSocketAddress reads an `ip-socket-address` from its 12 flat words: the discriminant, then the case's
// fields as def:flatten_type lays them out, every word an i32 (ipv4: port, four octets; ipv6: port, flow-info,
// eight segments, scope-id).
func decodeSocketAddress(w []interp.Value) (family int, ap netip.AddrPort, err error) {
	if len(w) != 12 {
		return 0, netip.AddrPort{}, fmt.Errorf("component: tcp-socket.connect: an ip-socket-address is 12 flat words, got %d", len(w))
	}
	word := func(i int) uint32 { return uint32(w[i].Bits) }
	switch word(0) {
	case familyIPv4:
		a := netip.AddrFrom4([4]byte{byte(word(2)), byte(word(3)), byte(word(4)), byte(word(5))})
		return familyIPv4, netip.AddrPortFrom(a, uint16(word(1))), nil
	case familyIPv6:
		var b [16]byte
		for i := range 8 {
			binary.BigEndian.PutUint16(b[2*i:], uint16(word(3+i)))
		}
		a := netip.AddrFrom16(b)
		if s := word(11); s != 0 {
			a = a.WithZone(strconv.FormatUint(uint64(s), 10))
		}
		return familyIPv6, netip.AddrPortFrom(a, uint16(word(1))), nil
	default:
		return 0, netip.AddrPort{}, &interp.Trap{Reason: fmt.Sprintf("tcp-socket.connect: ip-socket-address case %d out of range", word(0))}
	}
}

// sockErrorLabel maps a Go network error to its `error-code` case (ADR 0123), by errno; anything unrecognised is
// `other`. A dial's own timeout and a deadline are `timeout`.
func sockErrorLabel(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if l, ok := sockErrnoLabels[errno]; ok {
			return l
		}
	}
	var ne net.Error
	if errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return "timeout"
	}
	return "other"
}

// sockErrnoLabels is the POSIX errnos' mapping, with the platform's own numbers added (hostsock_windows.go).
var sockErrnoLabels = func() map[syscall.Errno]string {
	m := map[syscall.Errno]string{
		syscall.ECONNREFUSED:  "connection-refused",
		syscall.ECONNRESET:    "connection-reset",
		syscall.ECONNABORTED:  "connection-aborted",
		syscall.EPIPE:         "connection-broken",
		syscall.ETIMEDOUT:     "timeout",
		syscall.EHOSTUNREACH:  "remote-unreachable",
		syscall.ENETUNREACH:   "remote-unreachable",
		syscall.EADDRINUSE:    "address-in-use",
		syscall.EADDRNOTAVAIL: "address-in-use",
		syscall.EACCES:        "access-denied",
		syscall.EPERM:         "access-denied",
	}
	for k, v := range platformErrnoLabels {
		m[k] = v
	}
	return m
}()

// sockErrResult is a failed Read's or Write's future value: `err(code)` at the futures' payload type.
func sockErrResult(err error) canon.Value { return sockErr(nil, sockErrorLabel(err)) }

// tcpCreate is `[static]tcp-socket.create: func(address-family) -> result<tcp-socket, error-code>`, flattened
// to (family, ret). **With the network not granted it is `err(access-denied)`** (ADR 0123): wasmtime 49.0.3's
// reading with no network flag, measured, and the one switch Scott's stamp mirrors.
//
// NEUTER: drop the grant check, and TestTheNetworkIsDeniedAtCreateWithoutTheGrant's create returns ok with a handle.
// TestP3TcpMatchesWasmtime's denied rows pass under it: connect's own check answers access-denied, and std maps
// both to PermissionDenied.
func (h *Host) tcpCreate(res *hostResources) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("component: tcp-socket.create: got %d core args, want 2 (address-family, ret)", len(args))
		}
		fam := uint32(args[0].Bits)
		if fam > familyIPv6 {
			return nil, &interp.Trap{Reason: fmt.Sprintf("tcp-socket.create: ip-address-family case %d out of range", fam)}
		}
		own := canon.OwnType(tcpSocketResource.rt)
		ret := int(uint32(args[1].Bits))
		if !h.InheritNetwork {
			return nil, canon.StoreVia(guestHeap{c}, sockErr(&own, "access-denied"), ret)
		}
		idx := res.mint(tcpSocketResource, &tcpSocket{net: res.net, family: int(fam)})
		handle := canon.Own(tcpSocketResource.rt, uint32(idx))
		v, err := canon.Variant(sockResultType(&own), "ok", &handle)
		return nil, lowerResult(c, uint64(uint32(ret)), v, err)
	}
}

// tcpConnect is `[method]tcp-socket.connect: async func(remote-address: ip-socket-address) -> result<_,
// error-code>`. Its 13 flat parameters spill, and the lower loads them flat (ADR 0123): self, then the address's
// 12 words. self is lent for the subtask's life: released when the dial resolves, is cancelled, or Close stops
// it.
//
// The dial runs on a goroutine of its own, the one dial site, authorised by ADR 0123 in the census
// (TestEveryEngineGoroutineIsAtASiteADecisionAuthorises). It runs no guest code, and its result reaches the
// guest through onResolve, ADR 0101's discipline. It ends when DialContext returns, which a cancel or Close
// makes prompt.
func (h *Host) tcpConnect(async *asyncHandles, res *hostResources) asyncLowerImpl {
	return func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
		args := onStart()
		if len(args) != 13 {
			return nil, fmt.Errorf("component: tcp-socket.connect: got %d flat parameters, want 13 (self, remote-address)", len(args))
		}
		sock, release, err := lendAs[*tcpSocket](res, tcpSocketResource, args[0].Int32())
		if err != nil {
			return nil, err
		}
		fam, addr, err := decodeSocketAddress(args[1:])
		if err != nil {
			release()
			return nil, err
		}
		// An error the WIT names is the subtask's result, resolved inline: RETURNED, with no dial.
		fail := func(label string) (func(), error) {
			release()
			onResolve(sockErr(nil, label))
			return nil, nil
		}
		if !h.InheritNetwork {
			return fail("access-denied") // unreachable while create refuses: the grant is checked at both
		}
		sock.mu.Lock()
		state, sfam := sock.state, sock.family
		if state == tcpUnbound {
			sock.state = tcpConnecting
		}
		sock.mu.Unlock()
		if state != tcpUnbound {
			return fail("invalid-state")
		}
		ip := addr.Addr()
		if fam != sfam || addr.Port() == 0 || ip.IsUnspecified() || ip.Is4In6() || ip.IsMulticast() {
			sock.mu.Lock()
			sock.state = tcpUnbound // an argument refused before any attempt leaves the socket as it was
			sock.mu.Unlock()
			return fail("invalid-argument")
		}
		network := "tcp4"
		if fam == familyIPv6 {
			network = "tcp6"
		}
		ctx, cancel := context.WithCancel(context.Background())
		d := &sockDial{h: async, sock: sock, cancel: cancel, release: release, resolve: onResolve}
		ended, ok := res.net.startDial(d)
		if !ok {
			cancel()
			return fail("invalid-state") // Close has stopped the instance's sockets
		}
		async.mu.Lock()
		async.sockPending++ // a waker for the park bound until the dial's resolution is stored
		async.mu.Unlock()
		dial := h.dial
		if dial == nil {
			var dialer net.Dialer
			dial = dialer.DialContext
		}
		go func() {
			defer ended()
			c, derr := dial(ctx, network, addr.String())
			d.finish(res.net.claim(d), c, derr)
		}()
		return d.cancelSubtask(res.net), nil
	}
}

// finish is the dial's return. Unclaimed, the guest's cancel or Close resolved it already, and a connection
// that arrived anyway is closed. Claimed, the socket is connected or closed and the subtask resolves with the
// result.
func (d *sockDial) finish(claimed bool, c net.Conn, derr error) {
	defer d.cancel()
	if !claimed {
		if c != nil {
			_ = c.Close() // nobody holds it
		}
		return
	}
	v := sockOK()
	tc, isTCP := c.(*net.TCPConn)
	switch {
	case derr != nil:
		v = sockErr(nil, sockErrorLabel(derr))
		d.sock.mu.Lock()
		d.sock.state = tcpClosed // "After a failed connection attempt, the socket will be in the closed state"
		d.sock.mu.Unlock()
	case !isTCP:
		_ = c.Close() // unreachable: a tcp dial returns a *net.TCPConn
		v = sockErr(nil, "other")
	default:
		sc, ok := d.sock.net.adopt(tc)
		d.sock.mu.Lock()
		if ok {
			d.sock.state, d.sock.conn = tcpConnected, sc
			d.sock.local, _ = netip.ParseAddrPort(tc.LocalAddr().String())   //nolint:errcheck // a TCP address parses
			d.sock.remote, _ = netip.ParseAddrPort(tc.RemoteAddr().String()) //nolint:errcheck // a TCP address parses
		} else {
			d.sock.state = tcpClosed
			v = sockErr(nil, "invalid-state")
		}
		d.sock.mu.Unlock()
	}
	// NEUTER: never release, and TestP3TcpMatchesWasmtime's guest traps dropping the socket it connected, its
	// handle still lent.
	d.release()
	d.resolve(v)
	d.h.mu.Lock()
	d.h.sockPending--
	d.h.mu.Unlock()
}

// cancelSubtask is the connect subtask's onCancel: it runs as host code on the canceller's goroutine, so its
// CANCELLED resolution is queued and applied before the canceller reads the subtask's state (hostWait.cancel's
// shape). A dial that returned first has resolved it, and the cancel does nothing (#1009).
//
// NEUTER: return without claiming or resolving, and TestACancelledConnectResolvesCancelledAndItsDialEnds's
// cancel reads BLOCKED.
func (d *sockDial) cancelSubtask(n *hostNet) func() {
	return func() {
		if !n.claim(d) {
			return
		}
		d.cancel()
		d.sock.mu.Lock()
		d.sock.state = tcpClosed
		d.sock.mu.Unlock()
		d.release()
		d.resolve(canon.NoResolution())
		d.h.mu.Lock()
		d.h.sockPending--
		d.h.mu.Unlock()
	}
}

// tcpGetLocalAddress and tcpGetRemoteAddress are `get-local-address` and `get-remote-address: func() ->
// result<ip-socket-address, error-code>`, flattened to (self, ret): `invalid-state` until connected.
func (h *Host) tcpGetLocalAddress(_ *hostResources, c *interp.CanonCaller, self *tcpSocket, args []interp.Value) ([]interp.Value, error) {
	return tcpAddressGetter(c, self, args, "get-local-address", func(s *tcpSocket) netip.AddrPort { return s.local })
}

func (h *Host) tcpGetRemoteAddress(_ *hostResources, c *interp.CanonCaller, self *tcpSocket, args []interp.Value) ([]interp.Value, error) {
	return tcpAddressGetter(c, self, args, "get-remote-address", func(s *tcpSocket) netip.AddrPort { return s.remote })
}

func tcpAddressGetter(c *interp.CanonCaller, self *tcpSocket, args []interp.Value, name string, which func(*tcpSocket) netip.AddrPort) ([]interp.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("component: tcp-socket.%s: got %d core args, want 2 (self, ret)", name, len(args)+1)
	}
	ret := uint64(uint32(args[0].Bits))
	addrT := ipSocketAddressType()
	self.mu.Lock()
	connected, ap := self.state == tcpConnected, which(self)
	self.mu.Unlock()
	if !connected {
		return nil, lowerResult(c, ret, sockErr(&addrT, "invalid-state"), nil)
	}
	av, err := socketAddressValue(ap)
	if err != nil {
		return nil, fmt.Errorf("component: tcp-socket.%s: %w", name, err)
	}
	v, verr := canon.Variant(sockResultType(&addrT), "ok", &av)
	return nil, lowerResult(c, ret, v, verr)
}

// tcpGetAddressFamily is `get-address-family: func() -> ip-address-family`, one flat i32.
func (h *Host) tcpGetAddressFamily(_ *hostResources, _ *interp.CanonCaller, self *tcpSocket, _ []interp.Value) ([]interp.Value, error) {
	return []interp.Value{interp.I32(int32(self.family))}, nil
}

// tcpGetIsListening is `get-is-listening: func() -> bool`: false, since E7a never listens.
func (h *Host) tcpGetIsListening(_ *hostResources, _ *interp.CanonCaller, _ *tcpSocket, _ []interp.Value) ([]interp.Value, error) {
	return []interp.Value{interp.I32(0)}, nil
}

// takeStream marks a socket's receive or send taken, and returns its connection with a reference for the stream,
// or nil when the socket is not connected or the stream was taken before: the WIT's `invalid-state`.
func (s *tcpSocket) takeStream(send bool) *sockConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	taken := &s.received
	if send {
		taken = &s.sent
	}
	if s.state != tcpConnected || s.conn == nil || *taken {
		return nil
	}
	if !s.net.retain(s.conn) {
		return nil
	}
	*taken = true
	return s.conn
}

// tcpReceive is `[method]tcp-socket.receive: func() -> tuple<stream<u8>, future<result<_, error-code>>>`,
// flattened to (self, ret): read-via-stream's shape, served by ADR 0122's producer over the connection (ADR
// 0123). The end of input is `ok`, the WIT's graceful shutdown, and any other Read error its code. The guest's
// drop of the stream is `shutdown(SHUT_RD)`. A second receive, or one before connecting, is a finished stream
// and `err(invalid-state)`.
func (h *Host) tcpReceive(async *asyncHandles, res *hostResources) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("component: tcp-socket.receive: got %d core args, want 2 (self, ret)", len(args))
		}
		sock, release, err := lendAs[*tcpSocket](res, tcpSocketResource, args[0].Int32())
		if err != nil {
			return nil, err
		}
		defer release()
		p := &streamProducer{h: async, ok: sockOK(), mapErr: sockErrResult}
		if sc := sock.takeStream(false); sc != nil {
			n := res.net
			p.src, p.track = sc.c, n.track
			// NEUTER: drop the CloseRead, and TestAReceiveDropShutsTheReadHalf's Read waits out its deadline.
			p.onDone = func(readerDropped bool) {
				if readerDropped {
					_ = sc.c.CloseRead() //nolint:errcheck // the stream is gone; nobody reads the shutdown's error
				}
				n.release(sc)
			}
		} else {
			bad := sockErr(nil, "invalid-state")
			p.final = &bad
		}
		async.mu.Lock()
		ri, fi := p.mintLocked()
		async.mu.Unlock()
		var buf [8]byte
		binary.LittleEndian.PutUint32(buf[0:], uint32(ri))
		binary.LittleEndian.PutUint32(buf[4:], uint32(fi))
		if werr := c.Write(uint64(uint32(args[1].Bits)), buf[:]); werr != nil {
			return nil, fmt.Errorf("component: tcp-socket.receive: writing the stream and future handles: %w", werr)
		}
		return nil, nil
	}
}

// sockSend is one send: the guest's stream, read by the host into a bounded buffer, and the writer goroutine that
// drains the buffer to the connection (ADR 0123). Every field is guarded by h.mu.
type sockSend struct {
	h      *asyncHandles
	net    *hostNet
	conn   *sockConn
	reader *readableStreamEnd // the guest's stream, whose host read is this send's
	future *readableFutureEnd
	buf    []byte // written by the guest and not yet taken by a Write
	// inFlight is the bytes a Write has taken and not returned from, which count against the cap. closing is the
	// guest's writer having dropped: drain, then CloseWrite. done is the send finished, or stopped.
	inFlight int
	closing  bool
	done     bool
}

// free is the room the buffer has for a guest write.
func (s *sockSend) free() int { return sendBufCap - len(s.buf) - s.inFlight }

// armLocked re-arms the host's read of the guest's stream at the room the buffer has, or leaves no read pending
// when it is full, so a guest write parks until the writer drains. With room and a guest write parked, the copy
// runs now, reading guest memory, so the caller holds the run slot as well as h.mu.
func (s *sockSend) armLocked() error {
	re := s.reader
	re.resolved, re.delivered, re.progress = false, false, 0
	room := s.free()
	if room <= 0 {
		re.hasRead = false
		return nil
	}
	re.readN, re.hasRead = uint32(room), true
	if w := re.conn; w != nil && w.hasWrite && !w.resolved {
		progress, err := driveStreamCopyLocked(re, w)
		if err != nil {
			return err
		}
		w.completeWriteLocked(progress, copyCompleted)
		re.armLocked(progress, copyCompleted)
	}
	return nil
}

// tcpSend is `[method]tcp-socket.send: func(data: stream<u8>) -> future<result<_, error-code>>`, flattened to
// (self, data) -> future: write-via-stream's shape with a writer goroutine for its sink (ADR 0123). A second
// send, or one before connecting, resolves `err(invalid-state)` inline and unlinks the stream, so the guest's
// writes complete DROPPED.
//
// The writer goroutine is the one send site, authorised by ADR 0123 in the census. It runs no guest code: it
// waits for bytes on the instance's host-I/O condition, writes them with no lock held, and delivers what the
// Write freed under ADR 0101's discipline. It ends when the stream is finished, a Write fails, or Close stops it.
func (h *Host) tcpSend(async *asyncHandles, res *hostResources) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("component: tcp-socket.send: got %d core args, want 2 (self, data)", len(args))
		}
		sock, release, err := lendAs[*tcpSocket](res, tcpSocketResource, args[0].Int32())
		if err != nil {
			return nil, err
		}
		defer release()
		ri := uint32(args[1].Bits)
		sc := sock.takeStream(true)
		async.mu.Lock()
		defer async.mu.Unlock()
		re, ok := handleAt[*readableStreamEnd](async, ri)
		if !ok {
			if sc != nil {
				res.net.release(sc) // under h.mu, the permitted order
			}
			return nil, fmt.Errorf("%w: tcp-socket.send on handle %d that is not a readable stream end", ErrLinkRefused, ri)
		}
		if re.conn != nil && re.conn.producer != nil {
			if sc != nil {
				res.net.release(sc)
			}
			return nil, fmt.Errorf("%w: tcp-socket.send of a stream the host produces; a host-to-host copy is not built "+
				"(ADR 0122)", ErrUnsupportedForm)
		}
		okv := sockOK()
		fe := &readableFutureEnd{payload: &okv}
		fe.index = async.addLocked(fe)
		var ended func()
		if sc != nil {
			if e, tok := res.net.track(); tok {
				ended = e
			} else {
				res.net.release(sc)
				sc = nil
			}
		}
		if sc == nil {
			bad := sockErr(nil, "invalid-state")
			fe.payload = &bad
			unlinkHostReadLocked(re)
			if cerr := fe.completeLocked(copyCompleted); cerr != nil {
				return nil, cerr
			}
			return []interp.Value{interp.I32(int32(fe.index))}, nil
		}
		if async.hostIO == nil {
			async.hostIO = sync.NewCond(&async.mu)
		}
		s := &sockSend{h: async, net: res.net, conn: sc, reader: re, future: fe}
		if re.writerDropped {
			s.closing = true // the guest dropped its writer before sending: nothing to write, then CloseWrite
		} else {
			// NEUTER: write the bytes to the connection here, inline, and TestCloseEndsASocketsGoroutines' send row
			// never parks: its stream.write is blocked in the Write with asyncHandles.mu held, so Close could not take
			// it either. TestP3TcpMatchesWasmtime passes under it, its 100 KiB fitting the kernel's buffers.
			re.hostSink = func(b []byte) error {
				s.buf = append(s.buf, b...)
				async.hostIO.Broadcast()
				return nil
			}
			re.onHostComplete = func(r copyResult) {
				if r == copyCompleted {
					// The copy ran under the lock the caller holds; re-arming here needs no copy of its own, since no
					// write is parked behind the one that just completed.
					re.resolved, re.delivered, re.progress = false, false, 0
					if room := s.free(); room > 0 {
						re.readN = uint32(room)
					} else {
						re.hasRead = false
					}
					return
				}
				s.closing = true // DROPPED: the guest's writer is gone
				async.hostIO.Broadcast()
			}
			re.caller, re.state = c, copyStateCopying
			if err := s.armLocked(); err != nil {
				ended()
				res.net.release(sc)
				return nil, err
			}
		}
		go func() {
			defer ended()
			s.run()
		}()
		return []interp.Value{interp.I32(int32(fe.index))}, nil
	}
}

// unlinkHostReadLocked makes the guest's stream one whose reader has gone, as stream.drop-readable does: a parked
// write completes DROPPED, and the next completes DROPPED inline. The caller holds h.mu.
func unlinkHostReadLocked(re *readableStreamEnd) {
	re.hostSink, re.onHostComplete, re.hasRead = nil, nil, false
	if w := re.conn; w != nil {
		if w.hasWrite && !w.resolved {
			w.completeWriteLocked(0, copyDropped)
		}
		w.readerDropped, w.conn, re.conn = true, nil, nil
	}
}

// run is the writer goroutine's body: wait for bytes, write them with no lock held, deliver what the Write freed,
// and at the writer's drop CloseWrite and resolve the future.
func (s *sockSend) run() {
	h := s.h
	for {
		h.mu.Lock()
		for len(s.buf) == 0 && !s.closing && !h.producersStopped {
			h.hostIO.Wait()
		}
		if h.producersStopped {
			h.mu.Unlock()
			return // Close: it closes the connection
		}
		if len(s.buf) == 0 { // closing, and drained
			h.sockPending++ // a waker until the future's resolution is stored
			h.mu.Unlock()
			// NEUTER: skip the CloseWrite, and TestP3TcpMatchesWasmtime's read_to_end waits for an end of stream the
			// peer never sends, until the hang guard.
			cerr := s.conn.c.CloseWrite()
			s.deliver(func() { s.finishLocked(cerr) })
			return
		}
		chunk := s.buf
		s.buf, s.inFlight = nil, len(chunk)
		h.sockPending++ // a waker while the Write is in flight: its delivery may complete a parked write
		h.mu.Unlock()
		_, werr := s.conn.c.Write(chunk)
		s.deliver(func() {
			s.inFlight = 0
			if werr != nil {
				s.finishLocked(werr)
				return
			}
			if err := s.armLocked(); err != nil {
				s.finishLocked(err)
			}
		})
		if werr != nil {
			return
		}
	}
}

// deliver runs a store under ADR 0101's discipline, as the producer's does: queued for the run slot's holder while
// it is in host code, otherwise under the slot on a chain of its own, and dropped if Close has stopped the table.
// f runs under h.mu; the sockPending the caller raised is lowered whether it runs or not.
func (s *sockSend) deliver(f func()) {
	h := s.h
	apply := func() {
		h.mu.Lock()
		h.sockPending--
		if !h.producersStopped && !s.done {
			f()
		}
		h.mu.Unlock()
	}
	h.mu.Lock()
	if h.producersStopped {
		h.sockPending--
		h.mu.Unlock()
		return
	}
	if h.inHost > 0 {
		h.hostStores = append(h.hostStores, apply)
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	ch := &runChain{}
	if !h.acquireRun(ch, nil, h.producerStop) {
		h.mu.Lock()
		h.sockPending--
		h.mu.Unlock()
		return
	}
	defer h.releaseRunFor(ch)
	apply()
}

// finishLocked ends the send: the future resolves `ok`, or the error's code with the stream unlinked so later
// writes complete DROPPED, and the stream's reference to the connection is released. The caller holds h.mu.
func (s *sockSend) finishLocked(err error) {
	s.done = true
	if err != nil {
		bad := sockErr(nil, sockErrorLabel(err))
		s.future.payload = &bad
		unlinkHostReadLocked(s.reader)
	}
	if cerr := s.future.completeLocked(copyCompleted); cerr != nil {
		s.future.completeErr = cerr // surfaced at the future's drop, as write-via-stream's is
	}
	s.net.release(s.conn)
}
