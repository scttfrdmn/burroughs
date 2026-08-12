# 0026 — A tail call is a fourth control-transfer value, and the frame owner's trampoline re-enters

Date: 2026-08-12 · Status: **proposed** — no stamp exists yet, and a `Status:` is a citation to an
approval (the ruling on #142). Scott's stamp is what closes this; the interval it spends open stays
in the record.

Filed against **#237** (`type:decision`, `gate:gc`) and milestone **v0.2.0 GC gate**. Scott ruled
this into scope on #235 and scheduled it on #250: the document is scoped to **both** gates and lands
before `gate:gc`'s flip, not deferred to the tail-call milestone. One frame-reuse mechanism, two
gate consumers, decided once.

## Question

`return_call_ref` (`0x15`) is a **GC** opcode, so its five shortfalls are engine-execution failures
inside GC's own suite. They are not #9-orthogonal, ADR 0025's carve-out does not reach them (0025
carves out vectors whose *sole* blocker is the deferred validator; these are blocked by a missing
execution mechanism), and they therefore block G-1 when GC's flip comes due. Its siblings
`return_call` (`0x12`) and `return_call_indirect` (`0x13`) belong to a different gate and want the
identical mechanism. Decide what frame reuse *is* in an interpreter whose wasm frames are Go frames,
once, for all three.

Measured on #235 (engine `e90d911` × suite pin `de54fd27`) and unchanged since:
`return_call_ref.wast` is **35 pass / 5 fail** — `:195` `count 1_000_000`, `:201` `even 1_000_000`,
`:202` `even 1_000_001`, `:207` `odd 1_000_000`, `:208` `odd 999_999`. The population is "argument
exceeds `callBudget = 10000`", not any literal; `count 1000` at `:194` passes, which is what makes
the shortfall a depth cliff rather than a broken opcode.

## What the reference's shape already settles

The reference does **not** implement a tail call as a jump. It implements it as an *admin
instruction that propagates*, and reading `eval.ml` settles three questions that would otherwise
look open.

```ocaml
(* eval.ml:65-66 — the admin values *)
| Returning of value stack
| ReturningInvoke of value stack * funcinst

(* eval.ml:282-296 — the three return_call arms all reduce to the same thing *)
| ReturnCall x, vs ->
  (match (step {c with code = (vs, [Plain (Call x) @@ e.at])}).code with
   | vs', [{it = Invoke a; at}] -> vs', [ReturningInvoke (vs', a) @@ at] | _ -> assert false)

(* eval.ml:1072-1074 — where the frame actually disappears *)
| Frame (n, frame', (vs', {it = ReturningInvoke (vs0, f); at} :: es')), vs ->
  let (ts1, _ts2) = functype_of_comptype (expand_deftype (Func.type_of f)) in
  take (List.length ts1) vs0 e.at @ vs, [Invoke f @@ at]
```

1. **A `return_call` is a plain `call`'s resolution, rewrapped.** All three arms literally `step` the
   non-tail opcode and re-tag the resulting `Invoke` as `ReturningInvoke`. So resolution — index
   lookup, table bounds, null check, type match, the trap texts — is *shared verbatim* with the
   non-tail sibling and is not this decision's subject. `ReturnCallRef`'s null check
   (`eval.ml:288-289`) sits ahead of that reuse for the same reason `array.*`'s null trap outranks
   its bounds check.
2. **The frame is popped, and the callee is invoked one level up.** `Frame(…ReturningInvoke…)`
   reduces to `[Invoke f]` in the **parent's** instruction list — not inside a fresh `Frame`, and not
   in a loop belonging to the frame being replaced. The frame dies first; the callee is entered by
   whoever owned that frame. That is a trampoline, and it is the reference's own shape rather than
   an implementation trick.
3. **`take (List.length ts1) vs0 @ vs` is frame-relative, and it is the whole arithmetic.** Take the
   callee's *arguments* off the dying frame's stack `vs0`, drop everything else that frame left, and
   append onto the parent's stack `vs`. Note it takes the **parameter** count, where `Returning`'s
   sibling arm takes the **result** count — same expression, different arity, one line apart.

**Budget.** `eval.ml:1080` decrements `budget` only when stepping *into* a `Frame`, and `:1114`
checks it at `Invoke`. A `ReturningInvoke` never steps into a frame — it removes one — so a tail
call consumes no budget and unbounded tail recursion runs forever under a fixed one. This answers
#237's open question about `callBudget` **without changing it**: the budget counts frames, a tail
call creates none, and `call.wast:337`'s non-tail `runaway` still exhausts.

## The engine fact that makes this a decision rather than a transcription

`call.go`'s own doc comment states the model: `run` calls `call` calls `run`, so **a wasm frame is a
Go frame**. A tail call therefore cannot be a `jmp`, and reusing a frame means not growing the Go
stack — which means the callee must be entered by something that is *already* on the Go stack at the
right depth, not by a nested `invoke`.

**And the frame-relative base the reference's arithmetic needs does not exist in this engine.** This
is not a forecast; it is measured, and it is already a live defect independent of tail calls:
`returnFrom` truncates the **shared** value stack to the result arity, which destroys the caller's
pending operands (**#251**, filed from this ADR's own research, with the two-pending-operand row
reporting an impossible `left -1 numeric`). The base *exists* in `invoke`'s locals —
`numBase, refBase := len(st.num), len(st.refs)` — and is not threaded into
`runFrame(fn, locals, st, results, refResults, depth)`, so the dispatch loop that executes `return`,
`br`-to-outermost and both `return_call_*` arms cannot see the height it must truncate to.

So **#251 is this ADR's prerequisite, not its companion**: the same missing concept, one
constructor apart in `eval.ml` (`Returning` at `:1069`, `ReturningInvoke` at `:1072`). Fixing it
under tail calls' requirements alone would be deciding it in the load-bearing spot for the wrong
consumer (0006); fixing it as the grave it is leaves this ADR one mechanism to add.

## Decision

**A tail call is a fourth control-transfer value, propagated as a Go error out of the dying frame's
dispatch loop, and re-entered by a single trampoline owned by whoever entered that frame.**

```go
// tailCall is a resolved callee waiting for the frame it replaces to be gone. The fourth
// control-transfer outcome, joining ErrNotValidated's layering debt, *Trap, and 0022's *thrown —
// a taxonomy this package already has three members of, rather than a fifth channel.
type tailCall struct {
	inst   *Instance      // the callee's owner: a tail call may cross instances
	fn     *binary.Func
	ft     *binary.FuncType
	locals *frame         // arguments already popped and placed
}

func (t *tailCall) Error() string { ... } // rendered only if it escapes the trampoline, which is
                                          // a bug and should read as one
```

Four properties are load-bearing.

1. **Resolution is the non-tail sibling's, unchanged** — `eval.ml:282-305`'s own `step`-the-plain-
   opcode shape. `opReturnCallRef` reuses `callRef`'s resolution (including the null trap ahead of
   it), `opReturnCallIndirect` reuses `callIndirect`'s (including the bounds trap and the
   type-mismatch text), and `return_call` reuses `call`'s. What the tail arm does *differently*
   begins after the callee is known.
2. **The arm pops the arguments, truncates the frame's stack to its base, and returns the
   sentinel.** `take (List.length ts1) vs0 @ vs` in a shared-stack engine is exactly "pop the
   params into the new `*frame`, then truncate to `base`" — which is why #251's base is a
   prerequisite and why the parameter-popping loop must be **the one `invoke` already owns**
   (`invoke`'s own rule: two callers, one place that knows how a frame is built; the v128 two-slot
   case is grave #243's, and a second copy of that loop is a third site to drift).
3. **The trampoline lives in exactly one function, and both frame entry points call it.** A tail
   call in a function entered from the boundary (`Invoke` → `run`) has no `invoke` above it, so a
   loop written only inside `invoke` would let the sentinel escape at depth 0 — the shape of grave
   #105 (a mechanism re-derived next door instead of shared). One `enterFrame`-style helper holds
   the loop; `invoke` and the boundary both call it, and each iteration re-enters `runFrame` at the
   **same** Go depth, the same `depth`, the same `base`, and the same declared result arities.
4. **The declared results stay the *original* frame's, because the spec requires the types to
   match.** A tail callee's result type is the caller's, so nothing is re-derived per iteration and
   the post-call arity check still measures against the base it was taken at. With #9 absent, a
   mismatch is `ErrNotValidated`'s territory exactly as every other arity question here is.

**`callBudget` survives unchanged, and the trampoline is why.** The loop re-enters `runFrame` with
`depth` unincremented, so the guard in `call`/`callRef`/`callIndirect` — reached through the shared
resolution — never trips on a tail chain however long, while a non-tail `runaway` still exhausts at
10000. That is the reference's counter semantics reproduced rather than a coincidence
(`eval.ml:1080` versus `:1114`).

**And the discarded frame's handlers are discarded with it, for free.** The sentinel returns out of
`runFrame` *before* the callee runs, so the dying frame's `ctrl` — and every `try_table` in it — is
gone by the time the callee can throw. An exception the tail callee raises therefore propagates to
the *caller's* `ctrl`, which is what `try_table.wast`'s `return-call-in-try-catch` and
`return-call-indirect-in-try-catch` assert (`assert_exception`, uncaught). The existing arms' own
comments already reasoned this; the mechanism now makes it structural instead of incidental.

## Options considered

**A — loop back inside `runFrame`: the arm rewrites `body`, `locals`, `results`, `pc` and `ctrl`, and
`continue`s the dispatch loop (rejected).** The cheapest at run time and one Go frame shallower per
chain — which buys nothing asymptotic, since both options are O(1) in Go stack. It was rejected on
where it puts the *frame-building* code: the arm must reproduce `invoke`'s prologue inside the
dispatch loop (the `maxFrameLocals` ceiling, both arity checks, the reverse parameter pop with its
v128 two-slot case) or call out to a helper and then hand the results back into loop-local
variables, and it must rebind the receiver `in` for a cross-instance callee. That is the one place
this engine has already paid twice for duplicating a loop (graves #243 and #105), and it is also
precisely the code an explicit frame stack would later have to unpick — see the thesis clause below.

**B — a fourth control-transfer value plus a trampoline at the frame owner (chosen).** See Decision.
It is the reference's own reduction shape (2 above), and it is **0022's stamped precedent applied to
a second kind of non-local transfer**: 0022 rejected "scan the handler stack immediately, never
leave this `runFrame`" and chose a Go error checked at every frame boundary, because that is what
actually crosses the Go-call-is-a-wasm-frame line. A tail call crosses the same line for the same
reason. The cost — one failed type assertion on a path that already performs one (`raiseOrCatch`'s
`errors.As` for `*thrown`) — is on the error return only, never on an ordinary instruction, and §1
disclaims peak-throughput parity in any case.

**C — the explicit frame stack: `[]callFrame` in one dispatch loop, no Go recursion per wasm frame
(rejected for v0, and deliberately not foreclosed).** This is the endgame `call.go`'s comment
already defers and §7 needs: **S-3** requires every continuation stack to be enumerable and walkable
at a §3 stop so the guest collector can scan it, and a continuation cannot be captured out of the Go
stack. Rejected here because the consumer that forces it is v2's stack switching, not tail calls —
taking it now would be design in the load-bearing spot on the wrong requirement (0006), and it is
much the largest change on offer. What this ADR owes it is not to make it harder, which is option B's
second advantage: a trampoline whose body is "enter one frame, maybe get told to enter another" is
the loop an explicit frame stack generalizes (one iteration per frame becomes one entry per frame),
where option A's in-loop rewriting is state the conversion would have to unpick.

## Direction

**§7 S-3 is the clause this serves, and §1 is the clause that settles the cost argument.** The
thesis question an option must answer is which choice leaves the Go-shaped stack story in §7's
reach — not which is fastest per tail call and not which is most general. Option B keeps frame
construction in one place and frame *entry* in one loop, so the v2 conversion that S-3 forces is a
change of what the loop iterates over rather than a rewrite of the dispatch core. Option A trades
that for a saving §1 explicitly declines to buy (`Non-goal: peak throughput parity`), and option C
buys S-3 early at the price of taking v2's decision inside a v0 gate campaign.

## What this does not decide

- **#251's fix**, which is a grave with its own definition of done and its own controls. This ADR
  *depends* on the base it introduces and states the dependency; it does not design the repair.
- **Whether `return_call`/`return_call_indirect` flip with `gate:tailCall` in the same PR that lands
  the mechanism.** The mechanism is shared by ruling; the *gate* is G-1's own question, answered by
  that proposal's suite going green, and scheduling it is Scott's.
- **The exact spelling of the shared frame-entry helper** (`enterFrame`, a method on `*Instance`, or
  a free function taking the instance) — an implementation choice for the PR, constrained only by
  property 3: one loop, both entry points.
- **Any change to `callBudget`'s value.** #237 asked whether the constant survives; the answer above
  is that its *semantics* are unaffected, which is a different claim from re-tuning 10000, and
  nothing here re-opens that.
- **Whether the trampoline is the place a future engine-native epoch check (contract §3) lives.** It
  is a plausible site and it has no v0 consumer; named so a later reader does not read the silence
  as an oversight.

## Consequences

- **`runFrame` gains a base parameter** (from #251) and **one new sentinel type**; the dispatch loop
  gains no new state, which is the property option A was rejected for lacking.
- **Five vectors in `return_call_ref.wast` convert**, taking the file to **40 / 0** and removing
  `gate:gc`'s only non-#9 G-1 blocker in this family. The board figure gets measured on the PR's own
  tree, never quoted from here.
- **`0x12`/`0x13` inherit the mechanism rather than reimplementing it**, which is the ruling this
  document exists to record: one frame-reuse mechanism, two gate consumers.
- **Unbounded tail recursion becomes a *terminating* correctness case rather than a depth cliff**,
  so the five vectors are also the mechanism's own falsification: an implementation that nests
  reports `call stack exhausted` at 10000 and an implementation that reuses answers in constant Go
  stack. The control must additionally cover the two the corpus does not distinguish — a tail call
  crossing instances, and a tail callee's exception escaping a `try_table` the tail call sat inside.
- **`assert_exhaustion`'s existing vectors must stay green**, which is the negative half: a
  mechanism that accidentally stopped counting non-tail frames would buy the five and lose
  `call.wast:337`. Both directions get asserted, per the bidirectional-control rule.
