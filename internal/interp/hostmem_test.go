// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"

	wbin "github.com/scttfrdmn/burroughs/internal/binary"
)

// le32 renders a guest i32 the way the guest's own `i32.store` does, so a row's expected bytes are
// written once as a number and never spelled out as four literals — the shape a hand-written
// little-endian array gets wrong in exactly the byte a reader skims past.
func le32(v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return b[:]
}

// TestACallerReadsAndWritesTheGuestsMemory is the accept direction for Scott's ruling on the #651
// review: *"`Caller` gets guest-memory access — but as copying accessors, not a view."*
//
// **A round trip in one guest function, so a half-working accessor cannot pass.** The guest stores a
// value, the host reads it and writes a different one, the guest loads the word back and returns it.
// A `Read` at the wrong address sees zero, a `Write` that lands nowhere leaves the guest's own value
// in place, and either failure names a distinct number — which is the property the two constants are
// chosen for. They differ in every byte, so a partially-copied word is not a value either side
// expects.
func TestACallerReadsAndWritesTheGuestsMemory(t *testing.T) {
	const (
		byGuest = 0x11223344
		byHost  = 0x55667788
		addr    = 16
	)
	var seen []byte
	in := hostLink(t, `(module
		(import "h" "swap" (func $swap))
		(memory 1)
		(func (export "run") (result i32)
			(i32.store (i32.const 16) (i32.const 0x11223344))
			(call $swap)
			(i32.load (i32.const 16))))`, wbin.Features{},
		hostImports(map[string]Extern{
			"swap": HostExtern(ft(nil, nil), func(c *Caller, _ []Value) ([]Value, error) {
				got, err := c.Read(addr, 4)
				if err != nil {
					return nil, err
				}
				seen = got
				return nil, c.Write(addr, le32(byHost))
			}),
		}))

	res, trap := in.Invoke("run")
	if trap != nil {
		t.Fatalf("run: %v", trap)
	}
	if want := le32(byGuest); string(seen) != string(want) {
		t.Errorf("the host read %x at %d, want %x — the guest stored that word before calling, so a "+
			"zero here is a Read at the wrong address and anything else is a Read of the wrong width",
			seen, addr, want)
	}
	if len(res) != 1 || res[0] != I32(byHost) {
		t.Errorf("the guest loaded %v back, want %v: the host wrote that word through `Caller.Write` "+
			"while the guest was parked in the call. %v means the write landed nowhere and the guest's "+
			"own value survived", res, []Value{I32(byHost)}, []Value{I32(byGuest)})
	}
}

// TestACallerReadReturnsACopyAndNotAViewOfTheLiveImage is **the ruling's own property**, and the only
// row in this file that fails if `Read` is written the obvious way.
//
// `memory.read` hands back `bs[ea : ea+n]` — a sub-slice of the live image — so `return c.mem.read(…)`
// compiles, passes every other row here, and is exactly the aliasing Scott's ruling forecloses: *"A
// retained slice would alias a memory that can grow and relocate — #575 and #622's exact subject —
// which is the soundness burden option B was rejected for."*
//
// **The mutation is a second `Write` through the same `Caller`**, which needs no concurrency and no
// `grow` to witness the aliasing: if the first `Read`'s result is a view, the `Write` between the two
// reads changes bytes the embedder already holds, and `first` reads back as `second`. A `grow`-based
// witness would be the sharper story and a worse test — it would depend on which `grow` arm runs, and
// the reserved-capacity arm deliberately does not move the pointer.
//
// Watched die over the whole package, and it took **two** rows rather than the one this comment first
// claimed. `return bs, nil` in place of the `copy` fails here on `first`, with both buffers reading
// `0x99999999`; it also fails `TestACallerReadsAndWritesTheGuestsMemory`, whose `seen` is likewise held
// across a `Write` and so reads back as the host's word instead of the guest's. That row witnesses the
// aliasing *incidentally* and reports it as a wrong address or a wrong width, which is a true message
// about the wrong cause — the reason this row exists is that its own failure names the mechanism. Nothing
// else in the package moved.
func TestACallerReadReturnsACopyAndNotAViewOfTheLiveImage(t *testing.T) {
	const (
		original = 0x11223344
		overwrit = 0x99999999
	)
	var first, second []byte
	in := hostLink(t, `(module
		(import "h" "peek" (func $peek))
		(memory 1)
		(func (export "run")
			(i32.store (i32.const 0) (i32.const 0x11223344))
			(call $peek)))`, wbin.Features{},
		hostImports(map[string]Extern{
			"peek": HostExtern(ft(nil, nil), func(c *Caller, _ []Value) ([]Value, error) {
				a, err := c.Read(0, 4)
				if err != nil {
					return nil, err
				}
				if werr := c.Write(0, le32(overwrit)); werr != nil {
					return nil, werr
				}
				b, err := c.Read(0, 4)
				if err != nil {
					return nil, err
				}
				first, second = a, b
				return nil, nil
			}),
		}))

	if _, trap := in.Invoke("run"); trap != nil {
		t.Fatalf("run: %v", trap)
	}
	if want := le32(original); string(first) != string(want) {
		t.Errorf("the bytes `Read` returned before an intervening `Write` now read %x, want %x. They "+
			"changed under the embedder, so `Read` handed back a view of the live image rather than a "+
			"copy of it — the aliasing Scott's #651 ruling refuses, and the reason is `memory.grow`'s "+
			"reallocating arm: a slice held across it names an abandoned array, where a *write* is lost "+
			"with nothing reporting it", first, want)
	}
	if want := le32(overwrit); string(second) != string(want) {
		t.Errorf("the second `Read` returned %x, want %x — a copy must still be a copy of the *current* "+
			"bytes, and this row is what stops the one above being satisfied by a `Read` that caches",
			second, want)
	}
}

// TestACallerAccessorRefusesAnOutOfBoundsRangeAndWritesNothing gives the embedder the guest's own
// vocabulary for the guest's own error.
//
// **The same `*Trap` value an `i32.load` would trap with**, rather than a second dialect of
// out-of-bounds for host callers — so an embedder's overrun and a guest's overrun compare equal, and
// `errors.Is` answers on both.
//
// **And nothing is written when the extent fails**, which is `memory.write`'s property inherited
// rather than re-argued: the whole extent is checked before a byte moves. This is asserted by reading
// the memory back, which is how `memory_trap.wast` asserts it of the guest's stores — a partial write
// followed by an error is the worst of the three available behaviours and the one nobody would notice.
func TestACallerAccessorRefusesAnOutOfBoundsRangeAndWritesNothing(t *testing.T) {
	const onePage = 65536
	var (
		readErr, writeErr error
		tail              []byte
	)
	in := hostLink(t, `(module
		(import "h" "probe" (func $probe))
		(memory 1)
		(func (export "run")
			(i32.store (i32.const 65532) (i32.const 0x11223344))
			(call $probe)))`, wbin.Features{},
		hostImports(map[string]Extern{
			"probe": HostExtern(ft(nil, nil), func(c *Caller, _ []Value) ([]Value, error) {
				// One byte past the last page, in each direction. The read straddles the end rather
				// than starting past it, which is the case a bound written `ea + n > len` admits.
				_, readErr = c.Read(onePage-2, 4)
				writeErr = c.Write(onePage-2, le32(0x99999999))
				tail, _ = c.Read(onePage-4, 4)
				return nil, nil
			}),
		}))

	if _, trap := in.Invoke("run"); trap != nil {
		t.Fatalf("run: %v", trap)
	}
	for _, tc := range []struct {
		what string
		err  error
	}{{"Read", readErr}, {"Write", writeErr}} {
		if !errors.Is(tc.err, trapOOB) {
			t.Errorf("a `Caller.%s` straddling the end of the memory returned %v, want the guest's own "+
				"%v. An embedder's overrun and a guest's overrun are one error value on purpose",
				tc.what, tc.err, trapOOB)
		}
	}
	if want := le32(0x11223344); string(tail) != string(want) {
		t.Errorf("after a refused `Write` the last word reads %x, want the guest's %x — the refusal "+
			"moved bytes before failing, so a trapping host write is observable in the memory", tail, want)
	}
}

// TestACallerReadChecksItsBoundsBeforeItAllocates is the row that would be a panic rather than a
// failure if the two statements were swapped.
//
// `Read` ends in `make([]byte, n)`, and `n` is the embedder's. With the bound checked first, `n` is
// known to be no larger than the memory and the `make` is safe; with the `make` first,
// `Read(0, math.MaxUint64)` is `makeslice: len out of range` — a **panic in the engine** on the exact
// path whose whole job is to refuse the access, and one that would then be #650's subject rather than
// an error the embedder can read.
//
// So this asserts a trap comes back at all. `math.MaxUint64` rather than a merely large number
// because a large one only wastes memory, and a test that passes by allocating a terabyte is not a
// test anybody wants to have run.
func TestACallerReadChecksItsBoundsBeforeItAllocates(t *testing.T) {
	var got error
	in := hostLink(t, `(module
		(import "h" "greedy" (func $greedy))
		(memory 1)
		(func (export "run") (call $greedy)))`, wbin.Features{},
		hostImports(map[string]Extern{
			"greedy": HostExtern(ft(nil, nil), func(c *Caller, _ []Value) ([]Value, error) {
				_, got = c.Read(0, math.MaxUint64)
				return nil, nil
			}),
		}))

	if _, trap := in.Invoke("run"); trap != nil {
		t.Fatalf("run: %v", trap)
	}
	if !errors.Is(got, trapOOB) {
		t.Errorf("`Read(0, math.MaxUint64)` returned %v, want %v. Reaching this line at all means no "+
			"panic happened, so the surviving failure is a wrong error; the *interesting* failure of "+
			"this row is a `makeslice: len out of range` crash, which means the allocation was written "+
			"above the bound check", got, trapOOB)
	}
}

// TestACallerInAModuleWithNoMemoryIsRefusedWithErrNoMemory covers the arm `hostMemory` answers
// `(nil, nil)` for, and the reason that arm exists rather than deferring to `memoryFor`.
//
// **A memoryless module must still be able to *call* a host function**, so the absence cannot be an
// error on the call path — this module's host call returns normally and only the access fails. And the
// refusal is `ErrNoMemory` rather than `memoryFor`'s `ErrNotValidated: … names memory 0 of 0`, because
// a module with no memory is perfectly valid and saying otherwise would be an error message asserting
// something the engine knows to be false.
func TestACallerInAModuleWithNoMemoryIsRefusedWithErrNoMemory(t *testing.T) {
	var readErr, writeErr error
	in := hostLink(t, `(module
		(import "h" "peek" (func $peek))
		(func (export "run") (call $peek)))`, wbin.Features{},
		hostImports(map[string]Extern{
			"peek": HostExtern(ft(nil, nil), func(c *Caller, _ []Value) ([]Value, error) {
				_, readErr = c.Read(0, 1)
				writeErr = c.Write(0, []byte{1})
				return nil, nil
			}),
		}))

	if _, trap := in.Invoke("run"); trap != nil {
		t.Fatalf("run: %v — a host call in a memoryless module must still run; only the access fails", trap)
	}
	for _, tc := range []struct {
		what string
		err  error
	}{{"Read", readErr}, {"Write", writeErr}} {
		if !errors.Is(tc.err, ErrNoMemory) {
			t.Errorf("`Caller.%s` in a module with no memory returned %v, want %v", tc.what, tc.err, ErrNoMemory)
		}
		if errors.Is(tc.err, ErrNotValidated) {
			t.Errorf("`Caller.%s` reported %v, which says this module failed validation. It did not — a "+
				"module with no memory is valid, and `hostMemory`'s `(nil, nil)` arm exists precisely so "+
				"that `memoryFor`'s \"names memory 0 of 0\" is never the answer here", tc.what, tc.err)
		}
	}
}

// TestACallerReachesTheDeclaringInstancesMemory pins which module's index space "memory 0" names, and
// **says plainly that it discriminated no defect**, because the neighbouring question did and the two
// are easy to conflate.
//
// The world question — which `Instance.Close` counts a host call — had *two* reachable answers, since a
// thread is its own object and `TestAHostCallIsCountedAndWaitedForByTheCallersWorld` catches the wrong
// one. This question has **one** reachable answer today: `callHost`'s receiver is `funcTarget.inst`,
// the instance whose import slot held the host function, and there is no second `*Instance` at the site
// to prefer over it — a `thread` carries no instance, and the re-export chain that would let a *third*
// module call a host import it did not declare is refused at link by `importTypeMismatch` (#651's own
// finding). So this row is a **regression oracle against a later widening**, not a witness.
//
// The fixture still discriminates, which is what makes it worth having: `top` and `mid` each own a
// distinct unshared memory and each writes its own signature word to offset 0 before the host runs. An
// implementation that resolved memory 0 from anything top-derived reads `top`'s word and fails here by
// name.
func TestACallerReachesTheDeclaringInstancesMemory(t *testing.T) {
	const (
		midsWord = 0x0000AAAA
		topsWord = 0x0000BBBB
	)
	var seen []byte
	mid := hostLink(t, `(module
		(import "h" "peek" (func $peek))
		(memory 1)
		(func (export "viaHost")
			(i32.store (i32.const 0) (i32.const 0x0000AAAA))
			(call $peek)))`, wbin.Features{},
		hostImports(map[string]Extern{
			"peek": HostExtern(ft(nil, nil), func(c *Caller, _ []Value) ([]Value, error) {
				got, err := c.Read(0, 4)
				seen = got
				return nil, err
			}),
		}))
	top := hostLink(t, `(module
		(import "m" "viaHost" (func $f))
		(memory 1)
		(func (export "call")
			(i32.store (i32.const 0) (i32.const 0x0000BBBB))
			(call $f)))`, wbin.Features{}, exportsOf(mid))

	if _, trap := top.Invoke("call"); trap != nil {
		t.Fatalf("call: %v", trap)
	}
	if want := le32(midsWord); string(seen) != string(want) {
		t.Errorf("the host read %x at offset 0, want mid's %x. %x is top's word, which would mean "+
			"memory 0 was resolved from the instance that *invoked* rather than the one whose module "+
			"wrote the import — the opposite of the world, which is the running thread's and not the "+
			"declarer's", seen, want, le32(topsWord))
	}
}
