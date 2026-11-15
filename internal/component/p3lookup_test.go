// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// p3lookupHost is the Host a p3lookup run gets: argv as a command's, and the two grants.
func p3lookupHost(out, errb io.Writer, network, lookup bool, args ...string) *Host {
	h := NewHost(out, errb, nil)
	h.Args = append([]string{"p3lookup.wasm"}, args...)
	h.InheritNetwork = network
	h.AllowNameLookup = lookup
	return h
}

// TestP3LookupMatchesWasmtime is E7c's std witness (#1278, ADR 0126): an ordinary Rust wasm32-wasip3 program, with no
// bindings, resolves names through std's ToSocketAddrs and connects to `localhost:<port>` through TcpStream. Every
// lookup is wasi-libc's sync lower of `resolve-addresses`, an async import, so every one is the blocking sync lower
// (#1268). Its stdout, its empty stderr and its exit code must equal wasmtime 49.0.3's, byte for byte, read through
// CallRun as Run reads them, in both gate states:
//
//   - **granted** (InheritNetwork and AllowNameLookup, wasmtime's `-S inherit-network -S allow-ip-name-lookup`):
//     `testdata/p3lookup.reading`, localhost resolving to loopback including 127.0.0.1, the literal to itself, the
//     two invalid names InvalidInput, and the connect echoing;
//   - **lookup denied** (InheritNetwork alone, wasmtime's `-S inherit-network`): `testdata/p3lookup-denied.reading`,
//     each name wasi-libc's EAI_FAIL, which is permanent-resolver-failure, the literal still resolved by wasi-libc,
//     and the connect failing at its lookup.
//
// No name needs a DNS server, so neither engine's reading depends on the network. The guest prints properties of
// localhost's answers, never the answers: which loopbacks localhost has, and their order, are the host's (wasmtime on
// darwin answers ::1 first). After Close no socket goroutine is left running.
//
// Neutered, each run (ADR 0126):
//   - no grant (AllowNameLookup false in the granted row): the granted row prints the denied reading;
//   - the sync lower refused at instantiate again: p3lookup does not instantiate;
//   - the grant-off code access-denied rather than permanent-resolver-failure: the lookup-denied row's three lookups
//     print "PermissionDenied: Permission denied (os error 2)", wasi-libc's mapping of access-denied, instead of
//     EAI_FAIL's "Non-recoverable error", and the connect PermissionDenied.
//
// The guest sorts localhost's answers before reading them, and reads only order-free properties, so the plan's
// fourth neuter, an unsorted comparison, has no line to change: Burroughs' order and the oracle's cannot reach the
// comparison.
func TestP3LookupMatchesWasmtime(t *testing.T) {
	b, err := os.ReadFile("testdata/p3lookup/p3lookup.wasm")
	if err != nil {
		t.Fatalf("p3lookup fixture missing (it is committed): %v", err)
	}
	for _, c := range []struct {
		name    string
		lookup  bool
		reading string
	}{
		{"granted", true, "testdata/p3lookup.reading"},
		{"lookup-denied", false, "testdata/p3lookup-denied.reading"},
	} {
		want, wantExit := readP3CatReading(t, c.reading)
		for _, gate := range []string{"0", "1"} {
			t.Run(c.name+"/cm-threads="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				peer := startPeer(t, peerEcho)
				var out, errb bytes.Buffer
				in, err := InstantiateWithHost(b, p3lookupHost(&out, &errb, true, c.lookup, strconv.Itoa(peer.port)))
				if err != nil {
					t.Fatalf("p3lookup did not instantiate: %v", err)
				}
				code, rerr := runP3Tcp(t, in)
				in.Close()
				if rerr != nil {
					t.Fatalf("p3lookup did not run to its exit: %v\nstdout so far: %q\nstderr: %q", rerr, out.String(), errb.String())
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

// heldLookup is a resolver that never answers: it returns when its context is cancelled, reporting the cancel on
// returned if that is not nil.
func heldLookup(entered chan<- struct{}, returned chan<- error) func(ctx context.Context, _, _ string) ([]netip.Addr, error) {
	return func(ctx context.Context, _, _ string) ([]netip.Addr, error) {
		if entered != nil {
			close(entered)
		}
		<-ctx.Done()
		if returned != nil {
			returned <- ctx.Err()
		}
		return nil, ctx.Err()
	}
}

// TestNameLookupMatchesWasmtime is E7c's WAT witness (#1278, #1268, ADR 0126): `name-lookup.wasm` sync-lowers
// resolve-addresses and reads each row's result at the ABI, against wasmtime 49.0.3's reading of the same component
// (name-lookup.reading), in both gate states. Its rows are literals, invalid names and localhost, none needing a DNS
// server; every row is read granted and with the grant off, through the async-typed `run`, through the sync-typed
// `run-sync`, which traps where a lookup does not resolve inline, and through `run-async`, which lowers the lookup
// with the async option and waits for its subtask, so localhost's `list<ip-address>` is stored by the async lower's
// result store, from the lookup's goroutine, through the guest's realloc.
//
// **localhost's `run-sync` row is read with its lookup held.** wasmtime traps it, its lookup not resolving inline.
// Here localhost is answered on a goroutine too, but the hosts file can answer before the sync tail judges the wait,
// and the pin allows either outcome, so the row is made deterministic by holding the lookup; the guard of E7a's CI
// lessons is not to rest a witness on that race.
//
// Neutered, run: the grant check dropped, and every `off` row fails; the grant-off code access-denied, and every
// `off` row reads 0x80; the literal parse skipped, and rows 1, 2, 5, 6 and 7 read invalid-argument in both exports,
// each literal failing the name check (a colon, a bracket, or ending in a number), so none reaches the resolver; the
// sync lower's wait not judged, and the held `run-sync(0)` waits until the hang guard; the sync tail judged by lift
// form, and `run(0)`, whose localhost lookup is pending when judged, traps.
func TestNameLookupMatchesWasmtime(t *testing.T) {
	b, err := os.ReadFile("testdata/name-lookup.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	type row struct {
		export string
		n      int
		grant  bool
	}
	var rows []row
	for _, export := range []string{"run", "run-sync", "run-async"} {
		for n := range 9 {
			rows = append(rows, row{export, n, true})
		}
		for _, n := range []int{0, 1, 3, 5, 8} {
			rows = append(rows, row{export, n, false})
		}
	}
	for _, gate := range []string{"0", "1"} {
		for _, r := range rows {
			grant := "off"
			if r.grant {
				grant = "granted"
			}
			key := r.export + "(" + strconv.Itoa(r.n) + ") " + grant
			t.Run(key+"/cm-threads="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				reading := readingRow(t, "testdata/name-lookup.reading", key)
				h := NewHost(io.Discard, io.Discard, nil)
				h.AllowNameLookup = r.grant
				if r.export == "run-sync" && r.n == 0 && r.grant {
					h.lookup = heldLookup(nil, nil)
				}
				in, err := InstantiateWithHost(b, h)
				if err != nil {
					t.Fatalf("instantiate: %v", err)
				}
				defer in.Close()
				res, err := callWithinArgs(t, in, r.export, uint32(r.n))
				if trap, ok := strings.CutPrefix(reading, "trap: "); ok {
					if err == nil || !strings.Contains(err.Error(), trap) {
						t.Fatalf("%s = %v, %v; want wasmtime's trap %q", key, res, err, trap)
					}
					return
				}
				want, perr := strconv.ParseUint(reading, 10, 32)
				if perr != nil {
					t.Fatalf("name-lookup.reading's %s row %q: %v", key, reading, perr)
				}
				if err != nil {
					t.Fatalf("%s = %v; want %#x, wasmtime's reading", key, err, want)
				}
				if got, ok := res[0].U32(); !ok || uint64(got) != want {
					t.Fatalf("%s = %v (%#x), want %#x, wasmtime's reading", key, res, got, want)
				}
			})
		}
	}
}

// callWithinArgs is callWithin with a u32 argument.
func callWithinArgs(t *testing.T, in *Instantiated, export string, arg uint32) ([]canon.Value, error) {
	t.Helper()
	type out struct {
		res []canon.Value
		err error
	}
	done := make(chan out, 1)
	go func() { r, e := in.CallValues(export, canon.U32(arg)); done <- out{r, e} }()
	select {
	case o := <-done:
		return o.res, o.err
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatalf("%s(%d) neither returned nor trapped within the 10s hang guard", export, arg)
		return nil, nil
	}
}

// lookupHarness calls resolve-addresses through the async lower, the name stored at 512 and the result at 256, and
// returns the lower's packed status.
func lookupHarness(t *testing.T, s sockHarness, name string) uint32 {
	t.Helper()
	if err := s.cc.Write(512, []byte(name)); err != nil {
		t.Fatalf("storing the name: %v", err)
	}
	ret, err := asyncLowerFunc(s.host.resolveAddresses(s.h, s.res), true, s.h)(s.cc,
		[]interp.Value{interp.I32(512), interp.I32(int32(len(name))), interp.I32(256)})
	if err != nil {
		t.Fatalf("resolve-addresses: %v", err)
	}
	return uint32(ret[0].Int32())
}

// TestACancelledLookupResolvesCancelledAndItsGoroutineEnds is an async-lowered lookup's cancel (ADR 0126), run under
// -race too, connect's shape: a lookup held in flight is cancelled by the guest's async `subtask.cancel`, which reads
// CANCELLED_BEFORE_RETURNED inline; the lookup's context is cancelled, so its goroutine returns, the registry has
// none left, and nothing is pending for the park bound.
//
// Neutered, run: cancelSubtask returning without claiming or resolving, and the cancel reads BLOCKED, the lookup
// left waiting on its context until the guard.
func TestACancelledLookupResolvesCancelledAndItsGoroutineEnds(t *testing.T) {
	s := newSockHarness(t)
	s.host.AllowNameLookup = true
	entered, returned := make(chan struct{}), make(chan error, 1)
	s.host.lookup = heldLookup(entered, returned)
	packed := lookupHarness(t, s, "held.example")
	if state := subtaskState(packed & 0xf); state != subtaskStarted {
		t.Fatalf("resolve-addresses returned state %d, want STARTED: the lookup is held", state)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("the lookup did not start within the 10s hang guard")
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
			t.Errorf("the lookup returned %v, want its context cancelled", err)
		}
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("the lookup was not cancelled within the 10s hang guard")
	}
	waited := make(chan struct{})
	go func() { s.res.net.wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("the lookup's goroutine did not end within the 10s hang guard")
	}
	s.h.mu.Lock()
	pending := s.h.sockPending
	s.h.mu.Unlock()
	if pending != 0 {
		t.Errorf("sockPending = %d after the cancel, want 0: the park bound would read a waker that is gone", pending)
	}
}

// TestCloseEndsAPendingLookupAndItsGoroutine is Close with a lookup pending (ADR 0126), run under -race too:
// `name-lookup.wasm`'s `run(0)` waits in its sync lower of a held lookup of localhost, and Close returns, the call
// ends, the lookup's context is cancelled, and no socket goroutine remains.
//
// Neutered, run: hostNet.stop skipping the lookups' cancel, and Close waits on the held lookup's goroutine until the
// hang guard.
func TestCloseEndsAPendingLookupAndItsGoroutine(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/name-lookup.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	entered, returned := make(chan struct{}), make(chan error, 1)
	h := NewHost(io.Discard, io.Discard, nil)
	h.AllowNameLookup = true
	h.lookup = heldLookup(entered, returned)
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, e := in.CallValues("run", canon.U32(0)); done <- e }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("the lookup did not start within the 10s hang guard")
	}
	a := in.w.async
	awaitPlace(t, a, "run waiting in its sync lower", func() {}, func() bool { return len(a.syncLowerCalls) == 1 })
	closed := make(chan struct{})
	go func() { in.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("Close did not return within the 10s hang guard, with a lookup pending")
	}
	select {
	case e := <-done:
		if e == nil {
			t.Error("run(0) returned a value after Close; want it ended")
		}
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("run(0) did not end within 10s of Close")
	}
	select {
	case e := <-returned:
		if !errors.Is(e, context.Canceled) {
			t.Errorf("the lookup returned %v, want its context cancelled by Close", e)
		}
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("the lookup was not cancelled within the 10s hang guard")
	}
	requireNoSocketGoroutines(t)
}

// TestALookupErrorMapsByTheWITsEquivalents is the resolver's error mapping (ADR 0126), at the WIT's POSIX
// equivalents, since no reading may need a DNS server: not found is name-unresolvable (EAI_NONAME), temporary or a
// timeout temporary-resolver-failure (EAI_AGAIN), any other DNS error permanent-resolver-failure (EAI_FAIL), and a
// non-DNS error other. An empty answer is name-unresolvable ("never succeeds with 0 results"), and an IPv4-mapped
// answer is unmapped ("never returns IPv4-mapped IPv6 addresses").
//
// Neutered, run: not-found mapped to permanent-resolver-failure, and the not-found row fails.
func TestALookupErrorMapsByTheWITsEquivalents(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"not found", &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}, "name-unresolvable"},
		{"temporary", &net.DNSError{Err: "server misbehaving", Name: "x", IsTemporary: true}, "temporary-resolver-failure"},
		{"timeout", &net.DNSError{Err: "i/o timeout", Name: "x", IsTimeout: true}, "temporary-resolver-failure"},
		{"other DNS", &net.DNSError{Err: "refused", Name: "x"}, "permanent-resolver-failure"},
		{"not DNS", errors.New("boom"), "other"},
	} {
		if got := lookupErrorLabel(c.err); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	if got, want := lookupAnswer(nil, nil), lookupErr("name-unresolvable"); !reflect.DeepEqual(got, want) {
		t.Errorf("an empty answer = %v, want name-unresolvable", got)
	}
	got := lookupAnswer([]netip.Addr{netip.MustParseAddr("::ffff:127.0.0.1")}, nil)
	want, err := lookupOK([]netip.Addr{netip.MustParseAddr("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("an IPv4-mapped answer = %v, want it unmapped to 127.0.0.1", got)
	}
}

// TestANameIsALiteralAnInvalidNameOrALookup is the inline half of resolve-addresses (ADR 0126): which names are IP
// literals, answered as given, which are invalid, refused before any lookup, and which go to the resolver. The
// literal and invalid rows wasmtime 49.0.3 measured are name-lookup.wasm's; these add the URL standard's other IPv4
// forms, the rule that a name ending in a number is an address or nothing, and the non-ASCII divergence.
func TestANameIsALiteralAnInvalidNameOrALookup(t *testing.T) {
	for _, c := range []struct {
		name string
		lit  string // the literal it is, or ""
		ok   bool   // a valid name for the resolver, when not a literal
	}{
		{"127.0.0.1", "127.0.0.1", false},
		{"::1", "::1", false},
		{"[::1]", "::1", false},
		{"::ffff:127.0.0.1", "::ffff:127.0.0.1", false},
		{"127.1", "127.0.0.1", false},
		{"0x7f.1", "127.0.0.1", false},
		{"0177.0.0.1", "127.0.0.1", false},
		{"2130706433", "127.0.0.1", false},
		{"127.0.0.1.", "127.0.0.1", false},
		{"fe80::1%1", "", false},
		{"[fe80::1%1]", "", false},
		{"[127.0.0.1]", "", false},
		{"1.2.3.256", "", false},
		{"foo.123", "", false},
		{"a b", "", false},
		{"", "", false},
		{"a%20b", "", false},
		{"bücher.example", "", false},
		{"localhost", "", true},
		{"foo..bar", "", true},
		{"-", "", true},
		{"123.foo", "", true},
	} {
		a, isLit := parseIPLiteral(c.name)
		switch {
		case c.lit != "":
			if !isLit || a != netip.MustParseAddr(c.lit) {
				t.Errorf("%q: literal %v (%v), want %s", c.name, a, isLit, c.lit)
			}
		case isLit:
			t.Errorf("%q: read as the literal %v, want no literal", c.name, a)
		case validLookupName(c.name) != c.ok:
			t.Errorf("%q: valid for the resolver = %v, want %v", c.name, !c.ok, c.ok)
		}
	}
}
