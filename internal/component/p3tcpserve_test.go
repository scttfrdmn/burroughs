// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestP3TcpServeMatchesWasmtime is E7b-2's std witness (#1277, ADR 0125): an ordinary Rust wasm32-wasip3 program,
// with no bindings and one thread, is a TCP server through std's TcpListener. It binds 127.0.0.1:0 (create, bind,
// set-listen-backlog-size(128), listen), reads the listener's address, connects to itself, accepts (one stream.read
// of listen's stream<own<tcp-socket>>, then receive, send and get-remote-address on the accepted handle), and echoes
// 1 KiB each way. Its stdout, its empty stderr and its exit code must equal wasmtime 49.0.3's, byte for byte, in both
// gate states:
//
//   - **granted**: `testdata/p3tcpserve.reading`, every property true;
//   - **denied**: `testdata/p3tcpserve-denied.reading`, `bind: PermissionDenied`, which is create's access-denied.
//
// The guest prints properties, never a port. After Close no socket goroutine is left running.
//
// Neutered, each run (ADR 0125):
//   - the copy storing each socket's queue position rather than its minted index: the accept reads a handle the
//     guest's table does not hold, and the run traps at the accepted socket's receive;
//   - the local address answered for a connected socket only, as E7a did: the listener's address line reads
//     NotConnected, std's reading of invalid-state;
//   - readerDroppedLocked resolving the future unconditionally: the run panics on the nil future at the listener's
//     drop.
//
// **One neuter does NOT fail this witness**: the accepted connection built unadopted. Every connection this guest
// accepts is delivered and then dropped by the guest, and the drop's release closes it whether or not the registry
// holds it. TestAnAcceptedConnectionNeverDeliveredIsClosedAtClose is its witness.
func TestP3TcpServeMatchesWasmtime(t *testing.T) {
	b, err := os.ReadFile("testdata/p3tcpserve/p3tcpserve.wasm")
	if err != nil {
		t.Fatalf("p3tcpserve fixture missing (it is committed): %v", err)
	}
	for _, c := range []struct {
		name    string
		grant   bool
		reading string
	}{
		{"granted", true, "testdata/p3tcpserve.reading"},
		{"denied", false, "testdata/p3tcpserve-denied.reading"},
	} {
		want, wantExit := readP3CatReading(t, c.reading)
		for _, gate := range []string{"0", "1"} {
			t.Run(c.name+"/cm-threads="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				var out, errb bytes.Buffer
				h := NewHost(&out, &errb, nil)
				h.Args = []string{"p3tcpserve.wasm"}
				h.InheritNetwork = c.grant
				in, err := InstantiateWithHost(b, h)
				if err != nil {
					t.Fatalf("p3tcpserve did not instantiate: %v", err)
				}
				code, rerr := runP3Tcp(t, in)
				in.Close()
				if rerr != nil {
					t.Fatalf("p3tcpserve did not run to its exit: %v\nstdout so far: %q\nstderr: %q", rerr, out.String(), errb.String())
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

// tcpBindListenReading reads one row of testdata/tcp-bind-listen.reading.
func tcpBindListenReading(t *testing.T, row string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/tcp-bind-listen.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(ln, row+": "); ok {
			return rest
		}
	}
	t.Fatalf("tcp-bind-listen.reading has no row %q", row)
	return ""
}

// startBindListen instantiates tcp-bind-listen.wasm on a granted Host and calls run(row, port) on a goroutine. The
// caller reads the result from the channel, or closes the instance.
func startBindListen(t *testing.T, row, port int) (*Instantiated, <-chan []canon.Value, <-chan error) {
	t.Helper()
	b, err := os.ReadFile("testdata/tcp-bind-listen.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	h.InheritNetwork = true
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	res, errc := make(chan []canon.Value, 1), make(chan error, 1)
	go func() {
		r, e := in.CallValues("run", canon.U32(uint32(row)), canon.U32(uint32(port)))
		if e != nil {
			errc <- e
			return
		}
		res <- r
	}()
	return in, res, errc
}

// TestTcpBindListenMatchesWasmtime is bind, listen and the backlog setter at the ABI, below std (ADR 0125): every
// row of tcp-bind-listen.wasm against wasmtime 49.0.3's tcp-bind-listen.reading (10 of 10 per row), on fresh
// sockets, the rows that connect against an in-test peer.
//
// **One row is Burroughs' own by decision**: connect-before-listen, a connect to a bound port before any listen.
// bind listens eagerly here, so the kernel completes the connect into the backlog and it is ok (0), where wasmtime
// on darwin times out after about 7.8 s (the reading's 0x00840000; other kernels unmeasured). ADR 0125 records the
// divergence; this row asserts Burroughs' value rather than wasmtime's.
//
// Neutered, each run: bind's state check dropped, and bind-refusals reads its second bind ok (0x85820000).
func TestTcpBindListenMatchesWasmtime(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	peer := startPeer(t, peerSilent)
	for i, row := range []string{
		"local-between", "backlog-zero", "listen-twice", "listen-connected", "listen-unbound", "bind-refusals",
		"connect-bound", "listening-getters", "backlog-connected", "connect-listening", "connect-before-listen",
		"accept-one",
	} {
		t.Run(row, func(t *testing.T) {
			want := tcpBindListenReading(t, row)
			if row == "connect-before-listen" {
				want = "0" // Burroughs' value, by ADR 0125's recorded divergence
			}
			in, res, errc := startBindListen(t, i, peer.port)
			defer in.Close()
			select {
			case r := <-res:
				v, _ := r[0].U32()
				if got := strconv.FormatUint(uint64(v), 10); got != want {
					t.Errorf("run(%d, %s) = %s (%#08x), want %s", i, row, got, v, want)
				}
			case e := <-errc:
				t.Fatalf("run(%d, %s) = %v", i, row, e)
			case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
				t.Fatalf("run(%d, %s) neither returned nor trapped within the 10s hang guard", i, row)
			}
		})
	}
}

// acceptInFlight reports a listen stream with a guest read pending and its Accept in flight. The caller holds h.mu.
func acceptInFlight(h *asyncHandles) bool {
	for _, e := range h.entries {
		if r, ok := e.(*readableStreamEnd); ok && r.elem == acceptElem && r.hasRead && !r.resolved && r.conn != nil &&
			r.conn.producer != nil && r.conn.producer.reading {
			return true
		}
	}
	return false
}

// TestCloseEndsAGuestParkedInAccept is Close's listener rule (ADR 0125), run under -race too: tcp-bind-listen's
// accept-park row listens and reads the listen stream with nobody connecting, so the task parks in
// waitable-set.wait with the producer's Accept in flight. Close returns, the run ends, and the Accept's goroutine is
// gone: Close closes the listener, so the Accept returns, and waits it out.
//
// Neutered, run: hostNet.stop skipping the listeners' close, and Close hangs, waiting for the Accept.
func TestCloseEndsAGuestParkedInAccept(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	in, res, errc := startBindListen(t, 12, 0)
	awaitPlace(t, in.w.async, "a guest read of the listen stream with its Accept in flight", func() {}, func() bool {
		return acceptInFlight(in.w.async)
	})
	closed := make(chan struct{})
	go func() { in.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("Close did not return within the 10s hang guard with a guest parked in accept")
	}
	select {
	case <-res:
	case <-errc:
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("the run did not end within 10s of Close")
	}
	requireNoSocketGoroutines(t)
}

// bound creates an ipv4 socket and binds it to 127.0.0.1:0 through bind's method binding, failing on an error.
func (s sockHarness) bound(t *testing.T) int32 {
	t.Helper()
	sock := s.create(t)
	args := []interp.Value{
		interp.I32(sock), interp.I32(familyIPv4), interp.I32(0), interp.I32(127), interp.I32(0),
		interp.I32(0), interp.I32(1),
	}
	for range 6 {
		args = append(args, interp.I32(0))
	}
	args = append(args, interp.I32(16))
	if _, err := method(tcpSocketResource, s.host.tcpBind)(s.res)(s.cc, args); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if b, _ := s.cc.Read(16, 5); b[0] != 0 {
		t.Fatalf("bind returned err, case %d", b[4])
	}
	return sock
}

// listen calls listen and returns the stream's handle.
func (s sockHarness) listen(t *testing.T, sock int32) uint32 {
	t.Helper()
	if _, err := s.host.tcpListen(s.h, s.res)(s.cc, []interp.Value{interp.I32(sock), interp.I32(32)}); err != nil {
		t.Fatalf("listen: %v", err)
	}
	if b, _ := s.cc.Read(32, 5); b[0] != 0 {
		t.Fatalf("listen returned err, case %d", b[4])
	}
	return s.word(t, 36)
}

// localOf is a socket's recorded local address.
func (s sockHarness) localOf(t *testing.T, sock int32) netip.AddrPort {
	t.Helper()
	obj, release, err := lendAs[*tcpSocket](s.res, tcpSocketResource, sock)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	obj.mu.Lock()
	defer obj.mu.Unlock()
	return obj.local
}

// readAccept issues one stream.read of one element of listen stream ri into 512, and returns its packed status.
func (s sockHarness) readAccept(t *testing.T, ri uint32) uint32 {
	t.Helper()
	return uint32(mustRet(t)(streamRead(s.h, acceptElem)(s.cc, []interp.Value{interp.I32(int32(ri)), interp.I32(512), interp.I32(1)})))
}

// awaitRead waits for the stream read on ri to resolve, and returns its result and progress.
func (s sockHarness) awaitRead(t *testing.T, ri uint32) (copyResult, uint32) {
	t.Helper()
	var re *readableStreamEnd
	awaitPlace(t, s.h, "the listen stream's read resolved", func() {}, func() bool {
		re, _ = handleAt[*readableStreamEnd](s.h, ri)
		return re != nil && re.resolved
	})
	s.h.mu.Lock()
	defer s.h.mu.Unlock()
	return re.result, re.progress
}

// TestAnAcceptedConnectionNeverDeliveredIsClosedAtClose is the adoption at accept (ADR 0125), run under -race too: a
// guest read of the listen stream starts an Accept and is cancelled before anyone connects, so the connection the
// Accept then returns is queued and never delivered. Close closes it: the client's read returns at once.
//
// Neutered, run: the accepted connection built unadopted, and the client's read waits out its 2s deadline.
func TestAnAcceptedConnectionNeverDeliveredIsClosedAtClose(t *testing.T) {
	s := newSockHarness(t)
	sock := s.bound(t)
	ri := s.listen(t, sock)
	if got := s.readAccept(t, ri); got != asyncBlocked {
		t.Fatalf("the read = %#x, want BLOCKED: nobody has connected", got)
	}
	if _, err := streamCancelRead(s.h, acceptElem)(s.cc, []interp.Value{interp.I32(int32(ri))}); err != nil {
		t.Fatalf("stream.cancel-read: %v", err)
	}
	client, err := net.Dial("tcp4", s.localOf(t, sock).String())
	if err != nil {
		t.Fatalf("the client's dial: %v", err)
	}
	defer client.Close()
	awaitPlace(t, s.h, "the accepted connection queued, undelivered", func() {}, func() bool {
		re, _ := handleAt[*readableStreamEnd](s.h, ri)
		return re != nil && re.conn != nil && len(re.conn.producer.socks) == 1 && !re.conn.producer.reading
	})
	waited := make(chan struct{})
	go func() { s.res.net.stop(); s.res.net.wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("the instance's sockets did not stop within the 10s hang guard")
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second)) // the neuter's bound, not a measurement
	_, rerr := client.Read(make([]byte, 1))
	var ne net.Error
	if errors.As(rerr, &ne) && ne.Timeout() {
		t.Errorf("the client's read waited out its deadline: the accepted, undelivered connection was not closed")
	}
}

// TestTheListenerOutlivesTheHandleWhileItsStreamHoldsIt is sockets.wit's shared ownership for a listener (ADR
// 0125): the listen stream holds the listener, so a guest that drops its tcp-socket handle still accepts through
// the stream. The client connects before the drop, so the connection is in the backlog the listener's close would
// discard.
//
// Neutered, run: hostNet.releaseListener closing at every release, and the read after the handle's drop is DROPPED
// with no socket.
func TestTheListenerOutlivesTheHandleWhileItsStreamHoldsIt(t *testing.T) {
	s := newSockHarness(t)
	sock := s.bound(t)
	ri := s.listen(t, sock)
	client, err := net.Dial("tcp4", s.localOf(t, sock).String())
	if err != nil {
		t.Fatalf("the client's dial: %v", err)
	}
	defer client.Close()
	if derr := s.res.drop(tcpSocketResource, sock); derr != nil {
		t.Fatalf("dropping the listener's handle: %v", derr)
	}
	if got := s.readAccept(t, ri); got != asyncBlocked && got != uint32(copyCompleted)|1<<4 {
		t.Fatalf("the read = %#x, want BLOCKED or COMPLETED with progress 1", got)
	}
	result, progress := s.awaitRead(t, ri)
	if result != copyCompleted || progress != 1 {
		t.Fatalf("the read after the handle's drop = result %d, progress %d; want COMPLETED, 1", result, progress)
	}
	accepted := int32(s.word(t, 512))
	obj, release, err := lendAs[*tcpSocket](s.res, tcpSocketResource, accepted)
	if err != nil {
		t.Fatalf("the accepted handle %d is not a tcp-socket the table holds: %v", accepted, err)
	}
	obj.mu.Lock()
	state, remote := obj.state, obj.remote
	obj.mu.Unlock()
	release()
	if state != tcpConnected || remote.String() != client.LocalAddr().String() {
		t.Errorf("the accepted socket is state %d with remote %s, want connected with the client's %s", state, remote,
			client.LocalAddr())
	}
}

// failingListener is a listener whose first Accept fails with err, then accepts as its listener does.
type failingListener struct {
	net.Listener
	mu     sync.Mutex
	err    error
	failed bool
}

func (l *failingListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	first := !l.failed
	l.failed = true
	l.mu.Unlock()
	if first {
		return nil, l.err
	}
	return l.Listener.Accept()
}

// TestATransientAcceptErrorDoesNotEndTheStream is the listen stream's perpetuity (sockets.wit's listen, ADR 0125):
// an Accept that fails with ECONNABORTED, one of the transient errors the WIT names, is skipped, and the pending read
// accepts again and completes with the client's connection. A fatal error, the control, ends the stream: the read
// is DROPPED.
//
// Neutered, each run: acceptTransient returning false, and the transient row's read is DROPPED; finishLocked
// resolving the future unconditionally, and the fatal row panics on the listen stream's nil future.
func TestATransientAcceptErrorDoesNotEndTheStream(t *testing.T) {
	for _, c := range []struct {
		name   string
		err    error
		result copyResult
	}{
		{"transient", &net.OpError{Op: "accept", Net: "tcp", Err: os.NewSyscallError("accept", syscall.ECONNABORTED)}, copyCompleted},
		{"fatal", errors.New("an accept error no table names"), copyDropped},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSockHarness(t)
			s.host.listen = func(network, address string) (net.Listener, error) {
				l, err := net.Listen(network, address)
				if err != nil {
					return nil, err
				}
				return &failingListener{Listener: l, err: c.err}, nil
			}
			sock := s.bound(t)
			ri := s.listen(t, sock)
			if got := s.readAccept(t, ri); got != asyncBlocked {
				t.Fatalf("the read = %#x, want BLOCKED: nobody has connected", got)
			}
			if c.result == copyCompleted {
				client, err := net.Dial("tcp4", s.localOf(t, sock).String())
				if err != nil {
					t.Fatalf("the client's dial: %v", err)
				}
				defer client.Close()
			}
			result, progress := s.awaitRead(t, ri)
			if result != c.result {
				t.Errorf("the read after a %s accept error = result %d, progress %d; want %d", c.name, result, progress, c.result)
			}
		})
	}
}

// TestListenMustBeDeclaredAsTheHostDeclaresIt is listen's own signature rule (ADR 0125): tcp-listen-mistyped.wasm
// declares listen's stream as stream<u32>, and the host mints each element as an own of its tcp-socket, so it is
// refused at instantiate by name. wasmtime 49.0.3 refuses it at link too (*"instance export
// `[method]tcp-socket.listen` has the wrong type"*). tcp-bind-listen.wasm, declared as the host declares it, is
// the control every other test here instantiates.
//
// Neutered, run: checkListenSignature returning nil, and the guest instantiates.
func TestListenMustBeDeclaredAsTheHostDeclaresIt(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/tcp-listen-mistyped.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	h.InheritNetwork = true
	in, err := InstantiateWithHost(b, h)
	if err == nil {
		in.Close()
		t.Fatal("a guest declaring listen's stream as stream<u32> instantiated")
	}
	if !errors.Is(err, ErrLinkRefused) || !strings.Contains(err.Error(), "result<stream<own<tcp-socket>>") {
		t.Errorf("instantiate = %v, want ErrLinkRefused naming the host's declaration", err)
	}
}
