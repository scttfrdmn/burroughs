// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// tcpPeer is a test's TCP peer on 127.0.0.1: a listener, the port of a listener it bound and closed (a connect to it
// is refused), and what it does with each connection it accepts.
type tcpPeer struct {
	ln     net.Listener
	port   int
	closed int
	mu     sync.Mutex
	conns  []net.Conn
	gotEOF chan struct{} // closed when a drain peer has read its connection to the end
}

// The peers' behaviours: echo is build.sh's (echo until the peer's write half shuts, then close), drain reads to the
// end and then holds the connection open without writing, and silent accepts and never reads.
const (
	peerEcho = iota
	peerDrain
	peerSilent
)

func startPeer(t *testing.T, mode int) *tcpPeer {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gone, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	p := &tcpPeer{ln: ln, port: ln.Addr().(*net.TCPAddr).Port, closed: gone.Addr().(*net.TCPAddr).Port, gotEOF: make(chan struct{})}
	_ = gone.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			p.conns = append(p.conns, c)
			p.mu.Unlock()
			switch mode {
			case peerEcho:
				go func() {
					defer c.Close()
					_, _ = io.Copy(c, c)
				}()
			case peerDrain:
				go func() {
					_, _ = io.Copy(io.Discard, c)
					close(p.gotEOF)
				}()
			default:
				// A silent peer holds as little as the kernel allows, so a writer to it blocks on the guest's bytes
				// rather than on how far this runner's kernel autotunes its buffers (see smallSendBufferDial).
				if tc, ok := c.(*net.TCPConn); ok {
					_ = tc.SetReadBuffer(smallSocketBuffer)
				}
			}
		}
	}()
	t.Cleanup(p.shut)
	return p
}

// smallSocketBuffer is the socket buffer size the send-blocking witnesses ask for. The kernel rounds it up to its
// own minimum, a few KiB on Linux, darwin and Windows, which is the point: far below the volume the guest writes.
const smallSocketBuffer = 4096

// smallSendBufferDial is the dial seam for a witness that needs a guest's send to block: the connection's send
// buffer is shrunk as small as the kernel allows.
//
// **Why it exists:** the send row of TestCloseEndsASocketsGoroutines needs the guest's write to park, which needs
// the kernel's buffers full. It wrote 4 MiB on the premise that this was more than the buffers hold, a size the
// runner chooses. PR #1280's first CI run failed the row on all four runners. The cause **measured** is the
// volume's other cost: the guest builds its payload in an interpreted loop before its first write, and under
// `-race`, with the whole package in parallel, building 4 MiB took most of the 10 s hang guard (reproduced in
// `make ci` locally, where the row alone passes). Buffers autotuned past 4 MiB on CI is plausible and not measured.
// Shrinking both ends' buffers retires both: the premise is now the guest's volume against a few KiB of kernel
// buffer and the host's 64 KiB, so a 512 KiB volume is ample and an eighth of the build.
func smallSendBufferDial(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetWriteBuffer(smallSocketBuffer)
	}
	return c, nil
}

// shut closes the peer's listener and every connection it accepted.
func (p *tcpPeer) shut() {
	_ = p.ln.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
}

// socketGoroutines lists the goroutines still running at a socket site (ADR 0123): a dial, a send writer, or a
// producer's Read of a connection. A stdin producer abandoned by another test is not one of them.
func socketGoroutines() []string {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "(*sockSend).run") || strings.Contains(g, "(*Host).tcpConnect.func") ||
			(strings.Contains(g, "(*streamProducer).startReadLocked.func") && strings.Contains(g, "net.(*conn).Read")) {
			out = append(out, g)
		}
	}
	return out
}

// requireNoSocketGoroutines fails if a socket goroutine is still running, after a hang guard for one that has
// called its ended and not yet returned.
func requireNoSocketGoroutines(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second) // a harness hang guard, not a measurement
	for {
		gs := socketGoroutines()
		if len(gs) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d socket goroutine(s) outlived Close:\n%s", len(gs), strings.Join(gs, "\n\n"))
		}
		runtime.Gosched()
	}
}

// p3tcpHost is the Host a p3tcp run gets: argv as a command's, and the grant.
func p3tcpHost(out, errb io.Writer, grant bool, args ...string) *Host {
	h := NewHost(out, errb, nil)
	h.Args = append([]string{"p3tcp.wasm"}, args...)
	h.InheritNetwork = grant
	return h
}

func readP3Tcp(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/p3tcp/p3tcp.wasm")
	if err != nil {
		t.Fatalf("p3tcp fixture missing (it is committed): %v", err)
	}
	return b
}

// runP3Tcp runs p3tcp's `run` through CallRun, with a hang guard.
func runP3Tcp(t *testing.T, in *Instantiated) (int, error) {
	t.Helper()
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() { code, e := in.CallRun(); done <- result{code, e} }()
	select {
	case r := <-done:
		return r.code, r.err
	case <-time.After(20 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("p3tcp's run did not return within the 20s hang guard")
		return 0, nil
	}
}

// TestP3TcpMatchesWasmtime is E7a's std witness (#1235, ADR 0123): an ordinary Rust wasm32-wasip3 program, with no
// bindings, is a TCP client through std's TcpStream. It connects to an echo peer, reads its local and remote
// addresses, writes 100 KiB, shuts its write half, reads the echo to the end, and connects to a port nobody listens
// on. Its stdout, its empty stderr and its exit code must equal wasmtime 49.0.3's, byte for byte, read through
// CallRun as Run reads them, in both gate states:
//
//   - **granted** (`InheritNetwork`, wasmtime's `-S inherit-network`): `testdata/p3tcp.reading`, every property true
//     and the closed port ConnectionRefused;
//   - **denied** (the zero Host, wasmtime with no network flag): `testdata/p3tcp-denied.reading`, both connects
//     PermissionDenied, which is `create`'s access-denied.
//
// The guest prints properties, never a port, so the in-test peer's ports do not reach the comparison. After Close no
// socket goroutine is left running.
//
// Neutered, each run (ADR 0123):
//   - the walk binding connect with no spill: connect is handed the tuple's pointer, one word, and the run fails
//     with *"got 1 flat parameters, want 13"*;
//   - the dial's lend never released: the guest traps dropping the socket it connected, its handle still lent;
//   - the writer's CloseWrite skipped: read_to_end waits for an end the peer never sends, until the hang guard;
//   - get-directories served for 0.2 only again: the run fails at the first socket's close, which wasi-libc follows
//     with a preopens lookup, before the closed-port line.
//
// **Two neuters do NOT fail this witness.** The send sink written inline, though the plan forecast a deadlock here:
// 100 KiB fits the loopback's kernel buffers both ways, so the inline Write never blocks
// (TestCloseEndsASocketsGoroutines' send row is its witness). And tcpCreate's grant check dropped: connect's own check answers
// access-denied, which std reads as the same PermissionDenied (TestTheNetworkIsDeniedAtCreateWithoutTheGrant is its
// witness).
func TestP3TcpMatchesWasmtime(t *testing.T) {
	b := readP3Tcp(t)
	for _, c := range []struct {
		name    string
		grant   bool
		reading string
	}{
		{"granted", true, "testdata/p3tcp.reading"},
		{"denied", false, "testdata/p3tcp-denied.reading"},
	} {
		want, wantExit := readP3CatReading(t, c.reading)
		for _, gate := range []string{"0", "1"} {
			t.Run(c.name+"/cm-threads="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				peer := startPeer(t, peerEcho)
				var out, errb bytes.Buffer
				in, err := InstantiateWithHost(b, p3tcpHost(&out, &errb, c.grant, strconv.Itoa(peer.port), strconv.Itoa(peer.closed)))
				if err != nil {
					t.Fatalf("p3tcp did not instantiate: %v", err)
				}
				code, rerr := runP3Tcp(t, in)
				in.Close()
				if rerr != nil {
					t.Fatalf("p3tcp did not run to its exit: %v\nstdout so far: %q\nstderr: %q", rerr, out.String(), errb.String())
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

// awaitPlace polls a predicate over the instance's async table under its lock, taken with TryLock so a host
// function blocked holding it fails the guard rather than hanging the poll. A harness hang guard, not a measurement.
func awaitPlace(t *testing.T, h *asyncHandles, what string, onFail func(), pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if h.mu.TryLock() {
			ok := pred()
			h.mu.Unlock()
			if ok {
				return
			}
		}
		if time.Now().After(deadline) {
			onFail()
			t.Fatalf("%s: not reached within the 10s hang guard", what)
		}
		runtime.Gosched()
	}
}

// TestCloseEndsASocketsGoroutines is Close's socket rule (ADR 0123), run under -race too: with p3tcp parked on a
// connection, Close returns, the run ends, and no socket goroutine is left running. Unlike stdin's Read, which Close
// abandons (#1274), a socket is the instance's own, so Close closes it and waits its goroutines out. Two places:
//
//   - **parked in receive**: the peer reads the 100 KiB to the guest's shutdown and then holds the connection open,
//     so read_to_end waits with the producer's Read of the connection in flight;
//   - **parked in send**: the peer never reads, and both ends' socket buffers are shrunk to the kernel's minimum
//     (smallSendBufferDial), so 512 KiB is far more than they hold and a guest write parks with the writer
//     goroutine's Write blocked, whatever the runner's kernel would otherwise autotune.
//
// Neutered, each run: walker.close without stopAllSockets, and the receive row's Close hangs, its Read never
// returning; hostNet.stop returning before it closes the connections, the same; the send sink writing the bytes
// inline, and the send row's guest never parks, its stream.write blocked in the Write with asyncHandles.mu held.
// **The send row passes the first two**: the guest parks once the host's 64 KiB buffer is full, before the
// kernel's buffers are, so the Write in flight returns by itself. The receive row is their witness.
func TestCloseEndsASocketsGoroutines(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b := readP3Tcp(t)
	for _, c := range []struct {
		name  string
		mode  int
		bytes string
		place func(p *tcpPeer, h *asyncHandles) (string, func() bool)
	}{
		{"parked in receive", peerDrain, "102400", func(p *tcpPeer, h *asyncHandles) (string, func() bool) {
			return "a receive Read in flight after the guest's shutdown", func() bool {
				select {
				case <-p.gotEOF:
					return h.producerReads > 0
				default:
					return false
				}
			}
		}},
		{"parked in send", peerSilent, "524288", func(_ *tcpPeer, h *asyncHandles) (string, func() bool) {
			return "a guest write parked on the send stream", func() bool {
				for _, e := range h.entries {
					if w, ok := e.(*writableStreamEnd); ok && w.hasWrite && !w.resolved && w.conn != nil && w.conn.hostSink != nil {
						return true
					}
				}
				return false
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			peer := startPeer(t, c.mode)
			host := p3tcpHost(io.Discard, io.Discard, true,
				strconv.Itoa(peer.port), strconv.Itoa(peer.closed), c.bytes)
			if c.mode == peerSilent {
				host.dial = smallSendBufferDial
			}
			in, err := InstantiateWithHost(b, host)
			if err != nil {
				t.Fatalf("p3tcp did not instantiate: %v", err)
			}
			ran := make(chan error, 1)
			go func() { _, e := in.CallRun(); ran <- e }()
			what, pred := c.place(peer, in.w.async)
			awaitPlace(t, in.w.async, what, peer.shut, pred)
			closed := make(chan struct{})
			go func() { in.Close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
				peer.shut()
				t.Fatal("Close did not return within the 10s hang guard with a socket parked")
			}
			select {
			case <-ran:
			case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
				t.Fatal("the run did not end within 10s of Close")
			}
			requireNoSocketGoroutines(t)
		})
	}
}

// sockHarness drives the socket host functions directly, at the built-ins, in the producer harness's shape: one async
// table, one resource table, one test caller with a page of memory, and a granted Host.
type sockHarness struct {
	h    *asyncHandles
	res  *hostResources
	cc   *interp.CanonCaller
	host *Host
}

func newSockHarness(t *testing.T) sockHarness {
	t.Helper()
	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatalf("harness caller: %v", err)
	}
	host := NewHost(io.Discard, io.Discard, nil)
	host.InheritNetwork = true
	s := sockHarness{h: newAsyncHandles(), res: newHostResources(), cc: cc, host: host}
	t.Cleanup(func() {
		s.res.net.stop()
		s.res.net.wait()
	})
	return s
}

func (s sockHarness) word(t *testing.T, ptr uint64) uint32 {
	t.Helper()
	b, err := s.cc.Read(ptr, 4)
	if err != nil {
		t.Fatalf("reading guest memory: %v", err)
	}
	return binary.LittleEndian.Uint32(b)
}

// create is `tcp-socket.create(ipv4)`, returning the own's index.
func (s sockHarness) create(t *testing.T) int32 {
	t.Helper()
	if _, err := s.host.tcpCreate(s.res)(s.cc, []interp.Value{interp.I32(familyIPv4), interp.I32(0)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if b, _ := s.cc.Read(0, 1); b[0] != 0 {
		t.Fatalf("create returned err, discriminant %d", b[0])
	}
	return int32(s.word(t, 4))
}

// connect calls connect through the async lower with its parameters spilled at 64, the result at 128, and returns
// the lower's packed status.
func (s sockHarness) connect(t *testing.T, sock int32, to netip.AddrPort) uint32 {
	t.Helper()
	addr, err := socketAddressValue(to)
	if err != nil {
		t.Fatal(err)
	}
	// The tuple's image, stored with an own where the signature has a borrow: both are the handle's i32.
	img := canon.TupleType(canon.OwnType(tcpSocketResource.rt), ipSocketAddressType())
	v, err := canon.Record(img, map[string]canon.Value{"0": canon.Own(tcpSocketResource.rt, uint32(sock)), "1": addr})
	if err != nil {
		t.Fatal(err)
	}
	if serr := canon.StoreVia(guestHeap{s.cc}, v, 64); serr != nil {
		t.Fatalf("storing connect's parameters: %v", serr)
	}
	spill := canon.TupleType(canon.BorrowType(tcpSocketResource.rt), ipSocketAddressType())
	ret, err := asyncLowerFuncSpilled(s.host.tcpConnect(s.h, s.res), true, &spill, s.h)(s.cc, []interp.Value{interp.I32(64), interp.I32(128)})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return uint32(ret[0].Int32())
}

// awaitResolved waits for subtask sti to resolve, and returns its state.
func (s sockHarness) awaitResolved(t *testing.T, sti uint32) subtaskState {
	t.Helper()
	var state subtaskState
	awaitPlace(t, s.h, "the connect subtask resolved", func() {}, func() bool {
		st, ok := handleAt[*subtask](s.h, sti)
		if ok && st.resolved {
			state = st.state
		}
		return ok && st.resolved
	})
	return state
}

// connected creates a socket, connects it to the peer, and waits for the connect to resolve ok.
func (s sockHarness) connected(t *testing.T, peer *tcpPeer) int32 {
	t.Helper()
	sock := s.create(t)
	packed := s.connect(t, sock, netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), uint16(peer.port)))
	if packed != uint32(subtaskReturned) {
		if st := s.awaitResolved(t, packed>>4); st != subtaskReturned {
			t.Fatalf("connect resolved %d, want RETURNED", st)
		}
	}
	if b, _ := s.cc.Read(128, 1); b[0] != 0 {
		t.Fatalf("connect returned err, discriminant %d, case %d", b[0], s.word(t, 129)&0xff)
	}
	return sock
}

// receive calls receive and returns the stream's and the future's handles.
func (s sockHarness) receive(t *testing.T, sock int32) (ri, fi uint32) {
	t.Helper()
	if _, err := s.host.tcpReceive(s.h, s.res)(s.cc, []interp.Value{interp.I32(sock), interp.I32(256)}); err != nil {
		t.Fatalf("receive: %v", err)
	}
	return s.word(t, 256), s.word(t, 260)
}

// TestTheNetworkIsDeniedAtCreateWithoutTheGrant is the grant's off state (ADR 0123), read at the WIT where std's
// reading cannot: wasmtime 49.0.3 with no network flag answers `create` itself `err(access-denied)` (its trace,
// measured), and so does a Host without InheritNetwork. p3tcp's denied reading cannot tell create's denial from
// connect's, since std maps both to PermissionDenied.
//
// Neutered, run: tcpCreate's grant check dropped, and create returns ok with a handle. p3tcp's denied rows still
// pass under it, connect's own check answering access-denied.
func TestTheNetworkIsDeniedAtCreateWithoutTheGrant(t *testing.T) {
	s := newSockHarness(t)
	s.host.InheritNetwork = false
	if _, err := s.host.tcpCreate(s.res)(s.cc, []interp.Value{interp.I32(familyIPv4), interp.I32(0)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	b, _ := s.cc.Read(0, 5)
	if b[0] != 1 || b[4] != 0 {
		t.Errorf("create with the network denied = discriminant %d, case %d; want error (1), access-denied (0)", b[0], b[4])
	}
}

// TestACancelledConnectResolvesCancelledAndItsDialEnds is connect's cancel (ADR 0123), run under -race too: a dial
// held in flight is cancelled by the guest's async `subtask.cancel`, which reads CANCELLED_BEFORE_RETURNED inline;
// the dial's context is cancelled, so its goroutine returns and the registry has none left; and the lend of self is
// released, so the guest can drop the socket.
//
// Neutered, run: cancelSubtask returning without claiming or resolving, and the cancel reads BLOCKED, the dial left
// waiting on its context until the guard.
func TestACancelledConnectResolvesCancelledAndItsDialEnds(t *testing.T) {
	s := newSockHarness(t)
	entered, returned := make(chan struct{}), make(chan error, 1)
	s.host.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(entered)
		<-ctx.Done()
		returned <- ctx.Err()
		return nil, ctx.Err()
	}
	sock := s.create(t)
	packed := s.connect(t, sock, netip.MustParseAddrPort("127.0.0.1:9"))
	if state := subtaskState(packed & 0xf); state != subtaskStarted {
		t.Fatalf("connect returned state %d, want STARTED: the dial is held", state)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("the dial did not start within the 10s hang guard")
	}
	ret, err := subtaskCancel(s.h, true)(s.cc, []interp.Value{interp.I32(int32(packed >> 4))})
	if err != nil {
		t.Fatalf("subtask.cancel: %v", err)
	}
	if got := uint32(ret[0].Int32()); got != uint32(subtaskCancelledBeforeReturned) {
		t.Errorf("subtask.cancel = %#x, want CANCELLED_BEFORE_RETURNED (4) inline", got)
	}
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the dial returned %v, want its context cancelled", err)
		}
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("the dial was not cancelled within the 10s hang guard")
	}
	waited := make(chan struct{})
	go func() { s.res.net.wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("the dial's goroutine did not end within the 10s hang guard")
	}
	if err := s.res.drop(tcpSocketResource, sock); err != nil {
		t.Errorf("dropping the socket after the cancel: %v (the connect's lend was not released)", err)
	}
	s.h.mu.Lock()
	pending := s.h.sockPending
	s.h.mu.Unlock()
	if pending != 0 {
		t.Errorf("sockPending = %d after the cancel, want 0: the park bound would read a waker that is gone", pending)
	}
}

// TestTheConnectionOutlivesTheHandleWhileAStreamHoldsIt is sockets.wit's shared ownership (ADR 0123): the receive
// stream holds the connection, so a guest that drops its tcp-socket handle first still reads what the peer sends.
//
// Neutered, run: hostNet.release closing at every release, and the read after the handle's drop is DROPPED with
// nothing, the connection closed under the stream.
func TestTheConnectionOutlivesTheHandleWhileAStreamHoldsIt(t *testing.T) {
	s := newSockHarness(t)
	peer := startPeer(t, peerSilent)
	sock := s.connected(t, peer)
	ri, _ := s.receive(t, sock)
	if err := s.res.drop(tcpSocketResource, sock); err != nil {
		t.Fatalf("dropping the handle: %v", err)
	}
	var c net.Conn
	awaitPlace(t, s.h, "the peer accepted", func() {}, func() bool {
		peer.mu.Lock()
		defer peer.mu.Unlock()
		if len(peer.conns) > 0 {
			c = peer.conns[0]
		}
		return c != nil
	})
	if _, err := c.Write([]byte("hi")); err != nil {
		t.Fatalf("the peer's write: %v", err)
	}
	_ = c.(*net.TCPConn).CloseWrite()
	if got := uint32(mustRet(t)(streamRead(s.h, byteElem)(s.cc, []interp.Value{interp.I32(int32(ri)), interp.I32(512), interp.I32(8)}))); got != asyncBlocked {
		t.Fatalf("the first read = %#x, want BLOCKED: nothing has been read yet", got)
	}
	var re *readableStreamEnd
	awaitPlace(t, s.h, "the receive read resolved", func() {}, func() bool {
		re, _ = handleAt[*readableStreamEnd](s.h, ri)
		return re != nil && re.resolved
	})
	b, _ := s.cc.Read(512, 2)
	if re.result != copyCompleted || re.progress != 2 || string(b) != "hi" {
		t.Errorf("the read after the handle's drop = result %d, progress %d, %q; want COMPLETED, 2, \"hi\"",
			re.result, re.progress, b)
	}
}

// TestAReceiveDropShutsTheReadHalf is receive's drop (ADR 0123): the guest dropping the receive stream is
// `shutdown(SHUT_RD)`, as sockets.wit states, so the connection's read half is shut and a Read of it returns at once.
//
// Neutered, run: the CloseRead dropped, and the Read waits out its 2s deadline.
func TestAReceiveDropShutsTheReadHalf(t *testing.T) {
	s := newSockHarness(t)
	peer := startPeer(t, peerSilent)
	sock := s.connected(t, peer)
	ri, _ := s.receive(t, sock)
	obj, release, err := lendAs[*tcpSocket](s.res, tcpSocketResource, sock)
	if err != nil {
		t.Fatal(err)
	}
	conn := obj.conn.c
	release()
	if _, err := streamDropReadable(s.h, byteElem)(s.cc, []interp.Value{interp.I32(int32(ri))}); err != nil {
		t.Fatalf("stream.drop-readable: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second)) // the neuter's bound, not a measurement
	_, rerr := conn.Read(make([]byte, 1))
	var ne net.Error
	if errors.As(rerr, &ne) && ne.Timeout() {
		t.Errorf("a Read after the receive stream's drop waited out its deadline: the read half was not shut")
	}
}

func mustRet(t *testing.T) func([]interp.Value, error) int32 {
	t.Helper()
	return func(v []interp.Value, err error) int32 {
		t.Helper()
		if err != nil {
			t.Fatalf("built-in: %v", err)
		}
		return v[0].Int32()
	}
}

// TestAnAsyncLowerLoadsItsSpilledParameters is the spill's general mechanism (ADR 0123), on a fixture rather than on
// connect: async-spill-params.wasm's `wide` takes tuple<u8, u64, s16, u32, u32>, five flat words, past
// MAX_FLAT_ASYNC_PARAMS, so its core function takes the tuple's pointer. The host impl reads the five words it would
// have been handed unspilled, at their core types, with s16's sign extended into its i32; a pointer not aligned to
// the tuple's 8, and a tuple past the end of memory, are the guest's traps (def:lift_flat_values).
//
// Neutered, run: asyncLowerFuncWith skipping the load, and the impl is handed the pointer, one word.
func TestAnAsyncLowerLoadsItsSpilledParameters(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/async-spill-params.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	var got []interp.Value
	h := NewHost(io.Discard, io.Discard, nil)
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:spill/wide::wide": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			got = onStart()
			onResolve(canon.Value{})
			return nil, nil
		},
	}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	if _, err := in.CallValues("call", canon.U32(16)); err != nil {
		t.Fatalf("call(16): %v", err)
	}
	want := []interp.Value{interp.I32(200), interp.I64(0x0102030405060708), interp.I32(-3), interp.I32(7), interp.I32(9)}
	if len(got) != len(want) {
		t.Fatalf("the impl was handed %d words (%v), want the tuple's %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("word %d = %v, want %v", i, got[i], want[i])
		}
	}
	// Each trap locks its instance (#1040), so each is asked of a fresh one.
	for _, c := range []struct {
		ptr   uint32
		names string
	}{{4, "not aligned"}, {65520, "parameter tuple"}} {
		tin, err := InstantiateWithHost(b, h)
		if err != nil {
			t.Fatalf("instantiate: %v", err)
		}
		_, err = tin.CallValues("call", canon.U32(c.ptr))
		tin.Close()
		var trap *interp.Trap
		if !errors.As(err, &trap) || !strings.Contains(err.Error(), c.names) {
			t.Errorf("call(%d) err = %v, want a trap naming %q", c.ptr, err, c.names)
		}
	}
}

// tcpConnectParkReading reads one row of testdata/tcp-connect-park.reading.
func tcpConnectParkReading(t *testing.T, row string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/tcp-connect-park.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(ln, row+": "); ok {
			return rest
		}
	}
	t.Fatalf("tcp-connect-park.reading has no row %q", row)
	return ""
}

// runConnectPark calls tcp-connect-park.wasm's callback-lifted run(port) on a granted Host and returns its result.
func runConnectPark(t *testing.T, h *Host, port int) string {
	t.Helper()
	b, err := os.ReadFile("testdata/tcp-connect-park.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	type out struct {
		res []canon.Value
		err error
	}
	done := make(chan out, 1)
	go func() { r, e := in.CallValues("run", canon.U32(uint32(port))); done <- out{r, e} }()
	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("run(%d) = %v", port, o.err)
		}
		v, _ := o.res[0].U32()
		return fmtU32(v)
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("run neither returned nor trapped within the 10s hang guard")
		return ""
	}
}

// TestTcpConnectParkMatchesWasmtime is connect at the ABI, below std (ADR 0123): tcp-connect-park.wasm's
// callback-lifted task connects through the async lower, with its parameters spilled, and waits for the subtask.
// Against a listening port its event is RETURNED with `ok`, and against a closed one RETURNED with an error, as
// wasmtime 49.0.3 reads them under `-S inherit-network`.
func TestTcpConnectParkMatchesWasmtime(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	peer := startPeer(t, peerSilent)
	for _, c := range []struct {
		row  string
		port int
	}{{"listening", peer.port}, {"closed", peer.closed}, {"port-zero", 0}} {
		// port-zero resolves before connect's lower returns, so it is the fixture's inline-RETURNED arm, every run.
		// NEUTER: the fixture as first committed (join the packed result's index unconditionally), and this row
		// traps "waitable.join: handle 0 is not a waitable", as PR #1280's second CI run did by chance on Linux.
		h := NewHost(io.Discard, io.Discard, nil)
		h.InheritNetwork = true
		if got := runConnectPark(t, h, c.port); got != tcpConnectParkReading(t, c.row) {
			t.Errorf("run(%s port) = %s, want wasmtime's %s", c.row, got, tcpConnectParkReading(t, c.row))
		}
	}
}

// TestAPendingDialOutlivesTheParkBound is the socket term of the park bound's waker (ADR 0123): with liftParkBound
// at 5 ms and a dial that answers after 60 ms, tcp-connect-park.wasm's callback-lifted wait still reads wasmtime's
// `listening` row. A pending dial is a waker, as a producer's Read is, so the bound does not end a wait the dial is
// going to satisfy. (p3tcp cannot witness this: its `run` is sync-lifted, and the bound is the callback loop's.)
//
// Neutered, run: hostWaitPendingLocked without the socket term, and run fails with ErrLiftParkExpired.
func TestAPendingDialOutlivesTheParkBound(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	prev := liftParkBound
	liftParkBound = 5 * time.Millisecond
	defer func() { liftParkBound = prev }()
	peer := startPeer(t, peerSilent)
	h := NewHost(io.Discard, io.Discard, nil)
	h.InheritNetwork = true
	h.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		time.Sleep(60 * time.Millisecond) // the setup's delay, past the bound twelve times; nothing is asserted on it
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}
	if got := runConnectPark(t, h, peer.port); got != tcpConnectParkReading(t, "listening") {
		t.Errorf("run = %s, want wasmtime's %s", got, tcpConnectParkReading(t, "listening"))
	}
}
