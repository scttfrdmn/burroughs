// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// guestHeap adapts a CanonCaller to canon.Heap, so the shared value lowerings (canon.StoreString, and
// the list/record lowerings as #718 extends the set) write into guest memory through the canon lower's
// bound realloc and memory — the same code the differential runs against the model heap (#719, ADR
// 0083's implement-once). Offsets are guest byte positions.
type guestHeap struct{ c *interp.CanonCaller }

func (g guestHeap) Realloc(origPtr, origSize, align, newSize int) (int, error) {
	p, err := g.c.Realloc(uint32(origPtr), uint32(origSize), uint32(align), uint32(newSize))
	return int(p), err
}

func (g guestHeap) WriteBytes(ptr int, data []byte) error {
	return g.c.Write(uint64(uint32(ptr)), data)
}

func (g guestHeap) StoreInt(v uint64, ptr, nbytes int) error {
	buf := make([]byte, nbytes)
	for i := range nbytes {
		buf[i] = byte(v >> (8 * i))
	}
	return g.c.Write(uint64(uint32(ptr)), buf)
}

// ReadBytes makes a guestHeap a [canon.ReadHeap] too, so the codec's **lifting** arms reach guest memory
// the way its lowering arms already did (#903). `CanonCaller.Read` performs the bounds check against the
// instance's memory, which is the error the codec's string lift delegates its def:load_string_from_range:"trap_if(ptr + byte_length > len(cx.opts.memory))" trap to.
//
// A negative offset cannot reach a guest pointer — the two words a string's flat form carries are
// unsigned i32s — but it is refused rather than converted, because `int` is the codec's offset type and
// `uint64(uint32(negative))` would turn a programming error into a plausible address.
func (g guestHeap) ReadBytes(ptr, n int) ([]byte, error) {
	if ptr < 0 || n < 0 {
		return nil, fmt.Errorf("component: guest read of %d byte(s) at %d: a negative offset is not an address", n, ptr)
	}
	return g.c.Read(uint64(uint32(ptr)), uint64(uint32(n)))
}

// CheckRange validates a span without copying it, which is why `canon.ReadHeap` carries it separately
// from `ReadBytes`: **this** implementation's `ReadBytes` allocates and copies, so using it for a framing
// check over a guest-supplied length would copy up to the whole addressable memory and throw it away.
//
// `CanonCaller.CheckRange` reuses the engine's own effective-address and bounds logic and discards the
// sub-slice it validates, so the condition is the same one an element read would meet.
func (g guestHeap) CheckRange(ptr, n int) error {
	if ptr < 0 || n < 0 {
		return fmt.Errorf("component: guest range of %d byte(s) at %d: a negative offset is not an address", n, ptr)
	}
	return g.c.CheckRange(uint64(uint32(ptr)), uint64(uint32(n)))
}

// The real preview-2 host (PR C.2). The stub host (link_component.go) refuses every wasi import by name;
// this host provides the ten the guest-driven `wasi:cli/run` world reaches. Each import is a canon
// lower, so its impl is a `interp.CanonFunc` dispatched through the canonical-ABI adapter
// (`interp.CanonLowerExtern`, ADR 0084 / §5 H-2 amended): the impl marshals against the memory the
// lower's option names — bound into the `CanonCaller`, since the guest reaches these through a
// memory-less `$imports` trampoline — and, where it lowers a host-produced list, allocates its backing
// through the lower's `cabi_realloc` as agent execution (`CanonCaller.Realloc`).
//
// A stream's `result<_, stream-error>` and `result<u64, stream-error>` flatten past one core value, so
// the guest passes a return pointer and the host writes the lifted result there; the `ok` arm is
// written and the `err` arm is unreachable on the success path (its `own<error>` payload is refused at
// lower time — the deferral recorded on #694). A byte string crosses through the codec's `list<u8>`
// semantics: the guest hands a (ptr, len) pair and the host reads exactly those bytes from the bound
// memory. Stream and error handles are entries in the calling instance's own handle table (ADR 0117,
// hostresource.go); a borrow of one is lent for the call and resolves to the stream it names.

// Host is a preview-2 host: the standard streams, and the resource types and imports a guest reaches them
// through. It holds no handles: those are per component instance (ADR 0117), so one Host can serve several
// instances without one's handle meaning anything in another's. Exited/ExitCode record a guest `exit` or
// `exit-with-code` call.
type Host struct {
	Stdout   io.Writer
	Stderr   io.Writer
	Stdin    io.Reader
	Args     []string // the guest's argv, lowered by get-arguments
	Exited   bool
	ExitCode int32
	// InheritNetwork grants wasi:sockets TCP any address (ADR 0123), wasmtime's `-S inherit-network`. False, the
	// zero value, denies the network: `tcp-socket.create` answers `access-denied`.
	InheritNetwork bool
	// dial is connect's dialer, nil for net.Dialer's. Test-only, so a dial can be held in flight to be cancelled.
	dial func(ctx context.Context, network, address string) (net.Conn, error)
	// start is the monotonic clock's origin and seed is `get-insecure-seed`'s pair (ADR 0119), both set by
	// NewHost, the only constructor. A zero start is preview 1's #815: time.Since saturates, and the clock
	// stands still.
	start time.Time
	seed  [2]uint64
	// stdinReads serialises the WASI 0.3 stdin producers' Reads of Stdin (ADR 0122), so two streams from two
	// read-via-stream calls never Read one io.Reader concurrently. The WIT leaves concurrent streams
	// "implementation-specific"; a reader that is not safe for concurrent use is not the guest's problem.
	stdinReads sync.Mutex
	// resourceTypeImpls and resourceImpls are test-only extra resource types and imports over them (ADR
	// 0117), the resource half's counterpart of syncImpls, merged into resourceSource() under the same
	// no-shadowing rule. Their keys are unversioned, so each is a wildcard (lookupHost).
	resourceTypeImpls map[string]*hostResource
	resourceImpls     map[string]resourceMethod
	// asyncImpls is the test-only extra async-lower impl source, kept SEPARATE from wasi(): an async impl
	// has a distinct signature (asyncLowerImpl — it can leave its subtask pending), so a single source
	// returning both would let a wrong-source call site compile. It is merged into asyncWasi() under
	// wasi()'s no-shadowing rule, as syncImpls is into wasi(), and its keys are unversioned wildcards
	// (lookupHost). This said "empty in production (no real async impls until 2b)" until the production
	// async imports landed in asyncSource() (ADR 0121), when this stopped being the whole async source.
	asyncImpls map[string]asyncLowerImpl
	// syncImpls is the extra-sync-impl source, merged into wasi() (#863). Kept as its own field rather
	// than by widening wasi()'s literal, so the WASI capability set stays one readable block and an extra
	// cannot be mistaken for one of it. A name WASI already serves is refused at merge, never shadowed.
	//
	// Needed because a guest in the async tier may import a plain function: #862's receipt guest calls
	// `note` from `Drop` glue, which cannot await, so an async impl is the wrong shape for it.
	syncImpls map[string]interp.CanonFunc
}

// asyncImplFor is an async host import as the walk binds it: a factory called with the importing
// instance's async table, so the work an impl leaves pending is that instance's, and that instance's Close
// stops it (ADR 0121). A stream consumer's bind has the same shape for the same reason.
//
// **It is handed the instance's resource table too** (ADR 0123), so an async method of a host resource,
// `tcp-socket.connect`, can lend its `self` for the subtask's life. An impl over no resource ignores it.
type asyncImplFor func(async *asyncHandles, res *hostResources) asyncLowerImpl

// asyncWasi is the async-lower impl source — SEPARATE from wasi() by type, so binding an async lower to a
// sync impl (or vice versa) is a compile error, not a runtime surprise. It is the production async imports
// (asyncSource, ADR 0121) with the test-only asyncImpls merged in, each wrapped as a factory that ignores
// the table. A name the production table serves is refused, never shadowed (mergeExtras).
func (h *Host) asyncWasi() map[string]asyncImplFor {
	m := h.asyncSource()
	extras := make(map[string]asyncImplFor, len(h.asyncImpls))
	for name, impl := range h.asyncImpls {
		extras[name] = func(*asyncHandles, *hostResources) asyncLowerImpl { return impl }
	}
	mergeExtras(m, extras, "async import")
	return m
}

// streamConsumer is a host import that CONSUMES a guest-provided stream — it needs the per-instance async
// handle table (to reach the readable end the guest handed it) as well as the host's own resources (stdout),
// so it is a factory the walk calls with the instance's table, kept SEPARATE from wasi()/asyncWasi() by type
// for the same reason those are separate (a wrong-source binding is a compile error). It stays INTERNAL: it
// takes an *asyncHandles, an unexported type, so it cannot appear on the exported API (gate:async #739).
type streamConsumer struct {
	bind func(async *asyncHandles) interp.CanonFunc
	// bindRes, when set, is bound instead of bind, with the instance's resource table as well (ADR 0123): a
	// stream method of a host resource, `tcp-socket.send` or `.receive`, lends its `self` from it.
	bindRes func(async *asyncHandles, res *hostResources) interp.CanonFunc
	// payload is the type of the future the consumer returns, as the host's interface declares it and the
	// consumer lowers its completion at (ADR 0116). The guest's own signature must declare the same type,
	// checked at instantiate (ADR 0118), since the guest reads the bytes the host writes at that type.
	payload func() (canon.Type, error)
	// produces marks the stream PRODUCER (read-via-stream, ADR 0122), whose result is
	// `tuple<stream<u8>, future<payload>>` rather than a consumer's `future<payload>`. It shares this table
	// because it needs what a consumer needs, the instance's async table and a payload checked at instantiate.
	produces bool
}

// streamConsumers maps an import identity, keyed by compatibility class like wasi()'s, to a host import over a
// stream. stdout's and stderr's write-via-stream are served, only for WASI 0.3, where they replace 0.2's
// `get-stdout` and `get-stderr`; an unlisted consumer reaches the refusing stub. The two are one
// implementation over two writers, read here, when an instantiate builds the host's tables (ADR 0119).
//
// stdin's read-via-stream is the one producer (ADR 0122): the host writes the stream from h.Stdin, read on
// demand, and resolves its future when the source ends or the guest drops the stream.
func (h *Host) streamConsumers() map[string]*streamConsumer {
	m := map[string]*streamConsumer{}
	serve(m, wasiP3, "wasi:cli/stdout::write-via-stream", &streamConsumer{bind: writeViaStream(h.Stdout), payload: stdioWriteResultType})
	// NEUTER THIS (h.Stdout for h.Stderr), and TestP3SysMatchesWasmtime's stderr line lands on stdout.
	serve(m, wasiP3, "wasi:cli/stderr::write-via-stream", &streamConsumer{bind: writeViaStream(h.Stderr), payload: stdioWriteResultType})
	serve(m, wasiP3, "wasi:cli/stdin::read-via-stream", &streamConsumer{bind: h.readViaStream, payload: stdioWriteResultType, produces: true})
	// tcp-socket's two stream methods (ADR 0123, hostsock.go): receive is a producer in stdin's shape and send a
	// consumer in stdout's, each lending its self from the instance's resource table.
	serve(m, wasiP3, "wasi:sockets/types::[method]tcp-socket.receive", &streamConsumer{bindRes: h.tcpReceive, payload: sockFuturePayloadType, produces: true})
	serve(m, wasiP3, "wasi:sockets/types::[method]tcp-socket.send", &streamConsumer{bindRes: h.tcpSend, payload: sockFuturePayloadType})
	return m
}

// writeViaStream implements `wasi:cli/stdout::write-via-stream` and `wasi:cli/stderr::write-via-stream`:
// `func(data: stream<u8>) -> future<result<_, error-code>>`, the real host consumer of the readable stream
// end the guest hands it (the exit-condition path, gate:async increment 4). It reads `data` into w, the
// host's stdout or stderr (a host sink, NOT a guest ptr — the #763 divergence), and returns a host-minted
// future that resolves when the consume completes. The future reuses the readable-future-end kind already on
// main (async_lower.go mints one for a KindFuture result), and completes with `result::ok`, lowered through
// the codec at the future's payload type (#1246).
//
// It is the consumer's bind: given w, it returns the factory the walk calls with each instance's async table.
func writeViaStream(w io.Writer) func(async *asyncHandles) interp.CanonFunc {
	return func(async *asyncHandles) interp.CanonFunc { return writeViaStreamTo(w, async) }
}

// writeViaStreamTo is writeViaStream's consumer over one writer and one instance's async table.
func writeViaStreamTo(w io.Writer, async *asyncHandles) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: write-via-stream: got %d args, want (data)", len(args))
		}
		okv, err := stdioWriteResultOK()
		if err != nil {
			return nil, err
		}
		ri := uint32(args[0].Bits)
		async.mu.Lock()
		defer async.mu.Unlock()
		re, ok := handleAt[*readableStreamEnd](async, ri)
		if !ok {
			return nil, fmt.Errorf("%w: write-via-stream on handle %d that is not a readable stream end", ErrLinkRefused, ri)
		}
		// A stream the host itself produces (read-via-stream's) has no guest writer to drive the copy, and a
		// host-to-host copy is not built, so it is refused by name (ADR 0122).
		if re.conn != nil && re.conn.producer != nil {
			return nil, fmt.Errorf("%w: write-via-stream of a stream the host produces (a guest forwarding "+
				"read-via-stream's stream without reading it); a host-to-host copy is not built (ADR 0122)", ErrUnsupportedForm)
		}
		// Mint the future the guest awaits; it resolves with `result::ok` when the consume completes.
		//
		// **The payload is typed, not a u32** (#1246). This was `readableFutureEnd{value: 0}`, and the
		// completion wrote `value` as 4 bytes at the reader's pointer where `result<_, error-code>` is 2,
		// zeroing two guest bytes past the result on every completion. It is a default-on path: gate:async
		// is on, and every WASI 0.3 guest that writes stdout reaches it.
		fe := &readableFutureEnd{payload: &okv}
		fe.index = async.addLocked(fe)
		// **The writer has already dropped** (#1264, ADR 0122): its drop unlinked this end and left DROPPED pending,
		// so the host's read takes it and the future resolves `ok` inline, as the reference's host would find
		// `other` gone at its read. The drop left `conn` set, so this parked a read on a writer that was gone, and
		// the future never resolved.
		//
		// NEUTER: drop this arm, and TestAWritersDropUnlinksItsReader's write-via-stream after the drop never
		// resolves its future.
		if re.writerDropped {
			re.state = copyStateCopying
			re.pendingEventLocked() // DROPPED -> DONE; nothing pending if a read already took it
			re.state = copyStateDone
			if cerr := fe.completeLocked(copyCompleted); cerr != nil {
				return nil, cerr
			}
			return []interp.Value{interp.I32(int32(fe.index))}, nil
		}
		// Register the host read: sink to the writer. readN takes all the guest offers in a write.
		re.hostSink = func(b []byte) error { _, werr := w.Write(b); return werr }
		// **The host reads until the writer drops** (#1261, ADR 0120). A guest keeps one stream open across
		// its writes, as Rust std's stdout does, so a COMPLETED copy re-arms the read: the end stays COPYING
		// with its read pending, and the next write is again the second arriver. That is the reference's
		// owner-less End copying again after each event, until its event is DROPPED
		// (def:StreamEnd.notify.stream_event:"result = CopyResult.DROPPED"), and it is when the future resolves.
		// This resolved the future on the first copy, so a std guest's second println parked on a stream
		// nobody read and trapped "cannot block a synchronous task before returning".
		//
		// NEUTER: resolve the future on every result, and TestP3StdMatchesWasmtime's second line traps, and
		// TestWriteViaStreamResolvesAtTheWritersDrop's future resolves with the writer still open.
		re.onHostComplete = func(r copyResult) {
			if r == copyCompleted {
				re.resolved, re.delivered, re.progress = false, false, 0
				return
			}
			if cerr := fe.completeLocked(copyCompleted); cerr != nil {
				fe.completeErr = cerr // deferred: onHostComplete cannot return; futureDrop surfaces it
			}
		}
		re.readN = 1 << 30
		re.hasRead = true
		re.caller = c
		re.state = copyStateCopying
		// If the guest already wrote (guest-first), drive the copy now; otherwise its stream.write drives it. A
		// parked write of 0 copies nothing and completes, and the read re-arms, so this read is left parked as
		// the reference's zero-length arm leaves it (ADR 0120).
		if re.conn != nil && re.conn.hasWrite && !re.conn.resolved {
			progress, derr := driveStreamCopyLocked(re, re.conn)
			if derr != nil {
				return nil, derr
			}
			re.conn.completeWriteLocked(progress, copyCompleted)
			re.armLocked(progress, copyCompleted)
		}
		return []interp.Value{interp.I32(int32(fe.index))}, nil
	}
}

// stdioErrorCodeLabels are `wasi:cli/types@0.3`'s `error-code`, read from p3async-hello.wasm with
// `wasm-tools component wit`.
var stdioErrorCodeLabels = []string{"io", "illegal-byte-sequence", "pipe"}

// stdioWriteResultType is `write-via-stream`'s future payload type, `result<_, error-code>` (ADR 0116): a
// 1-byte discriminant and a 1-byte enum payload, size 2 and alignment 1.
//
// # The host's declaration, checked against the guest's signature
//
// The host implements `wasi:cli/stdout@0.3`'s `write-via-stream`, and that WIT fixes the type, so the host's
// declaration is the type the interface promises and the one the completion is lowered at.
//
// **When ADR 0116 chose it, the guest's signature could not be asked.** Its `error-code` is `use`d from
// `wasi:cli/types`, an outer alias of an `alias export` of an imported instance's type export, and the
// decoder recorded every type export as a resource placeholder, so the payload decoded as `future<ref>`.
// ADR 0118 resolves it, and the two are now a checked agreement rather than a substitution: at instantiate
// the guest's declared payload must equal this one (walker.checkConsumerPayload), or the lower is refused.
//
// Built through the codec's constructors on each call; they cannot fail on these constant labels, and are
// checked anyway rather than assumed.
func stdioWriteResultType() (canon.Type, error) {
	ec, err := canon.EnumType(stdioErrorCodeLabels...)
	if err != nil {
		return canon.Type{}, fmt.Errorf("component: write-via-stream: error-code: %w", err)
	}
	return canon.ResultType(nil, &ec), nil
}

// stdioWriteResultOK is `result::ok` at stdioWriteResultType, the value a completed write resolves with.
func stdioWriteResultOK() (canon.Value, error) {
	t, err := stdioWriteResultType()
	if err != nil {
		return canon.Value{}, err
	}
	return canon.Variant(t, "ok", nil)
}

// NewHost builds a preview-2 host over the given streams. Handles are minted per instance, from 1, by the
// instance's table (ADR 0117). The monotonic clock starts here and the insecure seed is drawn here (ADR
// 0119), so every instance one Host serves reads one clock and one seed.
func NewHost(stdout, stderr io.Writer, stdin io.Reader) *Host {
	// NEUTER THIS (drop `start`), and TestP3SysMatchesWasmtime's monotonic line reads false: the clock
	// saturates at 2^63-1 and never advances.
	return &Host{Stdout: stdout, Stderr: stderr, Stdin: stdin, start: time.Now(), seed: newInsecureSeed()}
}

// outputStream is the object behind a `wasi:io/streams` `output-stream` handle. **Not an io.Closer, on
// purpose**: the stdio streams are the process's, so dropping the guest's handle must not close them, and
// a nil destructor closes only an object that is a Closer.
type outputStream struct{ w io.Writer }

// inputStream is the object behind an `input-stream` handle; not a Closer, for outputStream's reason.
type inputStream struct{ r io.Reader }

// resourceSource is the host's resource half (ADR 0117): the resource types it implements and the imports
// over them, each keyed by name and compatibility class through serve, with the test-only extras merged.
//
// Four types are WASI 0.2's, two are 0.3's terminals, and one is 0.3's TCP socket (ADR 0123). `descriptor` is declared although nothing mints one: `get-directories`
// grants an empty list, and a guest's `[resource-drop]descriptor` must still name a host type rather than
// fall back to dropNoop.
func (h *Host) resourceSource() hostResourceSource {
	types := map[string]*hostResource{}
	serve(types, wasiP2, "wasi:io/streams::output-stream", outputStreamResource)
	serve(types, wasiP2, "wasi:io/streams::input-stream", inputStreamResource)
	serve(types, wasiP2, "wasi:io/error::error", errorResource)
	// For 0.3 too since ADR 0123, because get-directories is: its own<descriptor> must name a host type.
	serve(types, wasiP2P3, "wasi:filesystem/types::descriptor", descriptorResource)
	// WASI 0.3's terminal types (ADR 0119), declared because the `get-terminal-*` getters in wasi() are
	// served: each returns `option<own<…>>`, and checkHostResources refuses a served import whose own names no
	// host resource (ADR 0118). So without these, every std guest that lowers the getters, p3async-hello
	// first, is refused at instantiate. It also gives a guest's drop of one a host type rather than dropNoop.
	//
	// NEUTER THIS (delete both lines), and TestGateAsyncGuestRunsToTheCommittedReading and
	// TestP3SysMatchesWasmtime are refused at instantiate, naming get-terminal-stdin's terminal-input.
	serve(types, wasiP3, "wasi:cli/terminal-input::terminal-input", terminalInputResource)
	serve(types, wasiP3, "wasi:cli/terminal-output::terminal-output", terminalOutputResource)
	// wasi:sockets' TCP socket (ADR 0123, hostsock.go). UDP's is not declared, so a guest's drop of one keeps
	// dropNoop, as it did: nothing mints one.
	serve(types, wasiP3, "wasi:sockets/types::tcp-socket", tcpSocketResource)

	m := map[string]resourceMethod{}
	// The stdio getters mint an own of their stream (def:lower_own), built at the call so a Stdout set
	// after NewHost is the one written. stdin's stream is an input-stream; it was minted as an
	// output-stream over io.Discard while the handle maps were untyped, and nothing could tell.
	serve(m, wasiP2, "wasi:cli/stdout::get-stdout", mints(outputStreamResource, func() any { return &outputStream{w: h.Stdout} }))
	serve(m, wasiP2, "wasi:cli/stderr::get-stderr", mints(outputStreamResource, func() any { return &outputStream{w: h.Stderr} }))
	serve(m, wasiP2, "wasi:cli/stdin::get-stdin", mints(inputStreamResource, func() any { return &inputStream{r: h.Stdin} }))

	serve(m, wasiP2, "wasi:io/streams::[method]output-stream.blocking-write-and-flush", method(outputStreamResource, h.streamWrite))
	serve(m, wasiP2, "wasi:io/streams::[method]output-stream.write", method(outputStreamResource, h.streamWrite))
	serve(m, wasiP2, "wasi:io/streams::[method]output-stream.check-write", method(outputStreamResource, h.checkWrite))
	serve(m, wasiP2, "wasi:io/streams::[method]output-stream.blocking-flush", method(outputStreamResource, h.blockingFlush))
	serve(m, wasiP2, "wasi:io/streams::[method]input-stream.blocking-read", method(inputStreamResource, h.blockingRead))

	serve(m, wasiP2, "wasi:io/error::[method]error.to-debug-string", method(errorResource, h.errorToDebugString))

	// tcp-socket's sync methods (ADR 0123): its constructor and the getters Rust std reaches. connect is async
	// (asyncSource), and send and receive take streams (streamConsumers). The rest reach the stub at their call.
	serve(m, wasiP3, "wasi:sockets/types::[static]tcp-socket.create", h.tcpCreate)
	serve(m, wasiP3, "wasi:sockets/types::[method]tcp-socket.get-local-address", method(tcpSocketResource, h.tcpGetLocalAddress))
	serve(m, wasiP3, "wasi:sockets/types::[method]tcp-socket.get-remote-address", method(tcpSocketResource, h.tcpGetRemoteAddress))
	serve(m, wasiP3, "wasi:sockets/types::[method]tcp-socket.get-address-family", method(tcpSocketResource, h.tcpGetAddressFamily))
	serve(m, wasiP3, "wasi:sockets/types::[method]tcp-socket.get-is-listening", method(tcpSocketResource, h.tcpGetIsListening))

	mergeExtras(types, h.resourceTypeImpls, "resource type")
	mergeExtras(m, h.resourceImpls, "resource import")
	return hostResourceSource{types: types, methods: m}
}

// mergeExtras merges test-only entries into a production table, refusing one that would shadow a name the
// table serves under any class — wasi()'s rule for syncImpls, for the resource half.
func mergeExtras[V any](m, extras map[string]V, what string) {
	served := map[string]bool{}
	for k := range m {
		name, _, _ := cutVersion(k)
		served[name] = true
	}
	for name, v := range extras {
		if base, _, _ := cutVersion(name); served[base] {
			panic("component: an extra " + what + " would shadow the host's " + name +
				" — pick another name; the host does not let an extra entry displace a real one")
		}
		m[name] = v
	}
}

// wasi builds the import-name → canon-lower-impl table the walk consults. The keys are the
// "module::export" identities the loader records; a lowered func whose identity is absent still reaches
// the refusing stub (an unexercised import remains refused, by name).
//
// **Each key carries the compatibility class it serves** (#1229, ADR 0115): `wasi:cli/stdout@0.2::get-stdout`,
// registered through `serve`. A guest at `@0.2.6` (p3echo) and one at `@0.2.3` (p3hello) are one class and
// meet one entry; a guest at `@0.3.0` meets it only if the entry was registered for 0.3. This replaced
// version-stripped keys, under which WASI 0.2 and 0.3 — which share names with different signatures — met
// the same entry, and the 0.3 fixtures ran partly on implementations nobody had said were 0.3's.
func (h *Host) wasi() map[string]interp.CanonFunc {
	m := map[string]interp.CanonFunc{}
	// The stdio getters and every `wasi:io` import take or return resource handles, so they are in
	// resourceSource(), bound with the calling instance's table (ADR 0117). They are 0.2 only: 0.3 hands
	// streams across as values (`write-via-stream`, in streamConsumers).

	// One implementation for both tracks: these signatures are the same in 0.2 and 0.3, which is what the
	// 0.3 fixtures (p3async-hello, p3async-cancel) were relying on when the version was stripped.
	serve(m, wasiP2P3, "wasi:cli/exit::exit", h.exit)

	// get-arguments lowers the host's argv as a non-empty list<string>; get-environment/get-directories
	// grant nothing, so each lowers an empty list of its element type — through the codec, so the
	// backing realloc's alignment is the element's, not a hand-hardcoded 4.
	serve(m, wasiP2P3, "wasi:cli/environment::get-arguments", h.getArguments)
	serve(m, wasiP2P3, "wasi:cli/environment::get-environment", h.emptyListOf(
		canon.TupleType(canon.Type{Kind: canon.KindString}, canon.Type{Kind: canon.KindString})))
	// The element names the host's descriptor type; it named RT 0, which was `errorRT` too (ADR 0117).
	//
	// **Served for 0.3 too** (ADR 0123). It was 0.2 only, *"no 0.3 fixture imports it"*, until p3tcp: wasi-libc
	// asks for the preopens when a program that has used a socket closes one, as wasmtime 49.0.3's trace of the
	// same guest shows, and the 0.3 WIT's `list<tuple<descriptor, string>>` is the 0.2 shape. Nothing is granted,
	// so the list is empty on both tracks, as wasmtime's is with no `--dir`.
	//
	// NEUTER: serve it for 0.2 only again, and TestP3TcpMatchesWasmtime's closed-port line is never printed:
	// the call reaches the cross-track refusal.
	serve(m, wasiP2P3, "wasi:filesystem/preopens::get-directories", h.emptyListOf(
		canon.TupleType(canon.OwnType(descriptorResource.rt), canon.Type{Kind: canon.KindString})))

	// WASI 0.3's synchronous system rows (#1232, ADR 0119; hostsys.go), 0.3 only: 0.2's random and clocks
	// are other interfaces with other signatures, and are not v0.14.0's.
	serve(m, wasiP3, "wasi:random/random::get-random-bytes", h.randomBytes)
	serve(m, wasiP3, "wasi:random/random::get-random-u64", h.randomU64)
	serve(m, wasiP3, "wasi:random/insecure::get-insecure-random-bytes", h.randomBytes)
	serve(m, wasiP3, "wasi:random/insecure::get-insecure-random-u64", h.randomU64)
	serve(m, wasiP3, "wasi:random/insecure-seed::get-insecure-seed", h.insecureSeed)
	serve(m, wasiP3, "wasi:clocks/monotonic-clock::now", h.monotonicNow)
	serve(m, wasiP3, "wasi:clocks/monotonic-clock::get-resolution", h.clockGetResolution)
	serve(m, wasiP3, "wasi:clocks/system-clock::now", h.systemNow)
	serve(m, wasiP3, "wasi:clocks/system-clock::get-resolution", h.clockGetResolution)
	serve(m, wasiP3, "wasi:cli/exit::exit-with-code", h.exitWithCode)
	// cwd and the terminals are none: #1232 grants no cwd, and the host's streams are io.Writers and an
	// io.Reader, which cannot say whether a terminal is behind them. The terminal getters return an own, so
	// their resource types are declared in resourceSource(); none is ever minted.
	serve(m, wasiP3, "wasi:cli/environment::get-initial-cwd", noneOf("get-initial-cwd", canon.Type{Kind: canon.KindString}))
	serve(m, wasiP3, "wasi:cli/terminal-stdin::get-terminal-stdin", noneOf("get-terminal-stdin", canon.OwnType(terminalInputResource.rt)))
	serve(m, wasiP3, "wasi:cli/terminal-stdout::get-terminal-stdout", noneOf("get-terminal-stdout", canon.OwnType(terminalOutputResource.rt)))
	serve(m, wasiP3, "wasi:cli/terminal-stderr::get-terminal-stderr", noneOf("get-terminal-stderr", canon.OwnType(terminalOutputResource.rt)))

	// **No `[resource-drop]` keys** (ADR 0117). Four stood here, bound to a no-op, and no guest could reach
	// them: a key here is a `canon lower`'s identity, and a resource drop is a `canon resource.drop`, which
	// the walk binds itself. A drop of a host type is now real there (walk.go, resolverFor).
	//
	// Extra SYNC impls, the counterpart of `asyncImpls` (#863). A guest in the async tier can import a
	// plain function the host must answer — #862's receipt guest calls `note` from `Drop` glue, which
	// cannot await, so it has to be sync. `asyncImpls` already had a field for its half; this is the other.
	// Their keys are unversioned, so each is a wildcard and serves every class (lookupHost).
	//
	// **A name already served by WASI is REFUSED, not shadowed.** Silently overriding
	// `wasi:cli/exit::exit` with a test's function would be a capability change disguised as a map write,
	// and the guest would have no way to tell. The panic is the right severity for an in-package
	// misconfiguration: `syncImpls` is unexported and set only by a test, so a collision is a programming
	// error at the call site rather than anything a guest or an embedder can provoke. The collision is by
	// version-free name, under any class: a wildcard `wasi:cli/exit::exit` would answer 0.2's and 0.3's guests.
	served := map[string]bool{}
	for k := range m {
		name, _, _ := cutVersion(k)
		served[name] = true
	}
	// The resource imports count as served: `get-stdout` moved to resourceSource(), which resolveHost asks
	// before this table, so an extra impl of that name would be silently unreachable rather than refused.
	for k := range h.resourceSource().methods {
		name, _, _ := cutVersion(k)
		served[name] = true
	}
	for name, fn := range h.syncImpls {
		if base, _, _ := cutVersion(name); served[base] {
			panic("component: syncImpls would shadow the WASI impl for " + name +
				" — pick another name; the host does not let an extra impl displace a real capability")
		}
		m[name] = fn
	}
	return m
}

// streamErrorType is the WIT `stream-error` variant: `last-operation-failed(own<error>)` or `closed`. The
// own names the host's error type, whose number no other type shares (ADR 0117).
func streamErrorType() canon.Type {
	own := canon.OwnType(errorResource.rt)
	return canon.VariantType(
		canon.Case{Name: "last-operation-failed", Type: &own},
		canon.Case{Name: "closed"},
	)
}

// streamResultType builds `result<ok, stream-error>` — `ok == nil` is `result<_, stream-error>`.
func streamResultType(ok *canon.Type) canon.Type {
	se := streamErrorType()
	return canon.ResultType(ok, &se)
}

// lowerResult lowers a fully-built result value at the guest return pointer through the codec's shared
// `StoreVia` — the same result/variant/own lowering the definitions.py fixtures verify (#724), not a
// hand path. A construction error means the value did not match its declared type — an engine bug.
func lowerResult(c *interp.CanonCaller, ret uint64, v canon.Value, buildErr error) error {
	if buildErr != nil {
		return fmt.Errorf("component: build result value: %w", buildErr)
	}
	return canon.StoreVia(guestHeap{c}, v, int(uint32(ret)))
}

// streamWrite marshals output-stream.write / blocking-write-and-flush: (self, contents: list<u8>) ->
// result<_, stream-error>, flattened to (self, ptr, len, ret). It lifts the list<u8> from guest memory,
// writes it to the stream `self` names, and lowers Ok — or, when the write fails,
// Err(last-operation-failed(own<error>)) minted from the underlying error (#694's deferred err arm).
// Both arms lower through the codec (#724), not a hand path.
//
// `self` arrives lent (`method`, ADR 0117): a handle this instance does not hold, or one naming another
// type, traps before this runs. It was a lookup in a map every instance of the Host shared, and an unknown
// handle was refused as a link error.
func (h *Host) streamWrite(res *hostResources, c *interp.CanonCaller, self *outputStream, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("component: stream write: got %d core args, want 4 (self, ptr, len, ret)", len(args)+1)
	}
	ptr := uint64(uint32(args[0].Bits))
	n := uint64(uint32(args[1].Bits))
	ret := uint64(uint32(args[2].Bits))
	w := self.w
	// **Through the codec, which this function's doc comment already claimed** (#903). It read raw bytes
	// with `c.Read` and said it lifted "the list<u8> from the bound memory (canon load over u8)" — a
	// comment naming one mechanism while the code embodied another, and the reason it could say so with a
	// straight face is that for `list<u8>` the two agree byte for byte: a u8 element is one byte at a
	// one-byte stride, so the codec's load loop and a raw read produce the same slice.
	//
	// Routed properly now that there is a lifting abstraction to route through. It is behaviour-preserving
	// for this element type by the argument above, and it means the next element type to arrive here gets
	// the framing from the verified codec rather than from a second hand path.
	buf, err := canon.LoadListU8(guestHeap{c}, int(ptr), int(n))
	if err != nil {
		return nil, fmt.Errorf("component: stream write: reading contents: %w", err)
	}
	if _, werr := w.Write(buf); werr != nil {
		// The error is minted in the calling instance's table, an own the guest drops or debug-strings.
		own := canon.Own(errorResource.rt, uint32(res.mint(errorResource, werr)))
		se, serr := canon.Variant(streamErrorType(), "last-operation-failed", &own)
		if serr != nil {
			return nil, serr
		}
		v, verr := canon.Variant(streamResultType(nil), "error", &se)
		return nil, lowerResult(c, ret, v, verr)
	}
	v, verr := canon.Variant(streamResultType(nil), "ok", nil)
	return nil, lowerResult(c, ret, v, verr)
}

// checkWrite marshals output-stream.check-write: (self) -> result<u64, stream-error>, flattened to
// (self, ret). It reports a large writable budget (the ok arm), so the guest never blocks on capacity.
// `self` is lent and checked by `method` though no field of it is read: a handle the instance does not hold
// traps here as it would at a write.
func (h *Host) checkWrite(_ *hostResources, c *interp.CanonCaller, _ *outputStream, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("component: check-write: got %d core args, want 2 (self, ret)", len(args)+1)
	}
	ret := uint64(uint32(args[0].Bits))
	u := canon.U64(1 << 30) // a large writable budget, so the guest never blocks on capacity
	okT := canon.Type{Kind: canon.KindU64}
	v, verr := canon.Variant(streamResultType(&okT), "ok", &u)
	return nil, lowerResult(c, ret, v, verr)
}

// blockingFlush marshals output-stream.blocking-flush: (self) -> result<_, stream-error>, always Ok.
func (h *Host) blockingFlush(_ *hostResources, c *interp.CanonCaller, _ *outputStream, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("component: blocking-flush: got %d core args, want 2 (self, ret)", len(args)+1)
	}
	ret := uint64(uint32(args[0].Bits))
	v, verr := canon.Variant(streamResultType(nil), "ok", nil)
	return nil, lowerResult(c, ret, v, verr)
}

// exitError is a guest's `wasi:cli/exit` call, carried out of the guest as the host function's error (#1262,
// ADR 0120). A host function's error unwinds the interpreter, which is what an exit must do: the WIT's
// `exit` does not return, and Rust std follows the call with `unreachable`. [Instantiated.CallRun] unwraps
// it into the run's code, preview 1's `exitError` shape (internal/wasi), so an exit is neither a trap nor a
// silent success. Unexported: an embedder's [Component.Call] sees it only as an error that names the exit.
type exitError struct{ code int32 }

func (e exitError) Error() string {
	return fmt.Sprintf("component: the guest exited with code %d (wasi:cli/exit)", e.code)
}

// exit marshals cli/exit.exit: (status: result<_, _>) -> (), flattened to a single discriminant i32
// (0 = ok/success, 1 = error). It records the exit and ends the run with it (ADR 0120).
//
// NEUTER: return nil, as before ADR 0120, and TestSharedTrackEntriesStillServe gets nil for the exitError.
// No guest witness fails under it: Rust std's process::exit calls exit-with-code (exitWithCode), not this.
func (h *Host) exit(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	h.Exited = true
	if len(args) == 1 && args[0].Int32() != 0 {
		h.ExitCode = 1
	}
	return nil, exitError{code: h.ExitCode}
}

// getArguments lowers the host's argv as a non-empty `list<string>` at the return pointer (its sole
// arg) — the first host-produced list with real payload. Each string's bytes are allocated in guest
// memory through the guest's realloc; the list backing is `count` string records (ptr, len) of 8 bytes.
func (h *Host) getArguments(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("component: get-arguments: got %d core args, want 1 (ret)", len(args))
	}
	// list<string> lowered by the codec's shared framing (canon.StoreList/StoreString via StoreVia),
	// against guest memory — the same code the definitions.py list<string> fixtures verify (#718), not a
	// hand path.
	elems := make([]canon.Value, len(h.Args))
	for i, s := range h.Args {
		elems[i] = canon.Str(s)
	}
	v, err := canon.List(canon.Type{Kind: canon.KindString}, elems...)
	if err != nil {
		return nil, err
	}
	return nil, canon.StoreVia(guestHeap{c}, v, int(uint32(args[0].Bits)))
}

// blockingRead marshals input-stream.blocking-read: (self: borrow<input-stream>, len: u64) ->
// result<list<u8>, stream-error>, flattened to (self, len, ret). It reads up to `len` bytes from the
// stream `self` names, the host stdin, **through the blocking excursion** (§5 H-1/H-3,
// `CanonCaller.Blocking`), then — after the wake, never while blocked — lowers the result: Ok(list<u8>)
// when bytes were read, Err(closed) at EOF (the stream-error variant's `closed` case, no payload). The read
// is interruptible: Close cancels it. `self` stays lent across the read, so a drop of it from another agent
// meanwhile traps rather than freeing the stream under the read (ADR 0117).
func (h *Host) blockingRead(_ *hostResources, c *interp.CanonCaller, self *inputStream, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("component: blocking-read: got %d core args, want 3 (self, len, ret)", len(args)+1)
	}
	maxLen := args[0].Bits // u64
	ret := uint64(uint32(args[1].Bits))

	const readCap = 1 << 16
	n := maxLen
	if n > readCap {
		n = readCap
	}
	var data []byte
	if self.r != nil && n > 0 {
		buf := make([]byte, n)
		var m int
		var rerr error
		// A direct blocking read inside the blocked excursion — the shape p1's `fd_read` uses (no engine
		// goroutine, so no new concurrency to authorise). enterBlocked marks the agent at a safepoint for
		// the read's duration (H-1: a Stop completes while it is parked), and the wake's poll ends it on
		// Close (H-3) once the read returns. A reader that never returns is p1's own limitation, not this
		// slice's to lift; a mid-read-cancellable read would be a goroutine site behind its own decision.
		if err := c.Blocking(func() error {
			m, rerr = self.r.Read(buf)
			if rerr != nil && rerr != io.EOF {
				return rerr
			}
			return nil
		}); err != nil {
			return nil, err
		}
		data = buf[:m]
	}

	// result<list<u8>, stream-error>, lowered through the codec (#724): Ok(list<u8>) when bytes were
	// read, else Err(closed) at EOF (the stream-error variant's payloadless `closed` case).
	listU8 := canon.Type{Kind: canon.KindList, Elem: &canon.Type{Kind: canon.KindU8}}
	if len(data) > 0 {
		us := make([]canon.Value, len(data))
		for i, b := range data {
			us[i] = canon.U8(b)
		}
		lst, lerr := canon.List(canon.Type{Kind: canon.KindU8}, us...)
		if lerr != nil {
			return nil, lerr
		}
		v, verr := canon.Variant(streamResultType(&listU8), "ok", &lst)
		return nil, lowerResult(c, ret, v, verr)
	}
	closed, cerr := canon.Variant(streamErrorType(), "closed", nil)
	if cerr != nil {
		return nil, cerr
	}
	v, verr := canon.Variant(streamResultType(&listU8), "error", &closed)
	return nil, lowerResult(c, ret, v, verr)
}

// errorToDebugString marshals error.to-debug-string: (self: borrow<error>) -> string, flattened to
// (self, ret). `self` is the lent error, and its message is lowered as a string. An unknown handle used to
// lower a placeholder, "stream error"; it now traps, as def:lift_borrow refuses an index the instance does
// not hold (ADR 0117).
func (h *Host) errorToDebugString(_ *hostResources, c *interp.CanonCaller, self error, args []interp.Value) ([]interp.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("component: error.to-debug-string: got %d core args, want 2 (self, ret)", len(args)+1)
	}
	// The string is lowered by the codec's one string lowering (canon.StoreString), against guest memory
	// — the same code the definitions.py string fixtures verify (#719), not a hand path.
	return nil, canon.StoreString(guestHeap{c}, self.Error(), int(uint32(args[0].Bits)))
}

// emptyListOf marshals a `() -> list<elem>` import this host grants nothing (get-environment,
// get-directories): its sole core argument is the return pointer, and it lowers an *empty* list of the
// given element type through the codec (`canon.StoreVia`/`StoreList`). The model allocates even for a
// zero-length list — `store_list` calls `realloc(0, 0, elem_align, 0)` and stores the returned pointer
// (definitions.py) — so the header is (realloc(...), 0), not (0, 0), and the realloc's alignment is the
// element's, taken from `elem` by the codec rather than a hand-hardcoded 4. No element is lowered (the
// list is empty), so a `tuple`/`own` element only needs its size and alignment, not its lowering (#725).
// The lowering is byte-verified by the canon differential's empty-`list<tuple>` fixture.
func (h *Host) emptyListOf(elem canon.Type) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("component: list getter: got %d core args, want 1 (ret)", len(args))
		}
		v, err := canon.List(elem)
		if err != nil {
			return nil, fmt.Errorf("component: empty list value: %w", err)
		}
		return nil, canon.StoreVia(guestHeap{c}, v, int(uint32(args[0].Bits)))
	}
}
