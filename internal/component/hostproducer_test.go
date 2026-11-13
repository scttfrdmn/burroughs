// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// gatedReader is a stdin whose Read blocks until the test feeds it, so a test waits on PLACES rather than on a
// clock: `entered` receives each Read's buffer length as the Read starts, and the Read returns what `feed`
// hands it.
type gatedReader struct {
	entered chan int
	feed    chan gatedRead
}

type gatedRead struct {
	data []byte
	err  error
}

func newGatedReader() *gatedReader {
	return &gatedReader{entered: make(chan int, 16), feed: make(chan gatedRead)}
}

func (g *gatedReader) Read(p []byte) (int, error) {
	g.entered <- len(p)
	r := <-g.feed
	return copy(p, r.data), r.err
}

// awaitRead waits for the producer's next Read to start and returns its buffer length.
func (g *gatedReader) awaitRead(t *testing.T) int {
	t.Helper()
	select {
	case n := <-g.entered:
		return n
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("the producer started no Read within the 10s hang guard")
		return 0
	}
}

// awaitProducersIdle waits until no producer Read of h is in flight or awaiting its delivery: the place where
// a Read's result has been stored or discarded. A read of the count under the table's lock, polled, with a
// hang guard.
func awaitProducersIdle(t *testing.T, h *asyncHandles) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second) // a harness hang guard, not a measurement
	for {
		h.mu.Lock()
		n := h.producerReads
		h.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d producer read(s) still in flight after the 10s hang guard", n)
		}
		runtime.Gosched()
	}
}

// producerHarness is read-via-stream called at the built-ins, in the async-stream tests' harness: one handle
// table, one test caller, and the stream's and future's handles as the host wrote them at the return pointer.
type producerHarness struct {
	h      *asyncHandles
	cc     *interp.CanonCaller
	ri, fi uint32
	src    *gatedReader
}

func newProducerHarness(t *testing.T) producerHarness {
	t.Helper()
	h := newAsyncHandles()
	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatalf("harness caller: %v", err)
	}
	src := newGatedReader()
	if _, rerr := NewHost(io.Discard, io.Discard, src).readViaStream(h)(cc, []interp.Value{interp.I32(0)}); rerr != nil {
		t.Fatalf("read-via-stream: %v", rerr)
	}
	b, err := cc.Read(0, 8)
	if err != nil {
		t.Fatalf("reading the handles: %v", err)
	}
	return producerHarness{h: h, cc: cc, ri: binary.LittleEndian.Uint32(b[0:]), fi: binary.LittleEndian.Uint32(b[4:]), src: src}
}

func (p producerHarness) read(t *testing.T, ptr, n uint32) uint32 {
	t.Helper()
	ret, err := streamRead(p.h)(p.cc, []interp.Value{interp.I32(int32(p.ri)), interp.I32(int32(ptr)), interp.I32(int32(n))})
	if err != nil {
		t.Fatalf("stream.read(%d, %d): %v", ptr, n, err)
	}
	return uint32(ret[0].Int32())
}

func (p producerHarness) bytesAt(t *testing.T, ptr, n uint32) string {
	t.Helper()
	b, err := p.cc.Read(uint64(ptr), uint64(n))
	if err != nil {
		t.Fatalf("reading guest memory: %v", err)
	}
	return string(b)
}

// TestAStreamReadCancelledMidReadServesTheBufferedBytesNext is the producer's no-loss rule (ADR 0122): a read
// cancelled while the host's Read is in flight leaves what that Read returns in the producer's buffer, and the
// next reads are served from it, in order, before anything more is read from the source. The Read is sized at
// the guest's request, so a byte the guest never asked for is never consumed.
//
// Neutered, run: copyIntoLocked dropping the rest of the buffer after a copy, and the read after the 4-byte one
// parks BLOCKED with "ef" lost; apply discarding a delivery with no read pending, and the first read after the
// cancel parks BLOCKED with all six bytes lost.
func TestAStreamReadCancelledMidReadServesTheBufferedBytesNext(t *testing.T) {
	p := newProducerHarness(t)
	if got := p.read(t, 64, 8); got != asyncBlocked {
		t.Fatalf("the first read = %#x, want BLOCKED: nothing has been read yet", got)
	}
	if n := p.src.awaitRead(t); n != 8 {
		t.Errorf("the producer's Read asked for %d bytes, want the read's 8: it reads on demand", n)
	}
	ret, err := streamCancelRead(p.h)(p.cc, []interp.Value{interp.I32(int32(p.ri))})
	if err != nil {
		t.Fatalf("stream.cancel-read: %v", err)
	}
	if got := uint32(ret[0].Int32()); got != uint32(copyCancelled) {
		t.Fatalf("the cancel = %#x, want CANCELLED with progress 0", got)
	}
	p.src.feed <- gatedRead{data: []byte("abcdef")}
	awaitProducersIdle(t, p.h)
	if got := p.read(t, 128, 4); got != 4<<4 {
		t.Fatalf("the read after the cancel = %#x, want COMPLETED with progress 4 from the buffer, inline", got)
	}
	if got := p.bytesAt(t, 128, 4); got != "abcd" {
		t.Errorf("the read after the cancel delivered %q, want \"abcd\"", got)
	}
	if got := p.read(t, 128, 8); got != 2<<4 {
		t.Fatalf("the next read = %#x, want COMPLETED with progress 2: the buffer's last two bytes", got)
	}
	if got := p.bytesAt(t, 128, 2); got != "ef" {
		t.Errorf("the next read delivered %q, want \"ef\"", got)
	}
	select {
	case n := <-p.src.entered:
		t.Fatalf("the source was read again (%d bytes) while the buffer still held bytes", n)
	default:
	}
	// With the buffer empty, the next read asks the source again, and the end of input finishes the stream.
	if got := p.read(t, 128, 8); got != asyncBlocked {
		t.Fatalf("the read with the buffer empty = %#x, want BLOCKED", got)
	}
	p.src.awaitRead(t)
	p.src.feed <- gatedRead{err: io.EOF}
	awaitProducersIdle(t, p.h)
	p.h.mu.Lock()
	re, _ := handleAt[*readableStreamEnd](p.h, p.ri)
	ev, ok := re.pendingEventLocked()
	p.h.mu.Unlock()
	if !ok || ev.p2 != uint32(copyDropped) {
		t.Errorf("the read pending at the end of input has event %+v (taken %v), want DROPPED with progress 0", ev, ok)
	}
}

// TestAGuestDropOfTheProducedStreamResolvesTheFutureOK is wasi:cli/stdin's own rule (ADR 0122): *"If the
// stream's readable end is dropped the future will resolve to success."* The guest cancels its read, with the
// host's Read in flight, and drops the readable end; the future then reads `ok` inline, and the late Read's bytes
// are discarded rather than stored.
//
// Neutered, run: streamDropReadable not telling the producer, and the future read parks BLOCKED.
func TestAGuestDropOfTheProducedStreamResolvesTheFutureOK(t *testing.T) {
	p := newProducerHarness(t)
	if got := p.read(t, 64, 8); got != asyncBlocked {
		t.Fatalf("the first read = %#x, want BLOCKED", got)
	}
	p.src.awaitRead(t)
	if _, err := streamCancelRead(p.h)(p.cc, []interp.Value{interp.I32(int32(p.ri))}); err != nil {
		t.Fatalf("stream.cancel-read: %v", err)
	}
	if _, err := streamDropReadable(p.h)(p.cc, []interp.Value{interp.I32(int32(p.ri))}); err != nil {
		t.Fatalf("stream.drop-readable: %v", err)
	}
	const fptr = 200
	if err := p.cc.Write(fptr, []byte{0xAA, 0xAA}); err != nil {
		t.Fatalf("seeding the future's result: %v", err)
	}
	ret, err := futureRead(p.h)(p.cc, []interp.Value{interp.I32(int32(p.fi)), interp.I32(fptr)})
	if err != nil {
		t.Fatalf("future.read: %v", err)
	}
	if got := uint32(ret[0].Int32()); got != uint32(copyCompleted) {
		t.Fatalf("the future read = %#x, want COMPLETED inline: the drop resolves it", got)
	}
	if got := p.bytesAt(t, fptr, 1); got != "\x00" {
		t.Errorf("the future's result discriminant = %q, want 0 (ok)", got)
	}
	p.src.feed <- gatedRead{data: []byte("xyz")}
	awaitProducersIdle(t, p.h)
	if got := p.bytesAt(t, 64, 8); got != strings.Repeat("\x00", 8) {
		t.Errorf("the dropped read's buffer holds %q after the late Read, want it untouched", got)
	}
}

// TestAWritersDropUnlinksItsReader is #1264 (ADR 0122), guest to guest at the built-ins in stream_idle_test's
// style: the reference's End.drop unlinks both ends and notifies the other unless it is DONE or already has an
// event (def:End.drop), and the event is computed at the take (def:StreamEnd.notify.stream_event).
//
// Neutered, each run: the reader's DROPPED-at-the-take conversion dropped, and the untaken COMPLETED is taken as
// COMPLETED, the reader left IDLE; dropWriterLocked leaving `conn` set and notifying only a pending read, and
// that, the read after the drop parking BLOCKED, and write-via-stream's unresolved future all fail;
// write-via-stream's dropped-writer arm dropped, and its future never resolves; the writer's conversion dropped,
// and its untaken COMPLETED is taken as COMPLETED; streamWrite's reader-dropped arm dropped, and the write after
// the reader's drop parks BLOCKED.
func TestAWritersDropUnlinksItsReader(t *testing.T) {
	type pair struct {
		h      *asyncHandles
		cc     *interp.CanonCaller
		ri, wi uint32
	}
	const dst, src = 40, 104
	newPair := func(t *testing.T) pair {
		t.Helper()
		h := newAsyncHandles()
		cc, err := interp.NewCanonCallerForTest(1)
		if err != nil {
			t.Fatalf("harness caller: %v", err)
		}
		ret, err := streamNew(h)(cc, nil)
		if err != nil {
			t.Fatalf("stream.new: %v", err)
		}
		packed := uint64(ret[0].Int64())
		if werr := cc.Write(src, []byte("abcdefgh")); werr != nil {
			t.Fatalf("seeding src: %v", werr)
		}
		return pair{h: h, cc: cc, ri: uint32(packed & 0xffffffff), wi: uint32(packed >> 32)}
	}
	call := func(t *testing.T, p pair, fn func(*asyncHandles) interp.CanonFunc, args ...uint32) uint32 {
		t.Helper()
		vs := make([]interp.Value, len(args))
		for i, a := range args {
			vs[i] = interp.I32(int32(a))
		}
		ret, err := fn(p.h)(p.cc, vs)
		if err != nil {
			t.Fatalf("built-in(%v): %v", args, err)
		}
		if len(ret) == 0 {
			return 0
		}
		return uint32(ret[0].Int32())
	}

	t.Run("an untaken COMPLETED at the drop is taken as DROPPED with its progress", func(t *testing.T) {
		p := newPair(t)
		if got := call(t, p, streamRead, p.ri, dst, 8); got != asyncBlocked {
			t.Fatalf("read with no writer = %#x, want BLOCKED", got)
		}
		if got := call(t, p, streamWrite, p.wi, src, 3); got != 3<<4 {
			t.Fatalf("the write at the parked read = %#x, want COMPLETED with progress 3", got)
		}
		call(t, p, streamDropWritable, p.wi)
		p.h.mu.Lock()
		re, _ := handleAt[*readableStreamEnd](p.h, p.ri)
		ev, ok := re.pendingEventLocked()
		state := re.endStateName()
		p.h.mu.Unlock()
		if !ok || ev.p2 != uint32(copyDropped)|3<<4 {
			t.Errorf("the reader's event = %+v (taken %v), want DROPPED with progress 3 (%#x)", ev, ok, uint32(copyDropped)|3<<4)
		}
		if state != "DONE" {
			t.Errorf("the reader is %s after taking it, want DONE", state)
		}
		if b, err := p.cc.Read(dst, 3); err != nil || string(b) != "abc" {
			t.Errorf("the copy delivered %q (%v), want \"abc\": a guest reader's copy reads its source", b, err)
		}
	})

	t.Run("a read after the drop is DROPPED inline", func(t *testing.T) {
		p := newPair(t)
		call(t, p, streamDropWritable, p.wi)
		if got := call(t, p, streamRead, p.ri, dst, 8); got != uint32(copyDropped) {
			t.Errorf("the read after the writer's drop = %#x, want DROPPED (1) inline", got)
		}
	})

	t.Run("write-via-stream after the drop resolves its future", func(t *testing.T) {
		p := newPair(t)
		call(t, p, streamDropWritable, p.wi)
		fi := call(t, p, func(h *asyncHandles) interp.CanonFunc { return writeViaStreamTo(io.Discard, h) }, p.ri)
		p.h.mu.Lock()
		fe, _ := handleAt[*readableFutureEnd](p.h, fi)
		resolved := fe != nil && fe.resolved && fe.result == copyCompleted
		p.h.mu.Unlock()
		if !resolved {
			t.Error("write-via-stream's future is unresolved with the writer already gone (#1264)")
		}
	})

	t.Run("a writer's untaken COMPLETED at the reader's drop is taken as DROPPED with its progress", func(t *testing.T) {
		p := newPair(t)
		if got := call(t, p, streamWrite, p.wi, src, 5); got != asyncBlocked {
			t.Fatalf("write with no reader = %#x, want BLOCKED", got)
		}
		if got := call(t, p, streamRead, p.ri, dst, 3); got != 3<<4 {
			t.Fatalf("the read at the parked write = %#x, want COMPLETED with progress 3", got)
		}
		call(t, p, streamDropReadable, p.ri)
		p.h.mu.Lock()
		we, _ := handleAt[*writableStreamEnd](p.h, p.wi)
		ev, ok := we.pendingEventLocked()
		state := we.endStateName()
		p.h.mu.Unlock()
		if !ok || ev.p2 != uint32(copyDropped)|3<<4 {
			t.Errorf("the writer's event = %+v (taken %v), want DROPPED with progress 3 (%#x)", ev, ok, uint32(copyDropped)|3<<4)
		}
		if state != "DONE" {
			t.Errorf("the writer is %s after taking it, want DONE", state)
		}
	})

	t.Run("a write after the reader's drop is DROPPED inline", func(t *testing.T) {
		p := newPair(t)
		call(t, p, streamDropReadable, p.ri)
		if got := call(t, p, streamWrite, p.wi, src, 3); got != uint32(copyDropped) {
			t.Errorf("the write after the reader's drop = %#x, want DROPPED (1) inline", got)
		}
	})
}

// TestAGuestStreamReadFollowsEndCopysArms is stream.read guest to guest (ADR 0122), at the built-ins: the
// reference's End.copy arms seen from a guest reader (def:End.copy), the zero-length ones included, which no
// reader could reach while every reader was the host's.
//
//   - a read at a parked write copies `min` inline and completes both;
//   - a read at a parked write of 0 completes the write with 0 and parks;
//   - a read of 0 at a parked write completes itself with 0 inline, and the write stays parked;
//   - a write of 3 at a pending read of 0 notifies the reader with 0 and parks the write.
//
// Neutered, each run: streamRead's write-of-0 arm dropped, and the read at it copies nothing and completes; its
// read-of-0 arm dropped, and the parked write is completed with 0 too; streamWrite's pending-read-of-0 arm
// dropped, and the write of 3 completes inline with progress 0.
func TestAGuestStreamReadFollowsEndCopysArms(t *testing.T) {
	const dst, src = 40, 104
	setup := func(t *testing.T) (*asyncHandles, *interp.CanonCaller, uint32, uint32) {
		t.Helper()
		h := newAsyncHandles()
		cc, err := interp.NewCanonCallerForTest(1)
		if err != nil {
			t.Fatalf("harness caller: %v", err)
		}
		ret, err := streamNew(h)(cc, nil)
		if err != nil {
			t.Fatalf("stream.new: %v", err)
		}
		if werr := cc.Write(src, []byte("abcdefgh")); werr != nil {
			t.Fatalf("seeding src: %v", werr)
		}
		packed := uint64(ret[0].Int64())
		return h, cc, uint32(packed & 0xffffffff), uint32(packed >> 32)
	}
	op := func(t *testing.T, h *asyncHandles, cc *interp.CanonCaller, fn func(*asyncHandles) interp.CanonFunc, a ...uint32) uint32 {
		t.Helper()
		vs := make([]interp.Value, len(a))
		for i, x := range a {
			vs[i] = interp.I32(int32(x))
		}
		ret, err := fn(h)(cc, vs)
		if err != nil {
			t.Fatalf("built-in(%v): %v", a, err)
		}
		return uint32(ret[0].Int32())
	}
	resolved := func(h *asyncHandles, w waitable) bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return w.hasPendingEventLocked()
	}

	t.Run("a read at a parked write copies inline", func(t *testing.T) {
		h, cc, ri, wi := setup(t)
		if got := op(t, h, cc, streamWrite, wi, src, 5); got != asyncBlocked {
			t.Fatalf("write with no reader = %#x, want BLOCKED", got)
		}
		if got := op(t, h, cc, streamRead, ri, dst, 3); got != 3<<4 {
			t.Fatalf("read of 3 at a parked write of 5 = %#x, want COMPLETED with progress 3", got)
		}
		if b, _ := cc.Read(dst, 3); string(b) != "abc" {
			t.Errorf("the read delivered %q, want \"abc\"", b)
		}
	})
	t.Run("a read at a parked write of 0 completes the write and parks", func(t *testing.T) {
		h, cc, ri, wi := setup(t)
		op(t, h, cc, streamWrite, wi, src, 0)
		if got := op(t, h, cc, streamRead, ri, dst, 8); got != asyncBlocked {
			t.Errorf("read at a parked write of 0 = %#x, want BLOCKED", got)
		}
		we, _ := handleAt[*writableStreamEnd](h, wi)
		if !resolved(h, we) {
			t.Error("the parked write of 0 was not completed by the read")
		}
	})
	t.Run("a read of 0 at a parked write completes itself and leaves the write", func(t *testing.T) {
		h, cc, ri, wi := setup(t)
		op(t, h, cc, streamWrite, wi, src, 3)
		if got := op(t, h, cc, streamRead, ri, dst, 0); got != 0 {
			t.Errorf("read of 0 at a parked write = %#x, want COMPLETED with progress 0 inline", got)
		}
		we, _ := handleAt[*writableStreamEnd](h, wi)
		if resolved(h, we) {
			t.Error("the read of 0 completed the parked write; the reference leaves it parked")
		}
	})
	t.Run("a write at a pending read of 0 notifies the reader and parks", func(t *testing.T) {
		h, cc, ri, wi := setup(t)
		if got := op(t, h, cc, streamRead, ri, dst, 0); got != asyncBlocked {
			t.Fatalf("read of 0 with no writer = %#x, want BLOCKED", got)
		}
		if got := op(t, h, cc, streamWrite, wi, src, 3); got != asyncBlocked {
			t.Errorf("write of 3 at a pending read of 0 = %#x, want BLOCKED", got)
		}
		re, _ := handleAt[*readableStreamEnd](h, ri)
		if !resolved(h, re) {
			t.Error("the pending read of 0 was not notified by the write")
		}
	})
}

// TestStreamReadIsBoundAndItsSyncFormRefused is stream.read's binding (ADR 0122): its async form permits, its
// sync form is refused by name, and so is a read of a stream whose element is not one byte.
//
// Neutered, each run: unbuiltAsyncSurface's sync-form arm dropped, and stream-read-sync.wasm instantiates; the
// walk's element check dropped, and stream-read-u32.wasm instantiates.
func TestStreamReadIsBoundAndItsSyncFormRefused(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	async := &Component{Canons: []Canon{{Kind: CanonAsyncBuiltin, AsyncOp: 0x0f, AsyncForm: true}}}
	if err := gateAsync(async); err != nil {
		t.Errorf("an async stream.read was refused: %v", err)
	}
	for _, c := range []struct {
		fixture string
		target  error
		names   string
	}{
		{"stream-read-sync", ErrAsyncNotImplemented, "synchronous stream.read"},
		{"stream-read-u32", ErrUnsupportedForm, "not one byte"},
	} {
		t.Run(c.fixture, func(t *testing.T) {
			b, err := os.ReadFile("testdata/" + c.fixture + ".wasm")
			if err != nil {
				t.Fatalf("the committed fixture is missing: %v", err)
			}
			in, err := Instantiate(b)
			if err == nil {
				in.Close()
				t.Fatalf("%s instantiated; want it refused by name", c.fixture)
			}
			if !errors.Is(err, c.target) || !strings.Contains(err.Error(), c.names) {
				t.Errorf("%s: err = %v, want %v naming %q", c.fixture, err, c.target, c.names)
			}
		})
	}
}

// TestReadViaStreamMustBeDeclaredAsTheHostDeclaresIt is the producer's half of ADR 0118's agreement (ADR 0122):
// the host writes bytes into the stream and lowers its future at `result<_, error-code>`, so a guest whose
// read-via-stream returns anything but tuple<stream<u8>, future<that>> is refused at instantiate.
// read-park.wasm, which declares the host's, is the control.
//
// Neutered, run: the tuple check skipped, and read-via-stream-u32.wasm instantiates.
func TestReadViaStreamMustBeDeclaredAsTheHostDeclaresIt(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	for _, c := range []struct {
		fixture string
		refused bool
	}{{"read-park", false}, {"read-via-stream-u32", true}} {
		b, err := os.ReadFile("testdata/" + c.fixture + ".wasm")
		if err != nil {
			t.Fatalf("the committed fixture is missing: %v", err)
		}
		in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
		switch {
		case c.refused && err == nil:
			in.Close()
			t.Errorf("%s instantiated; want it refused for its stream<u32>", c.fixture)
		case c.refused && (!errors.Is(err, ErrLinkRefused) || !strings.Contains(err.Error(), "tuple<stream<u8>")):
			t.Errorf("%s: err = %v, want ErrLinkRefused naming the host's tuple<stream<u8>, …>", c.fixture, err)
		case !c.refused && err != nil:
			t.Errorf("%s, the control, was refused: %v", c.fixture, err)
		case !c.refused:
			in.Close()
		}
	}
}

// readParkReading reads one row of testdata/read-park.reading, wasmtime 49.0.3's.
func readParkReading(t *testing.T, row string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/read-park.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(ln, row+": "); ok {
			return rest
		}
	}
	t.Fatalf("read-park.reading has no row %q", row)
	return ""
}

// TestReadParkMatchesWasmtime is read-park.wasm's rows against wasmtime 49.0.3 (ADR 0122), in both gate
// states: a callback-lifted read of 8 bytes on stdin "abc" (COMPLETED, progress 3), on an empty stdin and on
// none (BLOCKED, then DROPPED with progress 0: the writer's drop at the end of input), and on 12 bytes
// (COMPLETED, progress 8, the read's length); a second read, which blocks rather than being served from bytes
// read ahead, since the host reads only what a read asks for; and the sync-lifted read's BLOCKED with the Read
// in flight.
//
// Neutered, run: readLocked finishing first and taking the DROPPED inline at the end of input, and the `none`
// row's guest waits for an event that was already taken, until the hang guard.
func TestReadParkMatchesWasmtime(t *testing.T) {
	b, err := os.ReadFile("testdata/read-park.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	for _, gate := range []string{"0", "1"} {
		for _, c := range []struct {
			name, export, row string
			stdin             func() io.Reader
		}{
			{"abc", "run", "run abc", func() io.Reader { return strings.NewReader("abc") }},
			{"empty", "run", "run empty", func() io.Reader { return strings.NewReader("") }},
			{"none", "run", "run devnull", func() io.Reader { return nil }},
			{"12-bytes", "run", "run abcdefghijkl", func() io.Reader { return strings.NewReader("abcdefghijkl") }},
			{"twice-abcdef", "twice", "twice abcdef", func() io.Reader { return strings.NewReader("abcdef") }},
			{"twice-a", "twice", "twice a", func() io.Reader { return strings.NewReader("a") }},
			{"start", "start", "start abc", func() io.Reader { return strings.NewReader("abc") }},
		} {
			t.Run(c.name+"/cm-threads="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				want := readParkReading(t, c.row)
				in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, c.stdin()))
				if err != nil {
					t.Fatalf("instantiate: %v", err)
				}
				defer in.Close()
				res, err := callWithin(t, in, c.export)
				if err != nil {
					t.Fatalf("%s() = %v", c.export, err)
				}
				v, _ := res[0].U32()
				if got := fmtU32(v); got != want {
					t.Errorf("%s() = %s, want wasmtime's %s", c.export, got, want)
				}
			})
		}
	}
}

// fmtU32 prints v as wasmtime's --invoke prints a u32: in decimal.
func fmtU32(v uint32) string { return strconv.FormatUint(uint64(v), 10) }

// TestAPendingProducerReadOutlivesTheParkBound is the park bound's producer arm (ADR 0122): read-park.wasm's
// callback-lifted `run` waits on a read whose Read the source answers only after liftParkBound, shortened to
// 5ms, has passed several times. The Read in flight is a waker, so the bound re-arms and the read completes,
// reading 131120 (STREAM_READ, COMPLETED with progress 3), as wasmtime does.
//
// Neutered, run: hostWaitPendingLocked without the producer term, and run() fails with ErrLiftParkExpired.
func TestAPendingProducerReadOutlivesTheParkBound(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	prev := liftParkBound
	liftParkBound = 5 * time.Millisecond
	defer func() { liftParkBound = prev }()
	b, err := os.ReadFile("testdata/read-park.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	src := newGatedReader()
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, src))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	go func() {
		<-src.entered
		time.Sleep(60 * time.Millisecond) // the setup's delay, past the bound twelve times; nothing is asserted on it
		src.feed <- gatedRead{data: []byte("abc")}
	}()
	res, err := callWithin(t, in, "run")
	if err != nil {
		t.Fatalf("run() = %v", err)
	}
	if v, _ := res[0].U32(); fmtU32(v) != readParkReading(t, "run abc") {
		t.Errorf("run() = %d, want wasmtime's %s", v, readParkReading(t, "run abc"))
	}
}

// TestCloseAbandonsAProducerBlockedInRead is Close's producer rule (ADR 0122), run under -race too:
// read-park.wasm's `start` leaves a read pending with the host's Read blocked on an io.Pipe nobody writes. Close
// returns without waiting for it, and a write to the pipe after Close, which the abandoned Read takes, stores
// nothing into the instance: the reader is not armed and its buffer is untouched.
//
// Neutered, run: walker.close without stopAllProducers (and the producer's two stop checks), and the late bytes
// are stored: the reader is armed COMPLETED with "late" in its buffer.
func TestCloseAbandonsAProducerBlockedInRead(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/read-park.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	pr, pw := io.Pipe()
	entered := make(chan struct{}, 1)
	src := readerFunc(func(p []byte) (int, error) {
		entered <- struct{}{}
		return pr.Read(p)
	})
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, src))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	res, err := callWithin(t, in, "start")
	if err != nil {
		in.Close()
		t.Fatalf("start() = %v", err)
	}
	if v, _ := res[0].U32(); v != asyncBlocked {
		in.Close()
		t.Fatalf("start() = %#x, want BLOCKED with the Read in flight", v)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		in.Close()
		t.Fatal("the producer started no Read within the 10s hang guard")
	}
	h := in.w.async
	h.mu.Lock()
	re, _ := handleAt[*readableStreamEnd](h, 1)
	h.mu.Unlock()
	if re == nil {
		in.Close()
		t.Fatal("start() left no readable stream end at handle 1")
	}

	closed := make(chan struct{})
	go func() { in.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("Close did not return with the producer blocked in Read: it waited on the Read")
	}

	if _, err := pw.Write([]byte("late")); err != nil { // returns once the abandoned Read has taken the bytes
		t.Fatalf("writing the pipe after Close: %v", err)
	}
	awaitProducersIdle(t, h)
	h.mu.Lock()
	armed := re.resolved
	h.mu.Unlock()
	if armed {
		t.Error("the abandoned Read's bytes armed the reader of a closed instance")
	}
	if got, err := re.caller.Read(64, 8); err == nil && !bytes.Equal(got, make([]byte, 8)) {
		t.Errorf("the closed instance's read buffer holds %q; the abandoned Read stored into it", got)
	}
}

// readerFunc adapts a function to io.Reader.
type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }
