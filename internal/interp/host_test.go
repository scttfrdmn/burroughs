// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// hostImports is the one-name registry these tests link against: every module name resolves, and the
// function name selects. `exportsOf`'s shape for a supplier that is a Go map rather than an instance.
func hostImports(fns map[string]Extern) Imports {
	return func(_, name string) (Extern, bool) {
		ext, ok := fns[name]
		return ext, ok
	}
}

// hostLink builds a module at `feats` and links it against a resolver, failing on anything short of a
// complete instantiation.
//
// Features are a parameter rather than a constant because two rows need gates `link1` does not open —
// `return_call` for the tail-call arm and `threads` for the shared memory `Spawn` requires — and a
// helper per gate would put the same three steps in the file three times.
func hostLink(t *testing.T, src string, feats binary.Features, imp Imports) *Instance {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := (&binary.Decoder{Features: feats}).DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in, trap, lerr := InstantiateLinked(m, imp)
	if lerr != nil {
		t.Fatalf("link: %v", lerr)
	}
	if trap != nil {
		t.Fatalf("instantiate trapped: %v", trap)
	}
	if derr := in.Deferred(); derr != nil {
		t.Fatalf("instantiation fell short: %v", derr)
	}
	return in
}

// ft is `binary.FuncType` with the two slices positional, so a signature reads as one line at the
// call site and a row's type sits next to the Go function it belongs to.
func ft(params, results []binary.ValType) binary.FuncType {
	return binary.FuncType{Params: params, Results: results}
}

// TestAHostFunctionSeesItsArgumentsAndDeliversItsResultsInDeclarationOrder is [#602][602]'s
// accept-direction control, and every row is built so that the *wrong* order produces a plausible
// number rather than an error.
//
// # Ordering is the defect these rows exist for
//
// `hostArgs` pops in reverse and fills in place; `pushHostResults` pushes in declaration order. Both
// are invisible for a one-argument, one-result function whose arguments are equal — which is what a
// first host-function test looks like if nobody chose the values. So:
//
//   - **mix** computes `10*a + b` from `(3, 4)`. Right order answers 34, reversed answers 43. A
//     commutative host function here would score both readings green, which is §9 G-3's blind spot in
//     miniature.
//   - **twoResults** has the *host* return `(7, 3)` into a guest `i32.sub`. Declaration order pushes
//     7 then 3 and the guest answers 4; a reversed push answers -4. Sign, not magnitude, so the two
//     readings cannot be confused by a reader either.
//   - **mixedTypes** takes `(i32, i64, f64)` and sums them as f64 from `(1, 20, 300)`. It is here
//     because `hostArgs` dispatches on the declared type per slot: a defect that read all three off
//     the numeric stack in one direction, or that took the f64 through `Bits` alone, lands on a
//     different total. It also asserts the `Value.Type` the host is *handed*, since a host function
//     that must re-derive its own parameter types from a lookup table would be an ABI nobody wants.
//
// [602]: https://github.com/scttfrdmn/burroughs/issues/602
func TestAHostFunctionSeesItsArgumentsAndDeliversItsResultsInDeclarationOrder(t *testing.T) {
	var gotTypes []binary.ValType

	in := hostLink(t, `(module
		(import "h" "mix" (func $mix (param i32 i32) (result i32)))
		(import "h" "two" (func $two (result i32 i32)))
		(import "h" "sum" (func $sum (param i32 i64 f64) (result f64)))
		(func (export "mix") (result i32) (call $mix (i32.const 3) (i32.const 4)))
		(func (export "sub") (result i32) (i32.sub (call $two)))
		(func (export "sum") (result f64)
			(call $sum (i32.const 1) (i64.const 20) (f64.const 300))))`,
		binary.Features{}, hostImports(map[string]Extern{
			"mix": HostExtern(ft([]binary.ValType{binary.I32, binary.I32}, []binary.ValType{binary.I32}),
				func(_ *Caller, args []Value) ([]Value, error) {
					return []Value{I32(10*int32(args[0].Bits) + int32(args[1].Bits))}, nil
				}),
			"two": HostExtern(ft(nil, []binary.ValType{binary.I32, binary.I32}),
				func(_ *Caller, _ []Value) ([]Value, error) {
					return []Value{I32(7), I32(3)}, nil
				}),
			"sum": HostExtern(ft([]binary.ValType{binary.I32, binary.I64, binary.F64}, []binary.ValType{binary.F64}),
				func(_ *Caller, args []Value) ([]Value, error) {
					for _, a := range args {
						gotTypes = append(gotTypes, a.Type)
					}
					f := float64(int32(args[0].Bits)) + float64(int64(args[1].Bits)) + args[2].Float64()
					return []Value{F64(f)}, nil
				}),
		}))

	for _, tc := range []struct {
		name    string
		fn      string
		want    uint64
		reverse string
	}{
		{"arguments in declaration order", "mix", 34, "43 means `hostArgs` handed the host its arguments backwards"},
		{"results in declaration order", "sub", uint64(uint32(4)), "0xfffffffc (-4) means `pushHostResults` pushed them backwards"},
	} {
		out, err := in.Invoke(tc.fn)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(out) != 1 || out[0].Bits != tc.want {
			t.Errorf("%s: %q = %v, want %d — %s", tc.name, tc.fn, out, tc.want, tc.reverse)
		}
	}

	out, err := in.Invoke("sum")
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if len(out) != 1 || out[0].Float64() != 321 {
		t.Errorf("sum = %v, want 321 — a different total means the mixed parameter types were taken "+
			"off the stack in the wrong order or through the wrong slot", out)
	}
	if want := []binary.ValType{binary.I32, binary.I64, binary.F64}; !valTypesEqual(gotTypes, want) {
		t.Errorf("the host was handed parameter types %v, want %v — a host function reads its "+
			"arguments' types off the Value, so this is part of the ABI and not a detail", gotTypes, want)
	}
}

// TestAHostCallReachedThroughAnotherInstanceRunsOnTheCallersThread is the two-instance row: the results
// arrive from the right function, and `Caller.Thread` names the thread that made the call.
//
// **It does not decide which *world* the call belongs to, and an earlier draft of this comment claimed
// it did.** `Caller.ctx` and `Caller.tid` are both read off `st.t`, so every assertion below answers the
// same way whichever instance `callHost` takes its world from — the claim was about the fixture's shape
// rather than about anything asserted here, which is a test comment stating a property its own
// assertions cannot see. The world question has its own row, and it is the next one:
// `TestAHostCallIsCountedAndWaitedForByTheCallersWorld`.
//
// # The shape, and the shape it is *not*
//
// `top` imports a **defined** function from `mid`, and `mid`'s body calls `mid`'s host import. So the
// guest entry is `top`'s, the thread is `top`'s, and the host function belongs to `mid` — which is what
// makes `thread.world`'s question a real one rather than a hypothetical: the counter, the closed check
// and the cancellation all have to pick one of the two instances, and picking differently for different
// ones is a `Close` that waits on a thread it cannot cancel.
//
// **It is deliberately not `A imports from B, B re-exports the host`,** which is what a reader expects
// here and which this engine refuses one layer earlier: `importTypeMismatch` answers *"a supplier that
// does not define its own export"* for any re-exported function import, host or not, and that
// over-rejection is flagged at its own site and predates this slice. So the recursion in `resolveCall`
// that a host extern's nil `owner` would nil-deref is not reachable from a link today. The walk's
// widened return still earns itself — the compiler is what forced all five resolved-callee sites to name
// an arm, and *an unreachable path is a path a later link change makes reachable* — but this test
// asserts the reachable shape and says so, rather than claiming coverage of the one it cannot build.
//
// `mid` also defines a decoy at the index `top` imports from, so a resolution that landed on the wrong
// slot answers 11 rather than failing.
func TestAHostCallReachedThroughAnotherInstanceRunsOnTheCallersThread(t *testing.T) {
	var ranOn ThreadID

	mid := hostLink(t, `(module
		(import "h" "f" (func $f (result i32)))
		(func (export "decoy") (result i32) (i32.const 11))
		(func (export "viaHost") (result i32) (call $f)))`, binary.Features{},
		hostImports(map[string]Extern{
			"f": HostExtern(ft(nil, []binary.ValType{binary.I32}), func(c *Caller, _ []Value) ([]Value, error) {
				ranOn = c.Thread()
				return []Value{I32(42)}, nil
			}),
		}))

	top := hostLink(t, `(module
		(import "m" "viaHost" (func $f (result i32)))
		(func (export "call") (result i32) (call $f)))`, binary.Features{}, exportsOf(mid))

	out, err := top.Invoke("call")
	if err != nil {
		t.Fatalf("call into another instance's host import: %v", err)
	}
	if len(out) != 1 || out[0].Bits != 42 {
		t.Errorf("a call crossing into another instance's host import = %v, want 42 — 11 is the decoy "+
			"at the neighbouring index", out)
	}
	if ranOn != top.host.id {
		t.Errorf("the host call reported thread %d, want %d — the thread is the *caller's*, and %d "+
			"would be the id of the instance that merely declared the import", ranOn, top.host.id, mid.host.id)
	}
}

// TestAHostCallIsCountedAndWaitedForByTheCallersWorld is the row that decides `thread.world`'s question,
// and it is here because writing the test above is what found the defect it now guards.
//
// # The two worlds answer differently, and only one of them can cancel the call
//
// `top` calls into `mid`, whose body calls `mid`'s host import. The call therefore has two candidate
// owners — the instance that *declared* the import (`mid`) and the world of the thread that is *running*
// (`top`'s) — and `callHost`'s first draft took `in.world`, the declarer's, because `callHost` is a method
// on the declaring instance. That is the wrong subject for both things the world is used for here:
//
//   - **`mid.Close()` would wait on a call it cannot cancel.** H-3's shutdown is cancel-then-wait, and
//     `Close` cancels *its own members*. The running thread is `top.host`, which is not one of them, so a
//     count kept in `mid`'s world makes `mid.Close()` set up its `hostIdle` channel and block forever on
//     an embedder nothing will ever interrupt. **A hang in the engine and not in the embedder**, which is
//     the distinction `Caller.Context`'s comment rests on.
//   - **`top.Close()` would not wait at all**, because `top`'s count is zero — so it cancels the thread
//     and returns while the embedder is still inside, which is exactly the H-3 breach
//     `TestCloseCancelsTheHostCallsContextAndWaitsForItToReturn` catches for the one-instance case.
//
// So both assertions below are on `Close`, one per direction, and they fail in opposite directions under
// the same defect.
//
// **Falsified** by taking the world from `in` rather than from `st.t`: `mid.Close()` does not return
// inside the deadline and `finished` is false after `top.Close()`. The injection was run over the whole
// package and **this is its only witness** — every other row here has one instance, where the two worlds
// are the same object and the defect is unobservable. So the population that covers this is one test, and
// the deadline below is what keeps it from covering it as a hang. The deadline exists so that failure is
// a FAIL with a message rather than the package's own timeout, and it is bounded rather than waited on
// forever for that reason alone — it is generous enough that it can only make this an unreliable *pass*,
// and `top.Close()` below releases the embedder either way, so a failing run does not leak the goroutine.
func TestAHostCallIsCountedAndWaitedForByTheCallersWorld(t *testing.T) {
	var (
		entered  = make(chan struct{})
		finished atomic.Bool
		invoked  = make(chan error, 1)
	)

	mid := hostLink(t, `(module
		(import "h" "park" (func $park (result i32)))
		(func (export "viaHost") (result i32) (call $park)))`, binary.Features{},
		hostImports(map[string]Extern{
			"park": HostExtern(ft(nil, []binary.ValType{binary.I32}), func(c *Caller, _ []Value) ([]Value, error) {
				close(entered)
				<-c.Context().Done()
				// The sleep is the subject and not the wait: an embedder with unwinding of its own to
				// do after it observes cancellation. `Close` must still be inside its wait when this
				// returns, which is what `finished` reads.
				time.Sleep(20 * time.Millisecond)
				finished.Store(true)
				return nil, c.Context().Err()
			}),
		}))

	top := hostLink(t, `(module
		(import "m" "viaHost" (func $f (result i32)))
		(func (export "call") (result i32) (call $f)))`, binary.Features{}, exportsOf(mid))

	go func() { _, err := top.Invoke("call"); invoked <- err }()
	<-entered

	midClosed := make(chan error, 1)
	go func() { midClosed <- mid.Close() }()
	select {
	case err := <-midClosed:
		if err != nil {
			t.Errorf("mid.Close() = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("mid.Close() did not return while a host call it merely *declared* was running on " +
			"another instance's thread, so the call is counted in the declarer's world — and the " +
			"declarer cannot cancel that thread, which makes this a hang in the engine")
	}

	if err := top.Close(); err != nil {
		t.Fatalf("top.Close(): %v", err)
	}
	if !finished.Load() {
		t.Error("top.Close() returned while the host call was still inside the embedder, so the " +
			"caller's world is not counting the call it is the only world that can cancel")
	}

	if err := <-invoked; !errors.Is(err, ErrHostTrap) || !errors.Is(err, context.Canceled) {
		t.Errorf("the in-flight Invoke returned %v, want a host trap wrapping context.Canceled", err)
	}
}

// TestAHostImportWhoseTypeDiffersIsRefusedOnTheLinkChannel pins both halves of the refusal: which
// channel it arrives on, and that the message is about the *host* function.
//
// **The message matters here for a specific reason.** `importTypeMismatch`'s first act is
// `ext.typeSpace()`, which renders a supplier's module — and a host extern has none, so running the
// ordinary path on one produces *"a supplier with no defining module"*: a true sentence about the wrong
// subject, which is the worst kind of error message because it sends the reader to look for a linking
// bug. So the host arm is intercepted ahead of it, and this asserts the interception by asserting the
// text a reader gets.
func TestAHostImportWhoseTypeDiffersIsRefusedOnTheLinkChannel(t *testing.T) {
	img, err := text.EncodeModule([]byte(`(module
		(import "h" "f" (func $f (param i32) (result i32)))
		(func (export "call") (result i32) (call $f (i32.const 1))))`))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Declared `(i32) -> i32`, supplied `(i64) -> i32`: one parameter, wrong width, which is the
	// mismatch a structural comparison must catch and a "same arity" check would not.
	_, _, lerr := InstantiateLinked(m, hostImports(map[string]Extern{
		"f": HostExtern(ft([]binary.ValType{binary.I64}, []binary.ValType{binary.I32}),
			func(_ *Caller, _ []Value) ([]Value, error) { return []Value{I32(0)}, nil }),
	}))
	if !errors.Is(lerr, ErrLinkFailed) {
		t.Fatalf("a host import of the wrong type gave %v, want ErrLinkFailed — a type mismatch at "+
			"link time is a link failure and never a trap", lerr)
	}
	if got := lerr.Error(); !strings.Contains(got, "host function") {
		t.Errorf("the refusal reads %q, and it must name the host function as the supplier: "+
			"`importTypeMismatch` would render \"a supplier with no defining module\" here, which is "+
			"a true sentence about the wrong subject", got)
	}
}

// TestAHostFunctionsErrorArrivesAsErrHostTrapWithTheEmbeddersCauseInside is the failure channel.
//
// Wrapped rather than returned bare, and both halves are asserted: an embedder needs `errors.Is` on
// its *own* sentinel to work through the engine, and the engine needs a reader of a bare
// `context.Canceled` coming out of an `Invoke` to be able to tell that a host function is where it came
// from. One `%w: %w` gives both; either half alone loses one of them.
func TestAHostFunctionsErrorArrivesAsErrHostTrapWithTheEmbeddersCauseInside(t *testing.T) {
	boom := errors.New("the embedder's own sentinel")

	in := hostLink(t, `(module
		(import "h" "f" (func $f (result i32)))
		(func (export "call") (result i32) (call $f)))`, binary.Features{},
		hostImports(map[string]Extern{
			"f": HostExtern(ft(nil, []binary.ValType{binary.I32}), func(_ *Caller, _ []Value) ([]Value, error) {
				return nil, boom
			}),
		}))

	_, err := in.Invoke("call")
	if !errors.Is(err, ErrHostTrap) {
		t.Errorf("a host function's error came back as %v, want it to wrap ErrHostTrap", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("a host function's error came back as %v, and the embedder's own cause must survive "+
			"the crossing or `errors.Is` at the embedder cannot ask about it", err)
	}
}

// TestAHostFunctionThatDoesNotHonourItsDeclaredTypeIsRefused covers the direction nothing has
// validated.
//
// The guest side of a host call is checked by the validator; the *host* side is checked by nobody, so a
// Go function returning two values where its type declares one would corrupt the caller's operand stack
// exactly as a wasm callee doing so would. Rows:
//
//   - **too few / too many** results — `ErrHostSignature`, and the stack is left as it was found. The
//     count is checked before anything is pushed, so a half-written stack is not one of the outcomes,
//     and the follow-up call below is what asserts that: an instance whose stack had been left dirty
//     answers the *next* call wrongly.
//   - **the wrong numeric type** — `ErrHostSignature`. `i64` where `i32` is declared is the row a
//     `Bits`-only comparison would pass, since the bit pattern is a fine i32.
//   - **a non-null funcref** — `ErrUnsupportedOp` and deliberately *not* `ErrHostSignature`: nothing is
//     wrong with the module or with the declared type, so the refusal names the engine, per
//     `Value.RefID`'s stated boundary and `invokeIndex`'s identical refusal on the way in.
func TestAHostFunctionThatDoesNotHonourItsDeclaredTypeIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		decl binary.FuncType
		give []Value
		want error
	}{
		{"no results where one is declared", ft(nil, []binary.ValType{binary.I32}), nil, ErrHostSignature},
		{
			"two results where one is declared", ft(nil, []binary.ValType{binary.I32}),
			[]Value{I32(1), I32(2)},
			ErrHostSignature,
		},
		{
			"an i64 where an i32 is declared", ft(nil, []binary.ValType{binary.I32}),
			[]Value{I64(1)},
			ErrHostSignature,
		},
		{
			"a numeric value where a reference is declared", ft(nil, []binary.ValType{binary.ExternRef}),
			[]Value{I32(0)},
			ErrHostSignature,
		},
		{
			"a non-null funcref", ft(nil, []binary.ValType{binary.FuncRef}),
			[]Value{{Type: binary.FuncRef, RefID: 1}},
			ErrUnsupportedOp,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The guest drops whatever comes back and answers a constant, so the *only* way this
			// invoke fails is the check under test — and the only way it succeeds when it should not
			// is a value that reached the stack.
			src := `(module
				(import "h" "f" (func $f (result ` + tc.decl.Results[0].String() + `)))
				(func (export "call") (result i32) (drop (call $f)) (i32.const 99))
				(func (export "clean") (result i32) (i32.const 99)))`
			in := hostLink(t, src, binary.Features{}, hostImports(map[string]Extern{
				"f": HostExtern(tc.decl, func(_ *Caller, _ []Value) ([]Value, error) {
					return tc.give, nil
				}),
			}))

			if _, err := in.Invoke("call"); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			// The refusal left no debris: a check that pushed before it finished checking would
			// leave the *instance* wrong rather than just this call, and the second invoke is the
			// only thing that can tell those apart.
			out, err := in.Invoke("clean")
			if err != nil {
				t.Errorf("a later call failed with %v after the refusal above, so the refusal left "+
					"the engine in a state it should not have touched", err)
			} else if len(out) != 1 || out[0].Bits != 99 {
				t.Errorf("a later call answered %v, want 99 — the refused host result reached the "+
					"stack before the check refused it", out)
			}
		})
	}
}

// TestAHostReferenceResultIsCheckedBySubtypingAndNotByIdentity is the other half of the result check,
// and it is the half that is wrong in the *accept* direction if `==` is used.
//
// A host result travels **inward**, which is the direction `invokeIndex`'s parameters travel and not the
// direction its results do, so it is compared through `typeOfRef`/`matchRefType` — the same `Match`
// relation `ref.test` evaluates. Identity refuses two things the reference accepts, and each is a row:
//
//   - **a null spelled at another type.** There is exactly one heaptype-free null (grave #266), so a
//     `NullRef(anyref)` serves an `externref` result and `==` on the static types never could.
//   - **a non-null host reference under an abstract result type.** Its dynamic type is `(ref any)` and a
//     nullable `anyref` admits it, `matchNull` being the whole of why.
//
// An accept-direction control, so its own vacuity matters: the guest *uses* the reference — `ref.is_null`
// — rather than dropping it, so a row cannot pass by the value never being looked at.
func TestAHostReferenceResultIsCheckedBySubtypingAndNotByIdentity(t *testing.T) {
	anyRef, ok := binary.AbstractRefType(binary.HeapAny, true)
	if !ok {
		t.Fatal("binary has no anyref, which the last row here is about")
	}

	for _, tc := range []struct {
		name    string
		spell   string
		decl    binary.ValType
		feats   binary.Features
		give    Value
		wantNil uint64
	}{
		// The two grave #266 rows: the *static* type of the Value the host hands back differs from
		// the declared result type, and identity would refuse both. There is exactly one
		// heaptype-free null, so a null spelled at any reference type is the null every nullable
		// reference type admits.
		{
			"a null funcref where externref is declared", "externref", binary.ExternRef,
			binary.Features{},
			NullRef(binary.FuncRef), 1,
		},
		{
			"a null externref where funcref is declared", "funcref", binary.FuncRef,
			binary.Features{},
			NullRef(binary.ExternRef), 1,
		},
		// A non-null host reference, which is the accept row: an externalized one at its own type…
		{
			"an externalized host reference under externref", "externref", binary.ExternRef,
			binary.Features{},
			ExternRef(7), 0,
		},
		// …and a bare one under an abstract result type, which is `matchNull` admitting the
		// non-nullable under the nullable and is the second direction identity cannot reach.
		{
			"a bare host reference under anyref", "anyref", anyRef,
			binary.Features{GC: true},
			HostRef(anyRef, 7), 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := hostLink(t, `(module
				(import "h" "f" (func $f (result `+tc.spell+`)))
				(func (export "call") (result i32) (ref.is_null (call $f))))`,
				tc.feats, hostImports(map[string]Extern{
					"f": HostExtern(ft(nil, []binary.ValType{tc.decl}),
						func(_ *Caller, _ []Value) ([]Value, error) { return []Value{tc.give}, nil }),
				}))

			out, err := in.Invoke("call")
			if err != nil {
				t.Fatalf("got %v, and every row here is a value the reference's own `Match` relation "+
					"admits — an error means the result is being compared by type identity", err)
			}
			if len(out) != 1 || out[0].Bits != tc.wantNil {
				t.Errorf("ref.is_null on the host's result = %v, want %d — the reference reached the "+
					"guest as something other than what the host returned", out, tc.wantNil)
			}
		})
	}
}

// TestAReturnCallToAHostFunctionKeepsItsResults is the tail-call arm, and it exists because two of
// `tailFrom`'s three steps invert for a host callee.
//
// `tailFrom` builds a frame, checks the base, and truncates the stack keeping nothing — a host callee has
// no frame, and its results must be *kept*. So the arm is a host call followed by `returnFrom` with the
// **current** frame's result counts, which is correct only because `return_call` is valid exactly where
// the callee's results match the caller's.
//
// Two results rather than one, and asymmetric: a `tailFrom` that truncated the stack would answer an
// error rather than a number, but an arm that kept only the top result would answer 7 here. That is the
// plausible wrong number this row exists to distinguish, and it needs a second result to exist at all.
func TestAReturnCallToAHostFunctionKeepsItsResults(t *testing.T) {
	in := hostLink(t, `(module
		(import "h" "two" (func $two (result i32 i32)))
		(func $tail (result i32 i32) (return_call $two))
		(func (export "call") (result i32) (i32.sub (call $tail))))`,
		tailGate, hostImports(map[string]Extern{
			"two": HostExtern(ft(nil, []binary.ValType{binary.I32, binary.I32}),
				func(_ *Caller, _ []Value) ([]Value, error) { return []Value{I32(7), I32(3)}, nil }),
		}))

	out, err := in.Invoke("call")
	if err != nil {
		t.Fatalf("a return_call to a host function: %v — a `tailFrom` on a host callee has no frame "+
			"to build and would fail here rather than answer wrongly", err)
	}
	if len(out) != 1 || out[0].Bits != 4 {
		t.Errorf("i32.sub over a tail-called host function's two results = %v, want 4 (7-3). "+
			"0xfffffffc is a reversed push; an error is a truncated stack", out)
	}
}

// TestCallerNamesTheThreadTheHostCallRanOn is [ADR 0069][0069]'s `Caller.Thread`, asserted on both kinds
// of thread there are.
//
// **The spawned row is the one that can fail.** Every host call in the tree before `Spawn` runs on
// `in.host`, so a `tid` read off the wrong object still answers the only id there is; two threads is the
// smallest world where "the calling thread" is a claim. The two ids must also *differ*, which is what
// distinguishes a correct read from a `Caller` that always names the instance's host thread.
//
// [0069]: ../../docs/decisions/0069-a-host-function-is-a-caller-and-a-value-slice-a-host-call-marks-its-thread-blocked-and-shutdown-is-its-own-terminal-method.md
func TestCallerNamesTheThreadTheHostCallRanOn(t *testing.T) {
	seen := make(chan ThreadID, 2)

	in := hostLink(t, `(module
		(import "h" "who" (func $who))
		(memory 1 1 shared)
		(func (export "entry") (param i32) (call $who))
		(func (export "call") (call $who)))`,
		binary.Features{Threads: true}, hostImports(map[string]Extern{
			"who": HostExtern(ft(nil, nil), func(c *Caller, _ []Value) ([]Value, error) {
				seen <- c.Thread()
				return nil, nil
			}),
		}))

	if _, err := in.Invoke("call"); err != nil {
		t.Fatalf("call on the host thread: %v", err)
	}
	onHost := <-seen
	if onHost != in.host.id {
		t.Errorf("a host call from a boundary Invoke reported thread %d, want %d — the id of the "+
			"thread the guest frame is running on", onHost, in.host.id)
	}

	entry, ok := in.exportedFunc("entry")
	if !ok {
		t.Fatal("no exported entry")
	}
	tid, err := in.Spawn(entry, 0, 0)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	onSpawned := <-seen
	if onSpawned != tid {
		t.Errorf("a host call from a spawned thread reported thread %d, want %d (the spawned tid). "+
			"%d would be the instance's host thread, which is the read that is right for one thread "+
			"and wrong for two", onSpawned, tid, in.host.id)
	}
	if onSpawned == onHost {
		t.Errorf("both host calls reported thread %d, so `Caller.Thread` is not distinguishing the "+
			"two threads it ran on at all", onSpawned)
	}
}

// TestCloseCancelsTheHostCallsContextAndWaitsForItToReturn is contract §5 H-3's teardown, both clauses.
//
// **The wait is on a signal and the sleep is the subject, not the wait.** The embedder's function sleeps
// *after* observing cancellation, which models an embedder with unwinding of its own to do; the test
// itself waits on channels. That distinction is the whole of *never sleep to wait* — a duration standing
// in for a completion signal is the banned thing, and a duration modelling work is what is being
// measured.
//
// **Falsified**: with the `<-idle` receive in `Close` deleted, `Close` returns while the embedder is
// still inside its 20 ms and this fails on `finished`; with the `cancelCtx` loop deleted, the embedder
// never observes `Done` and the test hangs to its own deadline rather than passing — which is why
// `Context()` being cancelled is asserted by the embedder returning at all, and not by a second flag.
func TestCloseCancelsTheHostCallsContextAndWaitsForItToReturn(t *testing.T) {
	var (
		entered  = make(chan struct{})
		finished atomic.Bool
		invoked  = make(chan error, 1)
	)

	in := hostLink(t, `(module
		(import "h" "park" (func $park))
		(func (export "call") (call $park)))`, binary.Features{},
		hostImports(map[string]Extern{
			"park": HostExtern(ft(nil, nil), func(c *Caller, _ []Value) ([]Value, error) {
				close(entered)
				<-c.Context().Done()
				time.Sleep(20 * time.Millisecond)
				finished.Store(true)
				return nil, c.Context().Err()
			}),
		}))

	go func() { _, err := in.Invoke("call"); invoked <- err }()
	<-entered

	if err := in.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !finished.Load() {
		t.Error("Close returned while the host function was still inside its call, so H-3's wait is " +
			"not waiting on the calls — an embedder still holding engine state would be torn down " +
			"underneath itself")
	}

	// The cancellation reaches the embedder as a context error, and the engine reports the whole thing
	// as a host trap: the call did not complete, and an embedder's `errors.Is(err, context.Canceled)`
	// is how it tells shutdown from a fault of its own.
	err := <-invoked
	if !errors.Is(err, ErrHostTrap) || !errors.Is(err, context.Canceled) {
		t.Errorf("the in-flight Invoke returned %v, want a host trap wrapping context.Canceled", err)
	}

	// Idempotent, and it does not hang the second time: `hostCalls` is zero, so there is nothing to
	// wait for and no channel to wait on.
	if err := in.Close(); err != nil {
		t.Errorf("a second Close returned %v, want nil", err)
	}
}

// TestAClosedInstanceBeginsNoHostCallAndSpawnsNoThread is H-3's terminality: a `Close`d instance refuses
// the two things that would outlive the wait `Close` just completed.
//
// A host call admitted after `Close` returned would be running outside that wait, which is `admit`'s
// indivisibility argument one subject over. A `Spawn` after it would add a member nothing will ever
// cancel. Both refusals are `ErrClosed`, which is *terminal* and deliberately not `ErrStopInProgress`'s
// transient refusal — a caller that retries on the latter would spin forever on this.
func TestAClosedInstanceBeginsNoHostCallAndSpawnsNoThread(t *testing.T) {
	var called atomic.Bool

	in := hostLink(t, `(module
		(import "h" "f" (func $f))
		(memory 1 1 shared)
		(func (export "entry") (param i32) (call $f))
		(func (export "call") (call $f)))`, binary.Features{Threads: true},
		hostImports(map[string]Extern{
			"f": HostExtern(ft(nil, nil), func(_ *Caller, _ []Value) ([]Value, error) {
				called.Store(true)
				return nil, nil
			}),
		}))

	if err := in.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := in.Invoke("call"); !errors.Is(err, ErrClosed) {
		t.Errorf("a host call on a closed instance gave %v, want ErrClosed", err)
	}
	if called.Load() {
		t.Error("the embedder's function ran on a closed instance, so the refusal is reported after " +
			"the call rather than instead of it")
	}

	entry, ok := in.exportedFunc("entry")
	if !ok {
		t.Fatal("no exported entry")
	}
	if _, err := in.Spawn(entry, 0, 0); !errors.Is(err, ErrClosed) {
		t.Errorf("Spawn on a closed instance gave %v, want ErrClosed — a thread admitted after Close "+
			"has nothing left that will cancel it", err)
	}
}

// TestAHostCallMarksItsThreadBlockedSoAStopArrives is the SP-2/SP-4 row, and it is the reason the
// blocked mark is in `callHost` at all.
//
// ADR 0067's arrival predicate is `blocked == callers`. A thread inside a host call still has its guest
// frame counted as a caller, so **without `enterBlocked` the thread reads to `Stop` as running guest
// code**: it will not reach a safepoint (it is parked in the embedder's channel receive), so `Stop` waits
// out its whole deadline and returns `ErrStopDeadline`. With the mark, SP-2 counts it as arrived without
// waking it, which is SP-4's *"a pause must not disturb a blocked host call"* in the same clause.
//
// **Nothing here is timing-shaped except the deadline, and the deadline is not the assertion.** The
// premise — the thread is inside the host call — is established by the embedder signalling, not hoped
// for, which is grave #598's ruling. The assertion is `Stop`'s verdict: `nil` under the mark,
// `ErrStopDeadline` without it. The deadline is generous because a shorter one would make this an
// unreliable *pass*, never an unreliable fail.
func TestAHostCallMarksItsThreadBlockedSoAStopArrives(t *testing.T) {
	var (
		entered  = make(chan struct{})
		release  = make(chan struct{})
		returned = make(chan struct{})
	)

	in := hostLink(t, `(module
		(import "h" "park" (func $park))
		(memory 1 1 shared)
		(func (export "entry") (param i32) (call $park)))`,
		binary.Features{Threads: true}, hostImports(map[string]Extern{
			"park": HostExtern(ft(nil, nil), func(_ *Caller, _ []Value) ([]Value, error) {
				close(entered)
				<-release
				close(returned)
				return nil, nil
			}),
		}))

	entry, ok := in.exportedFunc("entry")
	if !ok {
		t.Fatal("no exported entry")
	}
	if _, err := in.Spawn(entry, 0, 0); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	<-entered

	if err := in.Stop(30 * time.Second); err != nil {
		t.Fatalf("Stop with a thread parked in a host call: %v — ErrStopDeadline is the failure the "+
			"missing `blocked` mark produces, and it is not a slow machine", err)
	}

	// The pause did not disturb the call, which is SP-4's other half: the embedder is still inside its
	// receive, so a `Stop` that had somehow released it would show up here as a closed channel.
	select {
	case <-returned:
		t.Error("the host call returned during Stop, so the pause disturbed a blocked host call")
	default:
	}

	close(release)
	<-returned
	in.Resume()
}

// TestTheThreeSitesThatRefuseAResolvedHostCallee walks the arms of [ADR 0069][0069]'s five-site split
// from the other side: two dispatch (covered by every row above) and three refuse, each for its own
// reason, and a refusal that silently became a nil-deref is the failure this catches.
//
//   - **`Spawn`'s entry** — `ErrThreadEntry`. A host function runs no guest body, so it could reach no
//     safepoint and T-1's thread would be unstoppable. Refused on the *entry* channel rather than as an
//     unsupported op, because the argument is about the entry's shape.
//   - **the boundary's delegation** — `ErrUnsupportedOp`. This one *could* dispatch and chooses not to;
//     see `invokeIndex`'s own comment for the two things it would have to lie about.
//   - **a funcref** — `ErrUnsupportedOp`, reached here through a table slot. Option C's identity widening
//     is deferred, so a host function is not a `funcref` value and the engine says so rather than
//     resolving a nil owner.
//
// [0069]: ../../docs/decisions/0069-a-host-function-is-a-caller-and-a-value-slice-a-host-call-marks-its-thread-blocked-and-shutdown-is-its-own-terminal-method.md
func TestTheThreeSitesThatRefuseAResolvedHostCallee(t *testing.T) {
	host := hostImports(map[string]Extern{
		"f":     HostExtern(ft(nil, []binary.ValType{binary.I32}), func(*Caller, []Value) ([]Value, error) { return []Value{I32(1)}, nil }),
		"entry": HostExtern(ft([]binary.ValType{binary.I32}, nil), func(*Caller, []Value) ([]Value, error) { return nil, nil }),
	})

	t.Run("Spawn refuses a host function as an entry", func(t *testing.T) {
		in := hostLink(t, `(module
			(import "h" "entry" (func $entry (param i32)))
			(memory 1 1 shared)
			(export "entry" (func $entry)))`, binary.Features{Threads: true}, host)
		idx, ok := in.exportedFunc("entry")
		if !ok {
			t.Fatal("no exported entry")
		}
		// The type is exactly T-1's shape, so this cannot pass by the *signature* check refusing it
		// first — which is the vacuity this row would otherwise have.
		if _, err := in.Spawn(idx, 0, 0); !errors.Is(err, ErrThreadEntry) {
			t.Errorf("Spawn on a host entry gave %v, want ErrThreadEntry", err)
		}
	})

	t.Run("the boundary refuses a re-exported host function", func(t *testing.T) {
		in := hostLink(t, `(module
			(import "h" "f" (func $f (result i32)))
			(export "f" (func $f)))`, binary.Features{}, host)
		if _, err := in.Invoke("f"); !errors.Is(err, ErrUnsupportedOp) {
			t.Errorf("Invoke of a re-exported host function gave %v, want ErrUnsupportedOp", err)
		}
	})

	t.Run("a host function is not a funcref", func(t *testing.T) {
		in := hostLink(t, `(module
			(type $t (func (result i32)))
			(import "h" "f" (func $f (result i32)))
			(table 1 funcref)
			(elem (i32.const 0) func $f)
			(func (export "indirect") (result i32) (call_indirect (type $t) (i32.const 0))))`,
			binary.Features{}, host)
		if _, err := in.Invoke("indirect"); !errors.Is(err, ErrUnsupportedOp) {
			t.Errorf("call_indirect through a table slot holding a host function gave %v, want "+
				"ErrUnsupportedOp — resolving it would read a nil owner", err)
		}
	})
}

// TestAHostFunctionWithATypeIndexBearingTypeIsNotLinkable is the named engine limit, asserted so that it
// is a *refusal* and not a nil-deref two layers down.
//
// `(ref $t)` and `(ref null $t)` name an index into a type section, and a host function has no type
// section — so there is nothing for the index to resolve against and no meaningful comparison to make
// against the importer's declared type. Refused at `HostExtern`'s link, naming the engine per `tableFor`'s
// rule, because nothing is wrong with the module.
//
// **The decode is a `Fatalf` and not a `Skipf`, which is a repair rather than a style choice.** It was
// written as a skip — *"the decoder will not take an indexed reference type at these features"* — and the
// skip is unreachable: this test passes, so the decoder does accept `(ref null $t)` at `GC: true`. A
// defensive skip over an input that is in fact accepted is *a skip is not a verdict* in its purest form,
// because the only world in which it fires is one where the premise has broken and the right answer is a
// FAIL naming the broken premise. `TestEverySkipSiteIsLicensed` caught it, which is the control doing
// exactly what it is for: it asked for a licence, and the honest licence turned out not to exist.
func TestAHostFunctionWithATypeIndexBearingTypeIsNotLinkable(t *testing.T) {
	img, err := text.EncodeModule([]byte(`(module
		(type $t (func))
		(import "h" "f" (func $f (param (ref null $t))))
		(func (export "call") (call $f (ref.null $t))))`))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := (&binary.Decoder{Features: binary.Features{GC: true}}).DecodeModule(img)
	if err != nil {
		t.Fatalf("the decoder refused an indexed reference type at GC features: %v — this test's "+
			"premise is that it accepts one, so the module below no longer reaches the link where the "+
			"refusal under test lives", err)
	}
	_, _, lerr := InstantiateLinked(m, hostImports(map[string]Extern{
		"f": HostExtern(ft([]binary.ValType{binary.RefType(0, true)}, nil),
			func(*Caller, []Value) ([]Value, error) { return nil, nil }),
	}))
	if !errors.Is(lerr, ErrLinkFailed) {
		t.Fatalf("a host function declaring an indexed reference type linked with %v, want "+
			"ErrLinkFailed — the index names nothing on the host side", lerr)
	}
	if got := lerr.Error(); !strings.Contains(got, "engine limit") {
		t.Errorf("the refusal reads %q, and it must name the engine rather than the module: the "+
			"module is valid and it is this boundary that cannot express the type", got)
	}
}

// valTypesEqual compares two valtype slices. `slices.Equal` would do, and this exists so the failure
// above can be one line.
func valTypesEqual(a, b []binary.ValType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestAnEmbedderPanicLeavesTheEngineMarksClean is [#650][650]'s acceptance test, and
// [ADR 0070][0070]'s. Four channels, because the state a panic leaves behind is visible in four
// places and only one of them is loud.
//
// # The test #650 asked for cannot be written, and the measurement is why
//
// The issue's closing line asks for *"a `Stop` that must not report arrival"*. That test fails on
// correct code. `Stop`'s predicate is `blocked == callers`, a **difference**; a panic out of `h.fn`
// leaks one of each, so the leaked thread and the clean thread both satisfy it. Measured before the
// repair: `callers=1 blocked=1`, `Stop` returns `nil` — and after it, `callers=0 blocked=0`, `Stop`
// returns `nil`. Both are *arrived*, and for a genuinely idle thread that is the right answer, because
// any re-entry reaches `enterFrame`, whose first statement is `st.t.poll()`.
//
// So the oracle is the counters and the crossing parity, not `Stop`'s verdict. `Stop` is asserted
// anyway, and it is not vacuous: it discriminates against **option A** — repairing `callHost` and not
// `invokeIndex` leaves `callers=1 blocked=0`, and this call then waits out its whole deadline to report
// `0 of 1 arrived` for a thread executing nothing. That measured state is what the arm is here to catch
// if either half of the repair is removed.
//
// # The crossing number is derived, not read off a run
//
// Four: `invokeIndex`'s `enterGuest`/`leaveGuest` pair, plus the excursion `callHost` opens with
// `leaveGuest` and closes with `enterGuest`. Unrepaired it measured **3**, because the panic skipped the
// close — an odd delta, which is the one channel `TestEveryBoundaryCrossingIsPaired` could have seen,
// and only for an odd number of leaks inside its own delta. Pinned here as the exact number for the
// reason a floor is not a census.
//
// # The panic still belongs to the embedder
//
// The first arm requires a panic to come *out* of `Invoke`, which is ADR 0070's rejection of option B
// made falsifiable: a later `recover` at the boundary would convert an embedder's bug into an engine
// error, and this arm fails the moment one is added.
//
// [650]: https://github.com/scttfrdmn/burroughs/issues/650
// [0070]: ../../docs/decisions/0070-an-embedder-panic-is-repaired-inside-the-defers-that-already-exist.md
func TestAnEmbedderPanicLeavesTheEngineMarksClean(t *testing.T) {
	in := hostLink(t, `(module
		(import "h" "boom" (func $boom))
		(func (export "call") (call $boom)))`,
		binary.Features{}, hostImports(map[string]Extern{
			"boom": HostExtern(ft(nil, nil), func(*Caller, []Value) ([]Value, error) {
				panic("the embedder's own panic")
			}),
		}))

	before := crossings()
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("nothing panicked out of Invoke, so either the host call did not run or the " +
					"engine recovered it. ADR 0070 chose not to recover: an embedder's panic is the " +
					"embedder's bug and it keeps its own value and stack (option B, rejected)")
			}
			if s, ok := r.(string); !ok || s != "the embedder's own panic" {
				t.Errorf("the recovered value is %#v, want the embedder's own panic value — a "+
					"re-panic wrapping it would lose the original traceback", r)
			}
		}()
		_, _ = in.Invoke("call")
	}()

	if got := crossings() - before; got != 4 {
		t.Errorf("the recovered panic moved the boundary counter by %d, want 4: `invokeIndex`'s "+
			"enter/leave pair plus the excursion `callHost` opens with `leaveGuest` and closes with "+
			"`enterGuest`. 3 is the unrepaired number — the panic skipped the close, leaving the "+
			"count odd for whatever runs next (ADR 0070)", got)
	}

	in.world.mu.Lock()
	callers, blocked := in.host.callers, in.host.blocked
	in.world.mu.Unlock()
	if callers != 0 || blocked != 0 {
		t.Fatalf("after a recovered embedder panic the thread carries callers=%d blocked=%d, want 0 "+
			"and 0.\n1 and 1 is the unrepaired leak: `Stop` still reads `blocked == callers` and "+
			"answers *arrived*, so the breach is silent and the counters grow one pair per panic.\n"+
			"1 and 0 is option A — `callHost` repaired without `invokeIndex` — where every later "+
			"`Stop` waits out its deadline for a thread executing nothing (#650, ADR 0070)",
			callers, blocked)
	}

	if err := in.Stop(2 * time.Second); err != nil {
		t.Errorf("Stop: %v — with both marks clean this thread is idle and arrival is immediate. A "+
			"deadline expiry here is the option-A state (callers leaked, blocked not), which is #650 "+
			"made loud rather than repaired", err)
	} else {
		in.Resume()
	}

	// The half that always worked, asserted as the contrast: `endHostCall` was already deferred, so the
	// in-flight count is right and `Close` returns. In its own goroutine because the failure mode is a
	// hang, and a wedged test reports nothing.
	closed := make(chan error, 1)
	go func() { closed <- in.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("Close did not return after a recovered embedder panic — `endHostCall` is the one " +
			"half of `callHost`'s four that was always deferred, so a hang here means the host-call " +
			"count leaked and `Close` is waiting for a call that has already unwound")
	}
}

// TestASpawnEntryPanicLeavesNoCallerCounted is ADR 0070's third site, and its precondition is stated
// before its assertion because the two are unusually far apart.
//
// **An embedder's panic still ends the process, and the frame that now recovers is `runEntry`'s own.**
// This doc used to say the repair was there because
// [#12](https://github.com/scttfrdmn/burroughs/issues/12) *"is a `recover` above this frame by
// construction"* — a forecast about a join that had not been built. #12 landed ([ADR 0071][0071]) and the
// forecast is half right in a way worth stating rather than deleting: there *is* a `recover` now, and it
// is **inside** this frame rather than above it, because T-5.4's terminal unwind is a sentinel panic that
// `runEntry`'s existing `defer` folds into an `ErrTerminated`. What it deliberately does not catch is any
// *other* panic value: a non-sentinel `r` is re-panicked, so an embedder's `panic("boom")` reaches the
// goroutine's `defer`s and ends the process exactly as before. The leak this witnesses is therefore still
// not reachable in a released engine — and the repair still matters, because the sentinel path *is* now a
// recovered unwind through this same straight-line region, and a skipped `leaveCall` there is
// `invokeIndex`'s leak with `Close` waiting on it.
//
// So this test supplies the outer `recover` that the re-panic reaches, and calls `runEntry` directly on
// its own goroutine, which is what the `go` statement in `spawn` does one frame up. That is the only way
// the repair can be *watched*: an unfalsifiable protection is not a protection, and going through `Spawn`
// would take the test binary down with the panic instead of asserting anything.
//
// [0071]: ../../docs/decisions/0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md
func TestASpawnEntryPanicLeavesNoCallerCounted(t *testing.T) {
	in := hostLink(t, `(module
		(import "h" "boom" (func $boom))
		(func (export "entry") (param i32) (call $boom)))`,
		binary.Features{}, hostImports(map[string]Extern{
			"boom": HostExtern(ft(nil, nil), func(*Caller, []Value) ([]Value, error) {
				panic("the embedder's own panic, on a spawned thread")
			}),
		}))

	c, err := in.resolveCall(exportedFuncIndex(t, in, "entry"))
	if err != nil {
		t.Fatalf("resolveCall: %v", err)
	}
	th, err := in.newThread()
	if err != nil {
		t.Fatalf("newThread: %v", err)
	}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("nothing panicked out of runEntry, so this test's premise is absent rather " +
					"than its assertion failing")
			}
		}()
		_ = in.runEntry(th, c.fn, c.ft, 0, 0)
	}()

	in.world.mu.Lock()
	callers, blocked := th.callers, th.blocked
	in.world.mu.Unlock()
	if callers != 0 || blocked != 0 {
		t.Fatalf("the spawned thread's entry panicked and left callers=%d blocked=%d, want 0 and 0 — "+
			"`runEntry`'s `enterCall`/`leaveCall` is straight-line, so the panic skips the uncount "+
			"unless the `defer` that already carries `leaveGuest` repairs it (ADR 0070)",
			callers, blocked)
	}
}

// TestAPanicUnwindingDuringAStopDoesNotPark witnesses the half of [ADR 0070][0070] the other two tests
// cannot see: that the panic path clears the blocked mark **without polling**.
//
// With no stop in flight a poll returns immediately, so `unmarkBlocked` and `leaveBlocked` are
// indistinguishable — which would leave the choice between them asserted and unwatched. This test makes
// a stop be in flight at the moment the panic unwinds, and it does so without a race: the host function
// calls `Stop` **itself**. That call returns immediately and successfully, because the thread it is
// walking is the one inside this very host call and its marks say arrived (`blocked == callers`, SP-2's
// host-call half). So by the time `panic` runs, `w.resume != nil` and `stopReq` is set on this thread.
//
// A poll on the unwind would park there until a `Resume` that only the embedder can issue — and the
// embedder is the party whose `recover` is still one frame away. That is why `Stop`'s completion must not
// depend on it: the panic would be held inside the stop's round, and SP-4 has no clause that lets a
// round's completion wait on an embedder. The failure mode is a hang, so the arm is a timeout rather
// than a comparison.
//
// [0070]: ../../docs/decisions/0070-an-embedder-panic-is-repaired-inside-the-defers-that-already-exist.md
func TestAPanicUnwindingDuringAStopDoesNotPark(t *testing.T) {
	var in *Instance
	stopErr := make(chan error, 1)
	in = hostLink(t, `(module
		(import "h" "boom" (func $boom))
		(func (export "call") (call $boom)))`,
		binary.Features{}, hostImports(map[string]Extern{
			"boom": HostExtern(ft(nil, nil), func(*Caller, []Value) ([]Value, error) {
				// Arrival is immediate and by the predicate rather than by a park: this thread is
				// inside a host call, so `blocked == callers` already holds.
				stopErr <- in.Stop(10 * time.Second)
				panic("the embedder's own panic, with a stop in flight")
			}),
		}))

	unwound := make(chan any, 1)
	go func() {
		defer func() { unwound <- recover() }()
		_, _ = in.Invoke("call")
	}()

	if err := <-stopErr; err != nil {
		t.Fatalf("the host function's own Stop returned %v — this test's premise is that a thread "+
			"inside a host call is already arrived (SP-2's host-call half, ADR 0069), so a failure "+
			"here is the premise missing rather than the assertion firing", err)
	}
	select {
	case r := <-unwound:
		if r == nil {
			t.Fatal("no panic came out of Invoke")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the panic did not reach the embedder within 10s: the unwinding thread parked at a " +
			"safepoint. That is `leaveBlocked` on the panic path instead of `unmarkBlocked` — it polls, " +
			"the stop this host function started is still in flight, and the park waits for a `Resume` " +
			"the embedder cannot issue because its `recover` has not run yet (ADR 0070)")
	}
	in.Resume()
}
