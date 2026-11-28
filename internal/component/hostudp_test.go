// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"io"
	"net"
	"net/netip"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// udpGuard is the hang guard of a udp.wasm row or a p3udp run: a harness bound, not a measurement, so it is scaled under
// `-race` (raceGuardScale, #1391).
const udpGuard = 20 * time.Second * raceGuardScale

// udpRow is one row of udp.wasm, in udp.wat's order, so its index is its row number. darwin marks a row whose reading
// is darwin's kernel's rather than the engine's (ADR 0140's consequences), and icmp one that needs an ICMP error
// surfaced, which Go's net turns off on Windows (SIO_UDP_CONNRESET). park is the harness row that has no reading.
type udpRow struct {
	name               string
	darwin, icmp, trap bool
	denied, park       bool
}

var udpRows = []udpRow{
	{name: "bind-v4"},
	{name: "bind-v6"},
	{name: "fresh-getters"},
	{name: "bind-refusals"},
	{name: "bind-more"},
	{name: "sendto-v4"},
	{name: "sendto-v6"},
	{name: "connect-send"},
	{name: "connect-filter"},
	{name: "send-none"},
	{name: "send-mismatch"},
	{name: "send-implicit"},
	{name: "connect-implicit"},
	{name: "receive-unbound"},
	{name: "disconnect"},
	{name: "disconnect-unconnected"},
	{name: "disconnect-local", darwin: true},
	{name: "connect-refusals"},
	{name: "reconnect"},
	{name: "empty"},
	{name: "big"},
	{name: "sizes-64k"},
	{name: "rcvbuf-small", darwin: true},
	{name: "refused", icmp: true},
	{name: "hop-limit"},
	{name: "hop-defaults", darwin: true},
	{name: "hop-limit-v6"},
	{name: "rbuf"},
	{name: "sbuf"},
	{name: "buf-huge"},
	{name: "options-carry"},
	{name: "cancel"},
	{name: "blocked"},
	{name: "sync-lowers"},
	{name: "two-datagrams"},
	{name: "dial-v4"},
	{name: "dial-v6"},
	{name: "send-refusals"},
	{name: "broadcast"},
	{name: "denied", denied: true},
	{name: "park", park: true},
	{name: "max-v4"},
	{name: "max-v6"},
	{name: "send-oob", trap: true},
	{name: "connect-any"},
	{name: "disconnect-receive"},
	{name: "queued-status"},
	{name: "connect-after-arrival"},
	{name: "kernel-maxdgram", darwin: true},
	// Portable since the Linux disconnect binds a port its kernel dropped again (hostudp_disconnect_linux.go): its first
	// draft was darwin's, before CI's first Linux run.
	{name: "disconnect-any"},
}

// udpRowIndex is a row's number by its name.
func udpRowIndex(t *testing.T, name string) int {
	t.Helper()
	for i, r := range udpRows {
		if r.name == name {
			return i
		}
	}
	t.Fatalf("udp.wasm has no row %q", name)
	return -1
}

// startUDP instantiates udp.wasm on a Host granted the network or not, and calls run(row) on a goroutine. The caller
// reads the result from the channels, or closes the instance.
func startUDP(t *testing.T, row int, grant bool) (*Instantiated, <-chan []canon.Value, <-chan error) {
	t.Helper()
	b, err := os.ReadFile("testdata/udp.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	h.InheritNetwork = grant
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	res, errc := make(chan []canon.Value, 1), make(chan error, 1)
	go func() {
		r, e := in.CallValues("run", canon.U32(uint32(row)))
		if e != nil {
			errc <- e
			return
		}
		res <- r
	}()
	return in, res, errc
}

// TestUDPMatchesWasmtime is E3 at the ABI (ADR 0140): every row of udp.wasm against udp.reading, each on a fresh
// instance, with gate:async on and gate:cm-threads off and on. The reading is wasmtime 49.0.3's (10 of 10 per row) but
// for the two rows where Burroughs departs by ADR 0140, cancel and connect-after-arrival, whose wasmtime readings the
// file records beside them; connect-after-arrival's is the WIT's text. The denied row is read on a Host without the
// grant, as wasmtime's was read with no network flag. After each row's Close no socket goroutine is left running.
//
// **Rows that read the kernel are asked on darwin only,** and logged elsewhere: on another kernel Burroughs asks that
// kernel, as wasmtime would, and the committed reading is darwin's. The refused row is not asked on Windows, where Go's
// net turns ICMP errors off.
func TestUDPMatchesWasmtime(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	for _, gate := range []string{"0", "1"} {
		t.Run("cm-threads="+gate, func(t *testing.T) {
			t.Setenv(cmThreadsGateEnv, gate)
			for i, row := range udpRows {
				switch {
				case row.park:
					continue
				case row.darwin && runtime.GOOS != "darwin":
					t.Logf("%s: not asked on %s (its reading is darwin's kernel's)", row.name, runtime.GOOS)
					continue
				case row.icmp && runtime.GOOS == "windows":
					t.Logf("%s: not asked on windows (Go's net disables SIO_UDP_CONNRESET, so no ICMP error arrives)", row.name)
					continue
				}
				t.Run(row.name, func(t *testing.T) {
					want := lineAfter(t, "testdata/udp.reading", row.name+": ")
					in, res, errc := startUDP(t, i, !row.denied)
					// A row that fails its hang guard still closes its instance, so its reader does not outlive it into
					// the next row's goroutine check.
					closed := false
					defer func() {
						if !closed {
							in.Close()
						}
					}()
					select {
					case r := <-res:
						v, _ := r[0].U32()
						if row.trap {
							t.Errorf("run(%d, %s) = %d; want a trap (%s)", i, row.name, v, want)
						} else if got := strconv.FormatUint(uint64(v), 10); got != want {
							t.Errorf("run(%d, %s) = %s (%#08x), want %s", i, row.name, got, v, want)
						}
					case e := <-errc:
						if !row.trap || !strings.HasPrefix(want, "trap") || !isTrap(e) {
							t.Errorf("run(%d, %s) = %v; want %s", i, row.name, e, want)
						}
					case <-time.After(udpGuard):
						t.Fatalf("run(%d, %s) neither returned nor trapped within the %v hang guard", i, row.name, udpGuard)
					}
					in.Close()
					closed = true
					requireNoSocketGoroutines(t)
				})
			}
		})
	}
}

// udpReaderRunning reports a UDP socket of the instance whose reader is running with a receive pending.
func udpReaderRunning(res *hostResources) bool {
	res.net.mu.Lock()
	socks := make([]*udpSocket, 0, len(res.net.udps))
	for s := range res.net.udps {
		socks = append(socks, s)
	}
	res.net.mu.Unlock()
	for _, s := range socks {
		s.mu.Lock()
		ok := s.reading && len(s.pending) > 0
		s.mu.Unlock()
		if ok {
			return true
		}
	}
	return false
}

// TestCloseEndsAGuestParkedInUDPReceive is Close's UDP rule (ADR 0140), run under -race too: udp.wasm's park row
// receives on a bound socket nobody sends to, so the task parks in waitable-set.wait with the socket's reader blocked in
// ReadFrom. Close returns, the run ends, and the reader is gone: Close closes the socket, so the ReadFrom returns, and
// waits it out.
//
// NEUTER, run: hostNet.stop without the UDP sockets' stop, and Close does not return within the hang guard, waiting for
// the reader, whose socket the pending receive keeps lent, so Close's destructor of every own leaves it open.
func TestCloseEndsAGuestParkedInUDPReceive(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	in, res, errc := startUDP(t, udpRowIndex(t, "park"), true)
	awaitPlace(t, in.w.async, "a guest receive with the socket's reader blocked in ReadFrom", func() {}, func() bool {
		return udpReaderRunning(in.w.hostRes)
	})
	closed := make(chan struct{})
	go func() { in.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(udpGuard):
		t.Fatalf("Close did not return within the %v hang guard with a guest parked in receive", udpGuard)
	}
	select {
	case <-res:
	case <-errc:
	case <-time.After(udpGuard):
		t.Fatalf("the run did not end within %v of Close", udpGuard)
	}
	requireNoSocketGoroutines(t)
	in.w.async.mu.Lock()
	pending := in.w.async.sockPending
	in.w.async.mu.Unlock()
	if pending != 0 {
		t.Errorf("sockPending = %d after Close, want 0: a dropped receive still counted as a waker", pending)
	}
}

// udpHarness drives the UDP host functions directly, sockHarness's shape: one async table, one resource table, one
// test caller with a page of memory, and a granted Host.
type udpHarness struct{ sockHarness }

func newUDPHarness(t *testing.T) udpHarness {
	t.Helper()
	return udpHarness{newSockHarness(t)}
}

// create is `udp-socket.create(fam)`, returning the own's index.
func (u udpHarness) create(t *testing.T, fam int) int32 {
	t.Helper()
	if _, err := u.host.udpCreate(u.res)(u.cc, []interp.Value{interp.I32(int32(fam)), interp.I32(0)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if b, _ := u.cc.Read(0, 1); b[0] != 0 {
		t.Fatalf("create returned err, case %d", u.word(t, 4)&0xff)
	}
	return int32(u.word(t, 4))
}

// addrWords is ap's 12 flat words, as bind and connect take them.
func addrWords(ap netip.AddrPort) []interp.Value {
	w := make([]interp.Value, 12)
	for i := range w {
		w[i] = interp.I32(0)
	}
	a := ap.Addr()
	if a.Is4() {
		b := a.As4()
		w[1] = interp.I32(int32(ap.Port()))
		for i := range 4 {
			w[2+i] = interp.I32(int32(b[i]))
		}
		return w
	}
	b := a.As16()
	w[0], w[1] = interp.I32(familyIPv6), interp.I32(int32(ap.Port()))
	for i := range 8 {
		w[3+i] = interp.I32(int32(uint16(b[2*i])<<8 | uint16(b[2*i+1])))
	}
	return w
}

// call runs a sync method with self and the rest, returning the case byte at ret (0 ok, 1 err) and the error's case.
func (u udpHarness) call(t *testing.T, m resourceMethod, self int32, rest ...interp.Value) (isErr bool, code byte) {
	t.Helper()
	args := append([]interp.Value{interp.I32(self)}, rest...)
	args = append(args, interp.I32(512))
	if _, err := m(u.res)(u.cc, args); err != nil {
		t.Fatalf("method: %v", err)
	}
	b, _ := u.cc.Read(512, 5)
	return b[0] != 0, b[4]
}

// bound creates an ipv4 socket bound to 127.0.0.1:0 and returns it with its local address.
func (u udpHarness) bound(t *testing.T) (int32, netip.AddrPort) {
	t.Helper()
	s := u.create(t, familyIPv4)
	if isErr, code := u.call(t, method(udpSocketResource, u.host.udpBind), s, addrWords(netip.MustParseAddrPort("127.0.0.1:0"))...); isErr {
		t.Fatalf("bind: case %d", code)
	}
	obj, release, err := lendAs[*udpSocket](u.res, udpSocketResource, s)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	obj.mu.Lock()
	ap, err := osLocalAddr(obj.rc)
	obj.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return s, ap
}

// receive calls receive through the async lower, its result at 256, and returns the lower's packed status.
func (u udpHarness) receive(t *testing.T, s int32) uint32 {
	t.Helper()
	ret, err := asyncLowerFunc(u.host.udpReceive(u.h, u.res), true, u.h)(u.cc, []interp.Value{interp.I32(s), interp.I32(256)})
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	return uint32(ret[0].Int32())
}

// received is the datagram the last receive stored at 256: its bytes, or nil with the error's case.
func (u udpHarness) received(t *testing.T) ([]byte, byte) {
	t.Helper()
	b, _ := u.cc.Read(256, 12)
	if b[0] != 0 {
		return nil, b[4]
	}
	ptr, n := u.word(t, 260), u.word(t, 264)
	data, err := u.cc.Read(uint64(ptr), uint64(n))
	if err != nil {
		t.Fatal(err)
	}
	return data, 0
}

// TestEveryUDPOperationChecksTheGrant is the grant's off state past create (ADR 0140): a socket created while the
// network was granted, the grant then withdrawn, answers access-denied at bind, connect, send and receive. With the
// grant off from the start create refuses first (the fixture's denied row), so these checks are unreachable from a
// guest; this is their witness.
//
// NEUTER, run: the four checks dropped in one run, and each of the four assertions failed on its own operation: bind
// and connect ok, send "" (sent), and receive STARTED (0x11), pending. TestUDPMatchesWasmtime and
// TestP3UdpMatchesWasmtime pass under it, every check past create unreachable from a guest that create refused. The
// first draft's bind asked for port 9, and its assertion passed under the neuter on darwin, whose kernel answered that
// bind access-denied itself, so the bind now asks for port 0.
func TestEveryUDPOperationChecksTheGrant(t *testing.T) {
	u := newUDPHarness(t)
	s := u.create(t, familyIPv4)
	// The bind asks for port 0: a port below 1024 is the kernel's own access-denied on darwin, which would answer the
	// assertion whether or not the grant was checked.
	local, peer := netip.MustParseAddrPort("127.0.0.1:0"), netip.MustParseAddrPort("127.0.0.1:9")
	u.host.InheritNetwork = false
	for _, c := range []struct {
		name string
		m    resourceMethod
		ap   netip.AddrPort
	}{
		{"bind", method(udpSocketResource, u.host.udpBind), local},
		{"connect", method(udpSocketResource, u.host.udpConnect), peer},
	} {
		if isErr, code := u.call(t, c.m, s, addrWords(c.ap)...); !isErr || code != 0 {
			t.Errorf("%s with the network withdrawn = err %v, case %d; want access-denied (0)", c.name, isErr, code)
		}
	}
	obj, release, err := lendAs[*udpSocket](u.res, udpSocketResource, s)
	if err != nil {
		t.Fatal(err)
	}
	if l := obj.send(u.host, []byte("x"), true, familyIPv4, peer); l != "access-denied" {
		t.Errorf("send with the network withdrawn = %q, want access-denied", l)
	}
	release()
	if st := u.receive(t, s); st != uint32(subtaskReturned) {
		t.Fatalf("receive with the network withdrawn returned %#x, want RETURNED inline", st)
	}
	if _, code := u.received(t); code != 0 {
		t.Errorf("receive with the network withdrawn = case %d, want access-denied (0)", code)
	}
}

// TestEveryUDPSocketHasNoBroadcast is configure's SO_BROADCAST rule (ADR 0140), asked of the socket rather than of a
// send: Go's net sets SO_BROADCAST on every UDP socket, wasmtime's socket has none, and no WASI option grants it, so
// every OS socket a bind makes, explicit or implicit, has it cleared. (A probe's socket, which sends nothing, keeps Go's
// default.) The fixture's broadcast row is the wasmtime comparison; this witness sends nothing, so its neuter puts no
// datagram on the network.
//
// NEUTER, run: configure setting SO_BROADCAST rather than clearing it, Go's net's default, and both of this test's
// sockets read it set (32, darwin's SO_BROADCAST bit).
func TestEveryUDPSocketHasNoBroadcast(t *testing.T) {
	u := newUDPHarness(t)
	explicit, _ := u.bound(t)
	implicit := u.create(t, familyIPv4)
	if isErr, code := u.call(t, method(udpSocketResource, u.host.udpConnect), implicit, addrWords(netip.MustParseAddrPort("127.0.0.1:9"))...); isErr {
		t.Fatalf("connect: case %d", code)
	}
	for _, s := range []struct {
		name string
		h    int32
	}{{"bound", explicit}, {"implicitly bound", implicit}} {
		obj, release, err := lendAs[*udpSocket](u.res, udpSocketResource, s.h)
		if err != nil {
			t.Fatal(err)
		}
		obj.mu.Lock()
		v, err := osGetInt(obj.rc, udpBroadcastLevel, udpBroadcastName)
		obj.mu.Unlock()
		release()
		if err != nil {
			t.Fatalf("%s: getsockopt: %v", s.name, err)
		}
		if v != 0 {
			t.Errorf("the %s socket has SO_BROADCAST %d, want 0: a send to a broadcast address would leave the host", s.name, v)
		}
	}
}

// TestAHeldDatagramFromAnotherIsDroppedAtConnect is connect's half of the WIT's receive promise (ADR 0140): a datagram
// the reader read with no receive waiting, from C, is held; B then connects to A, and the held datagram is dropped,
// so B's receive is A's datagram. The fixture cannot reach this: wasmtime's guest-visible API holds no datagram.
//
// NEUTER, run: connect without dropHeldFromOthersLocked, and the receive is C's datagram.
func TestAHeldDatagramFromAnotherIsDroppedAtConnect(t *testing.T) {
	u := newUDPHarness(t)
	b, bAddr := u.bound(t)
	// A and C are the test's own sockets, so their sends need no guest.
	cConn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer cConn.Close()
	aConn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer aConn.Close()
	// A receive cancelled at once leaves the reader started for it to hold the next datagram, C's.
	cancel, _ := u.receiveDirect(t, b)
	cancel()
	obj, release, err := lendAs[*udpSocket](u.res, udpSocketResource, b)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := cConn.WriteToUDPAddrPort([]byte("from C"), bAddr); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(udpGuard)
	for {
		obj.mu.Lock()
		held := len(obj.held)
		obj.mu.Unlock()
		if held == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("C's datagram was not held within the %v hang guard", udpGuard)
		}
		runtime.Gosched()
	}
	if isErr, code := u.call(t, method(udpSocketResource, u.host.udpConnect), b, addrWords(aConn.LocalAddr().(*net.UDPAddr).AddrPort())...); isErr {
		t.Fatalf("connect: case %d", code)
	}
	if _, err := aConn.WriteToUDPAddrPort([]byte("from A"), bAddr); err != nil {
		t.Fatal(err)
	}
	_, got := u.receiveDirect(t, b)
	want := udpReceiveValue(udpDatagram{data: []byte("from A"), from: aConn.LocalAddr().(*net.UDPAddr).AddrPort()})
	fromC := udpReceiveValue(udpDatagram{data: []byte("from C"), from: cConn.LocalAddr().(*net.UDPAddr).AddrPort()})
	switch v := u.awaitValue(t, got); {
	case reflect.DeepEqual(v, fromC):
		t.Error("B's receive after connecting to A is C's datagram: a held datagram from another survived the connect")
	case !reflect.DeepEqual(v, want):
		t.Errorf("B's receive after connecting to A = %v, want A's datagram", v)
	}
}

// receiveDirect starts a receive at udpReceive's impl, with onResolve capturing the value rather than the async
// lower storing it: the harness's caller has no realloc for a list. It returns the receive's onCancel (nil when it
// resolved at once) and the channel its value arrives on.
func (u udpHarness) receiveDirect(t *testing.T, s int32) (cancel func(), got <-chan canon.Value) {
	t.Helper()
	ch := make(chan canon.Value, 1)
	cancel, err := u.host.udpReceive(u.h, u.res)(u.cc, func() []interp.Value { return []interp.Value{interp.I32(s)} },
		func(v canon.Value) { ch <- v })
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	return cancel, ch
}

// awaitValue is a receive's value, within the hang guard.
func (u udpHarness) awaitValue(t *testing.T, got <-chan canon.Value) canon.Value {
	t.Helper()
	select {
	case v := <-got:
		return v
	case <-time.After(udpGuard):
		t.Fatalf("the receive did not resolve within the %v hang guard", udpGuard)
		return canon.Value{}
	}
}

// TestACancelledReceiveTakesNothing is the reader's held datagram (ADR 0140): a receive is cancelled once its reader
// has started, a datagram then arrives, the reader holds it with no receive waiting, and the next receive is that
// datagram, at once. The fixture's cancel row asks the same, but its next receive usually reaches the socket before the
// reader has read, and is then the reader's own delivery; this test waits until the datagram is held.
//
// NEUTER, run: receive skipping the held datagram, and the next receive waits until the hang guard, the datagram held
// and never delivered. TestUDPMatchesWasmtime's cancel row passes under it, its next receive reaching the socket first.
func TestACancelledReceiveTakesNothing(t *testing.T) {
	u := newUDPHarness(t)
	b, bAddr := u.bound(t)
	from, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer from.Close()
	cancel, _ := u.receiveDirect(t, b)
	if cancel == nil {
		t.Fatal("the receive resolved at once with nothing sent")
	}
	cancel()
	obj, release, err := lendAs[*udpSocket](u.res, udpSocketResource, b)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := from.WriteToUDPAddrPort([]byte("late"), bAddr); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(udpGuard)
	for {
		obj.mu.Lock()
		held := len(obj.held)
		obj.mu.Unlock()
		if held == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the datagram was not held within the %v hang guard", udpGuard)
		}
		runtime.Gosched()
	}
	_, got := u.receiveDirect(t, b)
	if v := u.awaitValue(t, got); !reflect.DeepEqual(v, udpReceiveValue(udpDatagram{data: []byte("late"), from: from.LocalAddr().(*net.UDPAddr).AddrPort()})) {
		t.Errorf("the receive after the cancel = %v, want the datagram the cancelled one's reader held", v)
	}
}

// TestReceivesAreServedInOrder is the reader's queue (ADR 0140): two receives pending on one socket, then two
// datagrams; the first receive gets the first datagram.
//
// NEUTER, run: hand each datagram to the last pending receive, and each receive gets the other's datagram. This is the
// order's only witness: no fixture row has two receives pending.
func TestReceivesAreServedInOrder(t *testing.T) {
	u := newUDPHarness(t)
	b, bAddr := u.bound(t)
	from, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer from.Close()
	_, first := u.receiveDirect(t, b)
	_, second := u.receiveDirect(t, b)
	for _, msg := range []string{"first", "second"} {
		if _, err := from.WriteToUDPAddrPort([]byte(msg), bAddr); err != nil {
			t.Fatal(err)
		}
	}
	sender := from.LocalAddr().(*net.UDPAddr).AddrPort()
	datagram := func(msg string) canon.Value { return udpReceiveValue(udpDatagram{data: []byte(msg), from: sender}) }
	for i, c := range []struct {
		got         <-chan canon.Value
		want, other string
	}{{first, "first", "second"}, {second, "second", "first"}} {
		switch v := u.awaitValue(t, c.got); {
		case reflect.DeepEqual(v, datagram(c.want)):
		case reflect.DeepEqual(v, datagram(c.other)):
			t.Errorf("receive %d got the %s datagram, want the %s, in the order the receives were made", i, c.other, c.want)
		default:
			t.Errorf("receive %d got neither datagram", i)
		}
	}
}

// TestP3UdpMatchesWasmtime is E3's std witness (ADR 0140): an ordinary Rust wasm32-wasip3 program, with no bindings and
// one thread, uses std's UdpSocket over IPv4 and IPv6 loopback: bind, local_addr, send_to and recv_from, connect, send,
// recv and peer_addr, an empty, an 8192-byte and a truncated datagram, set_ttl, a closed port's ConnectionRefused, a
// read timeout, and a nonblocking recv. Its stdout, its empty stderr and its exit code must equal wasmtime 49.0.3's,
// byte for byte, in both gate states: granted, `testdata/p3udp.reading`; denied, `testdata/p3udp-denied.reading`,
// `bind: PermissionDenied`, which is create's access-denied. After Close no socket goroutine is left running.
//
// **The granted run is not asked on Windows**, and is logged: Go's net disables SIO_UDP_CONNRESET there, so the closed
// port's receive waits for an ICMP error that never arrives. The denied run is asked everywhere.
func TestP3UdpMatchesWasmtime(t *testing.T) {
	b, err := os.ReadFile("testdata/p3udp/p3udp.wasm")
	if err != nil {
		t.Fatalf("p3udp fixture missing (it is committed): %v", err)
	}
	for _, c := range []struct {
		name    string
		grant   bool
		reading string
	}{
		{"granted", true, "testdata/p3udp.reading"},
		{"denied", false, "testdata/p3udp-denied.reading"},
	} {
		if c.grant && runtime.GOOS == "windows" {
			t.Logf("%s: not asked on windows (Go's net disables SIO_UDP_CONNRESET, so the closed-port line's receive waits)", c.name)
			continue
		}
		want, wantExit := readP3CatReading(t, c.reading)
		for _, gate := range []string{"0", "1"} {
			t.Run(c.name+"/cm-threads="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				var out, errb bytes.Buffer
				h := NewHost(&out, &errb, nil)
				h.Args = []string{"p3udp.wasm"}
				h.InheritNetwork = c.grant
				in, err := InstantiateWithHost(b, h)
				if err != nil {
					t.Fatalf("p3udp did not instantiate: %v", err)
				}
				type result struct {
					code int
					err  error
				}
				done := make(chan result, 1)
				go func() { c, e := in.CallRun(); done <- result{c, e} }()
				var code int
				var rerr error
				select {
				case r := <-done:
					code, rerr = r.code, r.err
				case <-time.After(udpGuard):
					in.Close()
					t.Fatalf("p3udp did not exit within the %v hang guard", udpGuard)
				}
				in.Close()
				if rerr != nil {
					t.Fatalf("p3udp did not run to its exit: %v\nstdout so far: %q\nstderr: %q", rerr, out.String(), errb.String())
				}
				if got := out.String(); got != want {
					t.Errorf("stdout differs from wasmtime 49.0.3's reading.\n got: %q\nwant: %q", got, want)
				}
				if errb.Len() != 0 {
					t.Errorf("stderr = %q, want nothing, as wasmtime writes", errb.String())
				}
				if code != wantExit {
					t.Errorf("exit code = %d, want wasmtime's %d", code, wantExit)
				}
				requireNoSocketGoroutines(t)
			})
		}
	}
}
