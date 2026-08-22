# 0044 — The local-initialization rule is a per-frame undo list, and the block wall is performed rather than inherited

Date: 2026-08-22 · Status: **accepted** — approved as specified, mechanism included, by Scott on the
#486 review, relayed to
[a durable comment](https://github.com/scttfrdmn/burroughs/issues/452#issuecomment-5381724774).

Filed against **#452**. Implements the function-references local-initialization rule in
`internal/validate`.

## Context

`valid.ml` gives every local an init state. `check_local` starts a local at `Set` when its type is
defaultable and at `Unset` when it is not (`valid.ml:1010-1011`), parameters arrive `Set` by how
`check_func_body` assembles the context (`:1021-1024`), and `local.get` refuses an `Unset` one:
`require (init = Set) x.at "uninitialized local"` (`:589-590`).

**The rule's whole population is the function-references non-nullable reference local.** Every
numeric type is defaultable and every nullable reference type defaults to null, so nothing in the MVP
can be `Unset`. That is why the rule could be absent for as long as it was, and why its absence was an
*admission* rather than a decline: five corpus vectors assert `uninitialized local`, and four of them
were modules this package reported **valid**.

**What makes the mechanism a decision is where the reference keeps the state.** `check_instr` returns
the locals an instruction initializes as a *second component* beside its type — `LocalSet` and
`LocalTee` each return `[x]` (`valid.ml:593-599`) — and `check_instrs` folds it into the context for
the rest of the sequence (`:957-964`). The block arms then call `check_block` (`:966-968`), which walks
the body with the **enclosing** context and throws the body's list away, propagating instead the `xs`
of the block's declared instruction type, which is empty for anything the text format can spell.

So in the reference the block discipline is the *absence* of a propagation. It is free there and it is
not free here: this package walks one mutable `validator` over a flat instruction sequence, so "the
enclosing context is unchanged after the block" is not a property of how the walk is written — it has
to be **performed**.

**And the discipline is not a dataflow join.** `local_init.wast:52` sets the same local in *both* arms
of an `if` and is still invalid. An implementation that merges the arms' initializations at the `end`
is a natural reading of "the local is set on every path", passes `local_init.wast:39` (which sets in
the then-arm only), and admits `:52`. The reference has no join because the body's list never leaves
the body.

## Decision

**A per-frame undo list of the locals that frame initialized, unwound at both frame exits.**

- `validator.localInit []bool`, parallel to `locals`, seeded by `localInitStates` — `i < numParams ||
  defaultable(t)`.
- `frame.initedHere []uint32`, recording **only the indices that were `Unset` when the frame set
  them**.
- `initLocal` sets `localInit[idx]` and files the undo; `undoFrameInits` returns exactly those indices
  to `Unset` and is called from `endBlock` and from `elseArm`.

**Recording only the previously-`Unset` indices is the load-bearing detail**, not an optimization: an
undo list that recorded every `local.set` would un-initialize a local an *enclosing* scope had already
set. The reference gets that for free from `init_locals` being idempotent over a context the inner walk
never returns.

### The two alternatives, and why they lose

- **A snapshot of the whole init vector at `pushFrame`.** Correct and simpler to argue about, and it
  costs O(locals) on every block of every function — a per-block allocation in the hot path of the
  validator, to represent a set that is *empty* for every block containing no `local.set`. The undo
  list is nil for those, which is nearly all of them.
- **A dataflow join at the `end`.** Rejected on the authority: it admits `local_init.wast:52`. Named
  here because it is the reading a re-implementer arrives at from the rule's English statement rather
  than from `check_block`, and because the corpus row that catches it is one vector, so a slice that
  does not know to look for it can pass on `:39` alone.

### `initLocal` is deliberately not conditioned on `unreachable`

`check_instrs` folds `init_locals` forward whether or not the stack is at bottom — `valid.ml:964` sits
outside every bottom test — so `local.set` after a `br` initializes in the reference too. **No corpus
vector distinguishes the two readings**: the shape wants a `(block (br 0) (local.set $x))
(local.get $x)` that nothing in the suite writes. This is a mirror of the authority rather than a
measured choice, and it is stated in the code because the opposite reading is the one a reader would
assume.

## Pre-registration, and how it scored

Registered before the implementation, on the #486 review's terms; every term is met.

| term | forecast | measured |
| --- | --- | --- |
| default lane | **unmoved in all five columns** — the population is non-defaultable locals, which the default lane cannot spell | `60957 pass, 0 fail, 0 unsupported, 4187 gated, 0 unimplemented`, unmoved |
| all-on lane | `65102 pass, 7 fail` → `65107 pass, 2 fail` | exactly that |
| `local_init.wast` | `6/10` → `10/10` | `10/10` |
| `func.wast` | `174/175` → `175/175` | `175/175` |
| over-refusal | the three `array_*` files that declare non-defaultable locals stay at `35/35`, `28/28`, `24/24` | unchanged |
| the 2 survivors | #471's, not this rule's | #471's |

**Default-lane immobility is read as a leak detector, not as a null result.** A rule whose subject
cannot exist in the default lane must not move it; a move there would mean the refusal had escaped its
population, which is the failure mode a new refusal has. Registering the zero *as the detector* is what
makes it evidence rather than an absence of evidence.

## Consequences

- **A false premise in `internal/interp` becomes true, and its record is dated.** `value.go`'s grave
  #246 comment asserted that a non-nullable reference local "is non-defaultable and validation rejects
  the function, so every reference local that can exist defaults to null" — a premise about a check
  nobody had implemented. It now carries the date from which it holds, states that it was false for an
  interval, and bounds what that cost: `local.get` of such a local trapped as a null dereference, so
  the reachable failure was a *spurious trap* in a module the spec says is invalid, never a wrong value
  flowing on.
- **`localInitStates` is a second consumer of `defaultable`**, which existed for `struct.new_default`
  and `array.new_default`. One authority for defaultability, three callers.
- **The rendered message is not yet spec-phrase-first, and that is #455's.** The sentinel is
  `errors.New("uninitialized local")` with detail after the phrase per decision 0003, but `instrs`
  wraps every instruction error as `instr %d (%s): %w` (`internal/validate/instr.go:51`). The wrapper
  is one of the three rendering sites #455's option 4 moves; this sentinel is already in the form that
  will satisfy the term when it does.
- **The `unreachable` reading is an open question with no oracle.** If the suite ever grows a vector of
  the shape above, one of the two readings becomes wrong and this rule's code is where to look. Not
  filed as an issue, because there is nothing to do until the vector exists — it is recorded at the
  site instead, which is where a reader meets it.
