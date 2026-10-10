// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/component"
	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// componentsGateEnv is the config gate for the component mechanism (`gate:components`, ADR 0084). It is
// **on by default** as of the 2026-09-11 flip (Scott's stamp on #720's pre-registered forecast, the
// stamp-tier event behaviour 4 requires): the value-type suite is verified against both oracles and every
// unmodeled kind is refused by name, so a default build runs a `wasi:cli/run` component. The gate remains
// present as a rollback: set to "0" to refuse — the same refuse-by-name path, only the default differs.
const componentsGateEnv = "BURROUGHS_COMPONENTS"

// componentsEnabled reports whether the `gate:components` config gate is on. **On is the default** (the
// 2026-09-11 flip); only an explicit "0" refuses. A component is recognized (IsComponent) and, unless
// explicitly gated off, run.
func componentsEnabled() bool { return os.Getenv(componentsGateEnv) != "0" }

// Feature names a WebAssembly proposal capability an embedder enables for a component's core
// modules ([ComponentConfig.Features]). It is the engine's first caller-supplied capability surface
// (ADR 0088, ruled on #798): a **named set** rather than one field per capability, because a field
// per capability would grow the public surface every time a feature question arrives — the drift
// that decision exists to prevent. The container is extensible; its contents are guest-driven, so
// exactly the capabilities a real consumer needs are defined and **an unrecognized value is refused
// by name** rather than ignored.
type Feature string

// FeatureThreads enables the threads proposal's surface — the 0xFE atomics region and shared
// memories — for a component's core modules.
//
// An embedder supplies it to say what their artifact requires, which is not the same as flipping a
// gate: `gate:threads`' default is unchanged, and a component that supplies nothing is refused
// exactly as before. The consumer that forced this capability is a Go component: Go's compiler emits
// atomics unconditionally — a hello-world that starts no goroutines carries over a thousand of them —
// so no Go component can decode under the default set.
//
// Whether `gate:threads` off *should* admit atomics when nothing can spawn is a separate, open
// question (#799), deliberately not answered by supplying this capability.
const FeatureThreads Feature = "threads"

// resolveFeatures maps the caller's named capabilities onto the decoder's feature set, starting from
// the default. An unrecognized name is **refused by name**: the set must not silently accept a
// capability this engine does not implement, which is the guard that lets the container be
// extensible without becoming a place where typos pass.
func resolveFeatures(fs []Feature) (bin.Features, error) {
	feats := bin.DefaultFeatures()
	for _, f := range fs {
		switch f {
		case FeatureThreads:
			feats.Threads = true
		default:
			// "feature", not "component feature": this resolver serves the wasip1 path too as of #813,
			// and a message naming one caller's path is wrong for the other. Found by running it there.
			return bin.Features{}, fmt.Errorf("%w: unknown feature %q; this build recognizes %q",
				ErrUnsupported, string(f), string(FeatureThreads))
		}
	}
	return feats, nil
}

// ComponentConfig runs a WebAssembly **component** whose world is `wasi:cli/run` — the p3 track's
// public entry (ADR 0084 / 0085, #694), the component analogue of [WASIP1Config].
//
// **The version is in neither the type nor the method, unlike `WASIP1Config`.** A component names its
// own WASI version in its imports (`wasi:cli/run@0.2.3`, `wasi:io/streams@0.2.3`, …), so the entry does
// not: `Run` reaches whatever `wasi:cli/run` the component exports. Preview 1's version is in its type
// name because the *host* chooses it; a component's is the guest's own declaration.
//
// This slice runs a `wasi:cli/run` world with the standard streams. Value-carrying component exports an
// embedder calls with typed arguments are a **separate surface that ADR 0085 decides and that has not
// landed**: a tagged union over the WIT value types, distinct from the core-wasm [Value], reached through
// `LoadComponent(wasm) (*Component, error)` — and, by that ADR's amendment 1,
// `Component.Call(ctx, name, args...)` with cancellation as a distinct outcome. It lands with its first
// embedder consumer, a component export called with values, which this stdio-run entry is not.
//
// This sentence named a `ComponentValue` type until grave #850's sibling, #856: **ADR 0085 contains no such
// identifier.** The citation resolved — the ADR exists and the link is well-formed — while the claim about
// what it says did not, which is the half no citation sweep can check. The value type's exported name is
// genuinely still open (the ADR commits to the *shape* and shows a `Variant("closed", nil)` constructor), so
// this comment names the shape and the entry points that are decided, rather than inventing the one that
// is not.
// # Where this differs from WASIP1Config, field by field
//
// The names are the same and two of the meanings are not, so an embedder who knows one does **not**
// know the other without this list. The difference is deliberate and is not being reconciled: a
// component is embedded in someone's program, and it should not write to that program's terminal
// unless the embedder hands it a writer. A `wasip1` module is the command-line shape, where inheriting
// the process's streams is what a caller expects.
//
//	Stdin    nil is an EMPTY STREAM here; nil is os.Stdin  for a WASIP1Config
//	Stdout   nil DISCARDS here;           nil is os.Stdout for a WASIP1Config
//	Stderr   nil DISCARDS here;           nil is os.Stderr for a WASIP1Config
//	Args     same meaning, no default program name is substituted
//	Features same meaning, same [Feature] names, same refusal of an unrecognized one
//
// Two fields a [WASIP1Config] has and this does not:
//
//	Env       a component reads its environment through wasi:cli/environment, not modelled here
//	Preopens  a component's filesystem capabilities are not modelled here
//
// Both are absences rather than defaults: a component gets no environment and no filesystem through
// this type, and nothing here grants either.
type ComponentConfig struct {
	Args   []string  // the component's argv, lowered by wasi:cli/environment.get-arguments
	Stdin  io.Reader // the component's stdin; nil means an empty stream (NOT os.Stdin — see above)
	Stdout io.Writer // the component's stdout; nil discards (NOT os.Stdout — see above)
	Stderr io.Writer // the component's stderr; nil discards (NOT os.Stderr — see above)

	// Features are the proposal capabilities this component's core modules require (ADR 0088).
	// Empty — the zero value — is the engine's default set, so an existing embedder is unaffected
	// and a component needing a gated capability is still refused by name. An unrecognized value is
	// refused by name rather than ignored.
	Features []Feature
}

// Run instantiates the component against a preview-2 host over the configured streams, calls its
// `wasi:cli/run` export, and returns the guest's exit code. A guest `exit(err)` is the code with a nil
// error; a load/link failure or a trap is `(0, err)` — the one-channel discipline [WASIP1Config.Run]
// keeps, so an exit does not read as a silent success nor a trap as exit 0.
//
// **Stdout binds at the writer, not by a second stdio implementation** (ADR 0083's implement-once /
// bind-twice, the shared reading): the same `io.Writer` a `WASIP1Config` would receive is handed to the
// component host, which writes the guest's `list<u8>` to it — the preview-1 and preview-2 sides marshal
// different ABIs (iovecs vs a canon list) onto one sink, rather than reimplementing stdio twice.
func (c ComponentConfig) Run(wasm []byte) (exitCode int, err error) {
	// `gate:components` is on by default as of the 2026-09-11 flip (ADR 0084, #720; behaviour 4's
	// stamp-tier event). The gate remains present as a rollback: an explicit `BURROUGHS_COMPONENTS=0`
	// refuses to run a component, by name — the same refuse-by-name path, only the default differs.
	if !componentsEnabled() {
		return 0, fmt.Errorf("%w: gate:components is off in this build (%s=0); unset it to run a component "+
			"(the gate is on by default as of the 2026-09-11 flip)",
			ErrGated, componentsGateEnv)
	}
	stdout, stderr := c.Stdout, c.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	h := component.NewHost(stdout, stderr, c.Stdin)
	h.Args = c.Args
	feats, ferr := resolveFeatures(c.Features)
	if ferr != nil {
		return 0, ferr
	}
	in, err := component.InstantiateWithHostFeatures(wasm, h, feats)
	if err != nil {
		// `gate:async` (ADR 0086) refuses an async component at bind, by name; it is a distinct gate from
		// `gate:components`, off by default. Wrap its sentinel as ErrGated so the boundary classifies it
		// exit 6 (well-formed, gate off — grave #301), the same outcome `gate:components` off produces.
		if errors.Is(err, component.ErrAsyncGated) {
			return 0, fmt.Errorf("%w: %w", ErrGated, err)
		}
		// `gate:async` on but the async tier's execution not yet built (#739 slice 1): the engine reached
		// something it does not implement in this phase — exit 5 (exitUnsupported), not a gate decline (the
		// gate is on) and not the invocation's own failure. It refuses by name, never a silent no-op.
		if errors.Is(err, component.ErrAsyncNotImplemented) {
			return 0, fmt.Errorf("%w: %w", ErrUnsupported, err)
		}
		return 0, err
	}
	defer in.Close()
	if err := in.CallRun(); err != nil {
		return 0, err
	}
	if h.Exited {
		return int(h.ExitCode), nil
	}
	return 0, nil
}

// LoadComponent loads a component against this configuration and returns it for [Component.Call],
// the capability-carrying twin of the bare [LoadComponent].
//
// # What it is for
//
// The bare [LoadComponent] hard-codes discarded output and no argv, so a component that prints
// through [Component.Call] writes **nowhere** and cannot be given arguments. [ComponentConfig.Run]
// threads those capabilities but only for `wasi:cli/run`, and it moves no values. This is the gap
// between them: configured streams *and* value calls.
//
// # The defaults are Run's, exactly
//
// `nil` Stdout and Stderr **discard**; a `nil` Stdin is an empty stream; `Args` and `Features` are
// passed as given. That is not a choice made here — it is the same handling
// [ComponentConfig.Run] performs, because two methods on one configuration must mean the same thing
// by that configuration. `TestAZeroConfigComponentWritesNowhereThroughBothPaths` holds them equal by
// driving both.
//
// **A zero-value configuration still sends output nowhere**, and that is deliberate rather than an
// oversight inherited from the bare form: a component embedded in someone's program should not write
// to that program's terminal unless the embedder hands it a writer. [ComponentConfig]'s own
// documentation records where these defaults differ from [WASIP1Config]'s.
//
// # Refusals
//
// Three classifications, which is **one more than either existing path performs** and the reason is
// that this method is the only one that can meet all three: it loads (so a form this engine does not
// model is reachable, as in the bare [LoadComponent]) *and* it resolves features (so the async tier's
// unimplemented execution is reachable, as in [ComponentConfig.Run]).
//
//	gate off, well-formed component  -> ErrGated
//	form this engine does not model  -> ErrUnsupported
//	async on, execution not built    -> ErrUnsupported
//
// Each is refused **at load**, which is the earliest point any of them can be known.
func (c ComponentConfig) LoadComponent(wasm []byte) (*Component, error) {
	if !componentsEnabled() {
		return nil, fmt.Errorf("%w: gate:components is off in this build (%s=0); unset it to load a "+
			"component (the gate is on by default as of the 2026-09-11 flip)",
			ErrGated, componentsGateEnv)
	}
	stdout, stderr := c.Stdout, c.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	h := component.NewHost(stdout, stderr, c.Stdin)
	h.Args = c.Args
	feats, ferr := resolveFeatures(c.Features)
	if ferr != nil {
		return nil, ferr
	}
	in, err := component.InstantiateWithHostFeatures(wasm, h, feats)
	if err != nil {
		if errors.Is(err, component.ErrAsyncGated) {
			return nil, fmt.Errorf("%w: %w", ErrGated, err)
		}
		if errors.Is(err, component.ErrUnsupportedForm) {
			return nil, fmt.Errorf("%w: %w", ErrUnsupported, err)
		}
		if errors.Is(err, component.ErrAsyncNotImplemented) {
			return nil, fmt.Errorf("%w: %w", ErrUnsupported, err)
		}
		return nil, err
	}
	return &Component{in: in}, nil
}

// IsComponent reports whether wasm is a WebAssembly component rather than a core module: it reads the
// 8-byte preamble's layer field (a component is layer 1, a core module layer 0), mirroring
// [IsWASIP1Command]'s fact-based detection without instantiating. Bad magic or a short header is
// `(false, err)`; the caller decides whether that is "not a component" or a failure to surface —
// `burroughs run` takes the former and lets a real load classify the bytes.
func IsComponent(wasm []byte) (bool, error) {
	if len(wasm) < 8 || string(wasm[0:4]) != "\x00asm" {
		return false, fmt.Errorf("burroughs: not a wasm binary (bad magic)")
	}
	layer := binary.LittleEndian.Uint16(wasm[6:8])
	return layer == 1, nil
}

// Component is a loaded, instantiated WebAssembly component whose exports an embedder can call with
// values — [ADR 0085]'s surface, landing with the `Call(ctx, …)` signature its amendment 1 approved.
//
// It mirrors [Instance] for core modules, and the asymmetry between them is deliberate and stated in that
// amendment: `Instance.Call(name, args...)` takes **no** context and is not changing, because it is
// released surface and adding a parameter would break every embedder to serve a path it does not have.
// So the component call carries a context and the core call does not. That is a real inconsistency in the
// public surface, accepted knowingly, and it was cheap only because `Component.Call` had not shipped.
//
// [ADR 0085]: https://github.com/scttfrdmn/burroughs/blob/main/docs/decisions/0085-the-public-component-api-surface-a-new-component-value-type-resource-handles-first-class-and-wit-typed-constructors.md
type Component struct {
	in *component.Instantiated

	// mu guards `closed` and the `active` increment, which must be atomic *together*: a Call that read
	// "not closed" and then incremented after Close had started counting would be a call Close does not
	// wait for.
	mu     sync.Mutex
	closed bool

	// active counts Calls in flight. A plain counter rather than a `sync.WaitGroup`, because `Wait` is
	// unbounded and Close's wait is bounded — see Close for why that bound exists.
	active int
}

// componentCloseBound is how long [Component.Close] waits for in-flight calls to finish cancelling
// before tearing down anyway.
//
// A guest is not obliged to cooperate: it can ignore `TASK_CANCELLED` and keep yielding, or sit in a host
// import that never returns. So the wait is bounded for `liftParkBound`'s reason one level out — *a wait
// that cannot be satisfied must end in a verdict* — and reaching the bound is reported as a **named
// outcome** rather than silently tearing down or hanging. A `var` so a witness can shorten it; a test
// that waited the real bound to see the bound is a test nobody runs.
var componentCloseBound = 5 * time.Second

// LoadComponent loads and instantiates a component, returning a handle whose exports can be called.
//
// It mirrors [Instantiate]/[Instance], which is what ADR 0085 commits to in those words. Streams and
// arguments are the zero configuration — stdout and stderr discarded, stdin empty.
//
// **For configured streams or argv, use [ComponentConfig.LoadComponent]**, the capability-carrying
// twin. It takes the same defaults this method does, so moving to it changes nothing until a field is
// set. (This paragraph said the twin was "a further exported name and this release ships only what
// was stamped" until that twin landed.) [ComponentConfig.Run] remains the path for running
// `wasi:cli/run` rather than calling exports.
//
// A component using a proposal whose gate is off is refused as [ErrGated]; one whose form this engine
// does not model, as [ErrUnsupported]. Both are refused **at load**, which is the earliest point either
// can be known.
func LoadComponent(wasm []byte) (*Component, error) {
	if !componentsEnabled() {
		return nil, fmt.Errorf("%w: gate:components is off in this build (%s=0); unset it to load a "+
			"component (the gate is on by default as of the 2026-09-11 flip)",
			ErrGated, componentsGateEnv)
	}
	h := component.NewHost(io.Discard, io.Discard, nil)
	in, err := component.InstantiateWithHost(wasm, h)
	if err != nil {
		// The same classification [ComponentConfig.Run] performs, for the same reason: `gate:async` is a
		// distinct gate from `gate:components` and a component refused by it is **well-formed**, so it
		// crosses as ErrGated rather than as a malformedness this boundary would be manufacturing
		// (grave #301).
		if errors.Is(err, component.ErrAsyncGated) {
			return nil, fmt.Errorf("%w: %w", ErrGated, err)
		}
		if errors.Is(err, component.ErrUnsupportedForm) {
			return nil, fmt.Errorf("%w: %w", ErrUnsupported, err)
		}
		return nil, err
	}
	return &Component{in: in}, nil
}

// Exports returns the names of this component's callable exports, sorted, in exactly the form
// [Component.Call] accepts.
//
// # Why this exists
//
// Without it an embedder has to know the names in advance, and the export grammar makes that worse
// rather than better: a name is either bare or `interface#function`, and nothing in the artifact tells
// a caller which form a given component wants. [Instance] has had this since it shipped; a component
// is the surface where guessing is harder.
//
// # What is in the list, and what is not
//
//	Call(ctx, name, …)   // every name returned here resolves
//
// A top-level exported function appears by its bare name; a function inside an exported interface
// appears as `interface#function`. **An exported interface's own name does not appear**, because
// naming one is refused — it is an instance, not a function — and a list of names must not contain a
// string that cannot be called. `TestExportsNamesEveryCallableAndNothingElse` holds that by **calling
// every name this returns** rather than by comparing two lists, which is the only way the list and
// the resolver cannot drift apart.
//
// Sorted, because a map's iteration order is not a fact about the component and an embedder printing
// these for a human should not see them shuffle. A fresh slice each call, so writing to the result
// cannot reach the engine's own view.
//
// # Scope
//
// Names only. An export's **type** is a further method when something needs it, and adding one does
// not change this signature — the A-then-D shape ADR 0085 uses for the value constructors: the total
// form first, never instead of.
func (c *Component) Exports() []string {
	return c.in.ValueExportNames()
}

// Call invokes an exported function with component values and returns its results.
//
// # Naming
//
// A **bare** name addresses a top-level exported function: `Call(ctx, "run", …)`. A function inside an
// exported interface is addressed `interface#function`: `Call(ctx, "wasi:cli/run@0.3.0#run", …)`. Naming
// an interface without a function refuses and says which form to use.
//
// **A bare name is never resolved by searching the interfaces**, and that is a decision rather than an
// omission: a search picks one silently when two interfaces export the same function name, and an
// embedder cannot tell which it got. The grammar is the same one the import side uses, where a
// world-level function import is registered under its own name.
//
// # Cancellation
//
// Cancelling ctx cancels the running task: the engine delivers the component model's cancelled event, the
// guest runs its own cancellation path, and the call returns [ErrCancelled] — from a task that **ended**,
// not from an abandoned wait. A call that completes before the cancellation takes effect returns its
// result normally.
//
// # Value scope
//
// `u32`, `string`, `list` and `record` cross the boundary, constructed with [ComponentU32],
// [ComponentString], [ComponentList] and [ComponentRecord]. A `tuple` arrives as a record, because
// that is what the Canonical ABI despecializes it to.
//
// An argument or result of any **other** kind is refused **by name** as [ErrUnsupported], naming the
// kind. `variant`, `flags` and resource handles do not cross, and `option`, `enum` and `result`
// despecialize to `variant`, so they do not either — each arrives when a guest needs one (ADR 0085's
// guest-driven rule). Those are listed in `CHANGELOG.md`'s known limits for the release that shipped
// this surface.
//
// (This paragraph read *"This release carries `u32` only"* until the slice that added the two methods
// above. It was true when ADR 0096 stamped it — point 5, *"first merge carries `u32` only"* — and ADR
// 0097 superseded that point without this godoc being revisited. A doc comment on a public method is
// where a stranger reads what the method accepts, so a stale one there is worse than a stale note in
// a changelog.)
func (c *Component) Call(ctx context.Context, name string, args ...ComponentValue) ([]ComponentValue, error) {
	if ctx == nil {
		// A nil context is a programming error, and saying so beats substituting Background: an embedder
		// who passed nil believes they can cancel this call, and silently giving them a call that cannot
		// be cancelled is the quietly-wrong outcome.
		return nil, fmt.Errorf("%w: Call needs a non-nil context; use context.Background() for a call "+
			"that is not cancellable", ErrUnsupported)
	}
	// The closed check and the in-flight increment happen under one acquisition (ADR 0096's ruling):
	// separately, a call that read "not closed" and incremented after Close had taken its count would be
	// a call Close never waits for — torn down mid-flight with its caller told nothing useful.
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: %q was not called", ErrComponentClosed, name)
	}
	c.active++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.active--
		c.mu.Unlock()
	}()

	in := make([]canon.Value, 0, len(args))
	for i, a := range args {
		cv, err := a.toCanon()
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", i, err)
		}
		in = append(in, cv)
	}

	res, err := c.in.CallValuesCtx(ctx, name, in...)
	if err != nil {
		// The engine's cancellation sentinel becomes the public one. Wrapped rather than replaced so the
		// engine's own message — which names the park or the entry wait it ended at — survives for a
		// reader, while `errors.Is(err, ErrCancelled)` is what an embedder matches on.
		if errors.Is(err, component.ErrCancelled) {
			return nil, fmt.Errorf("%w: %w", ErrCancelled, err)
		}
		if errors.Is(err, component.ErrUnsupportedForm) {
			return nil, fmt.Errorf("%w: %w", ErrUnsupported, err)
		}
		return nil, err
	}

	out := make([]ComponentValue, 0, len(res))
	for i, r := range res {
		pv, cerr := fromCanon(r)
		if cerr != nil {
			return nil, fmt.Errorf("result %d: %w", i, cerr)
		}
		out = append(out, pv)
	}
	return out, nil
}

// Close cancels every call still running, waits a bounded time for them to finish cancelling, and
// releases the component's engine threads and host state.
//
// Approved on the #858 review (ADR 0096's ruling), with that behaviour stated rather than left to the
// implementation:
//
//   - **Cancel, then tear down.** In-flight callers get [ErrCancelled] from tasks that *actually ended* —
//     the guest runs its own cancellation path, so its destructors fire. Tearing down first would end the
//     same tasks without that, which is how a cancellation and an abandonment become indistinguishable.
//   - **The wait is bounded**, and reaching the bound returns [ErrCloseIncomplete] rather than hanging or
//     tearing down silently. A guest can ignore a cancellation; `Close` cannot be made to wait on one
//     that does.
//   - **Calls after Close return [ErrComponentClosed]**, not a refusal from deeper in the engine about an
//     instance that no longer exists.
//
// Close is **idempotent**: a second call returns nil without re-tearing down. An embedder who defers it
// and also calls it on an error path should not have to track which ran.
func (c *Component) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	// Set **before** cancelling, so no new call can join the set Close is about to wait for. The order is
	// the whole of the guard: cancelling first would leave a window in which a fresh Call starts against
	// an instance that is already being torn down.
	c.closed = true
	c.mu.Unlock()

	c.in.CancelAll()

	// A bounded wait on a real condition, polled rather than signalled.
	//
	// **No goroutine**, deliberately: the obvious shape is `go func() { wg.Wait(); close(done) }()` with
	// a `select` on a timer, and that would be a new goroutine site — which the goroutine census
	// (`TestEveryEngineGoroutineIsAtASiteADecisionAuthorises`) admits only behind its own decision doc,
	// and which *leaks* on the bound path, because the waiter stays blocked on calls that never finish.
	// A poll on a teardown path costs a millisecond of granularity and nothing else.
	deadline := time.Now().Add(componentCloseBound)
	incomplete := false
	for {
		c.mu.Lock()
		remaining := c.active
		c.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			incomplete = true
			break
		}
		time.Sleep(time.Millisecond)
	}

	// Teardown runs on **both** paths. A bound reached is not a reason to leak the instance — it is a
	// reason to say so, which the returned error does.
	c.in.Close()
	if incomplete {
		return fmt.Errorf("%w: gave up after %s; the guest did not finish its cancellation",
			ErrCloseIncomplete, componentCloseBound)
	}
	return nil
}
