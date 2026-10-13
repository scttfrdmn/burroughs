package component

import (
	"encoding/binary"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestAResultStoreDoesNotLandWhileTheHolderIsInHostCode is the #983 exception's own witness: while the run
// slot's holder is in host code (`asyncHandles.inHost`), another goroutine's result store must not write
// guest memory underneath it.
//
// # The window
//
// #983's fix let a store skip the slot while its holder ran host code, because waiting there can deadlock:
// the holder may be the one resolving. That left a window. The holder's impl runs on its goroutine and can
// reach guest memory through `Caller`, ADR 0064's deliberately plain region. Meanwhile a resolver on ANOTHER
// goroutine wrote a result into that same memory, and nothing ordered the two. In the reference it cannot
// happen: the resolver's work is another thread's execution, and that cannot run while this thread is in its
// own host call.
//
// # The witness
//
// `run-slot.wasm`, heap-backed: two callers lower into retptr 0 and park. X alone is resolved, X wakes, and
// X's probe impl, host code on X's goroutine with the slot held, does three things:
//
//  1. writes a sentinel to retptr 0 through `c.Write`;
//  2. resolves Y's subtask on ANOTHER goroutine and waits for that call to return;
//  3. reads retptr 0 back through `c.Read`.
//
// **The sentinel must survive**: Y's store may not land while X is in host code. It is applied when X leaves
// host code, on X's goroutine, before X's guest resumes. The assertion is the memory X itself read, not a
// clock.
//
// **Neuter, run:** writing directly while `inHost` is raised, as #983's first fix did, lets Y's store
// overwrite the sentinel, and this fails on the read-back.
func TestAResultStoreDoesNotLandWhileTheHolderIsInHostCode(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/run-slot.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	const sentinel = 0xA5A5A5A5
	resolvers := make(chan func(canon.Value), 2)
	var mu sync.Mutex
	calls := 0
	var readBack uint32
	var yResolve func(canon.Value)

	h := NewHost(io.Discard, io.Discard, nil)
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:async/ops::op": func(c *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			switch {
			case n <= 2: // each caller's first call: deferred, so it waits on the set
				resolvers <- onResolve
			case n == 3: // X's probe: host code, slot held
				var w [4]byte
				binary.LittleEndian.PutUint32(w[:], sentinel)
				if werr := c.Write(0, w[:]); werr != nil {
					return nil, werr
				}
				done := make(chan struct{})
				go func() { yResolve(canon.U32(2)); close(done) }() // Y's store, from another goroutine
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					return nil, errResolverHung
				}
				r, rerr := c.Read(0, 4)
				if rerr != nil {
					return nil, rerr
				}
				mu.Lock()
				readBack = binary.LittleEndian.Uint32(r)
				mu.Unlock()
				onResolve(canon.U32(3))
			default: // Y's probe
				onResolve(canon.U32(4))
			}
			return func() {}, nil
		},
	}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	done := make(chan error, 2)
	for range 2 {
		go func() { _, e := in.CallValues("run"); done <- e }()
	}
	var rs []func(canon.Value)
	for len(rs) < 2 {
		select {
		case r := <-resolvers:
			rs = append(rs, r)
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of 2 callers reached the import", len(rs))
		}
	}
	waitFor(t, "both callers parked", func() bool { return parkedWaiters(in.w.async) == 2 })
	yResolve = rs[1]
	rs[0](canon.U32(1)) // X

	for range 2 {
		select {
		case e := <-done:
			if e != nil {
				t.Fatalf("run: %v", e)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("a caller never returned")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 4 {
		t.Fatalf("%d calls reached the import; want 4, so the probe never ran", calls)
	}
	if readBack != sentinel {
		t.Fatalf("X read back %#x where it wrote %#x: Y's result store landed in guest memory while X, the "+
			"run slot's holder, was in host code — two goroutines writing one guest word with nothing "+
			"ordering them, which the reference cannot produce", readBack, uint32(sentinel))
	}
}

// errResolverHung is the probe's own refusal when the resolver it started never returns.
var errResolverHung = errorString("the resolver started inside the probe never returned")

type errorString string

func (e errorString) Error() string { return string(e) }
