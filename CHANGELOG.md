# Changelog

All notable changes to Burroughs are documented here.

The format is [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

**Two versions, deliberately independent** (decision 0004, resolving contract
§10.7): the engine's SemVer governs code compatibility, while the host
contract versions on its own schedule and governs semantic promises. Every
release below states the contract version it implements.

Minor versions map to milestones, so the number is a conformance statement:
`v0.1.0` is the MVP core suite green, one minor per proposal gate flipped
after that, and `v1.0.0` is reserved for the v1 threads-and-safepoints
milestone landing with the §4 litmus battery passing on both a TSO and a
weakly-ordered platform.

## [Unreleased]
*Implements contract v0.1.*

### Added

- **The text encoder resolves a symbolic field name on `struct.get`/`struct.set`** (#188), the
  remainder #183's own PR named and declined to fold in: `(struct.get $vec $y (local.get $v))`,
  where `$y` names a field by identifier rather than by number. `structtype`
  (`internal/text/types.go`) now returns the name-to-index binding it always built locally to
  enforce the duplicate-field check, and `compType` (`internal/text/typetable.go`) grows a
  `fieldNames map[string]uint32` field carrying it forward from parse time — a field *name* binds
  to a position and never to a type index, so nothing about it needs the deferred phase's
  forward-reference machinery, unlike `fields`/`ft`. `idxPairRetained`
  (`internal/text/code.go`) routes `STRUCT_GET`/`STRUCT_SET` through a new
  `structFieldPairRetained` when the second category is `catFieldOfType`: the first index
  resolves to a concrete type index via `resolveTypeIdx`, and `resolveFieldIdx` looks the field
  name up against that specific type's `fieldNames`, encoding the resulting position. A
  numeric-first-index-naming-a-forward-referenced-type sub-case is refused rather than resolved
  (measured: no vector in the 254-file corpus needs it — every `struct.get`/`struct.set` names a
  type already fully defined earlier in its module). Verified against the merged decoder: two new
  `encodableModules` round-trip rows (`internal/text/encode_test.go`) use a three-field struct so
  a wrong resolution is distinguishable from a correct one, falsified by an off-by-one mutation in
  `resolveFieldIdx` and confirmed to fail on exactly those two rows before being reverted. Board:
  `struct.wast` alone pass=7 fail=6→0 unsupported=5 gated=12→18 (all six converted vectors land in
  `gated`, not `pass` — GC is off by default and `internal/interp` has no arms for the `0xfb`
  prefix yet, so the six now-decodable `assert_return`s are honestly declined by the gate rather
  than executed); default lane fail 436→430 (-6), gated 3257→3263 (+6), unsupported unmoved at
  27099; all-gates-on lane stays `gated 0` (`TestAllGatesOnLeavesNothingGated`), the six landing
  in interpreter-arm fail buckets there instead.

- **The text encoder retains `struct.get`/`struct.set`/`array.copy`/`array.new_data`/
  `array.new_elem`/`array.init_data`/`array.init_elem`/`array.new_fixed`'s immediates** (#8,
  #183's two-blocker chain). `internal/text/instr.go`'s `immIdxIdx` and `immIdxNat32` cases
  stop discarding via `p.idx()`/`p.nat32()`: the seven `immIdxIdx` mnemonics route through a
  new `idxPairRetained`, which resolves each index through the existing `pairCategories`/
  `retainIdxIn` machinery — the same two-index resolution `table.init`/`memory.init` already
  use — and `ARRAY_NEW_FIXED`'s trailing count gets a new `nat32Retained`, staging the same
  `encodeLocalIdx` bytes an index immediate would. `encodableShapes[immIdxIdx]` and
  `[immIdxNat32]` flip to `true`. **No per-mnemonic split needed for `STRUCT_GET`/
  `STRUCT_SET`**, despite their second index resolving against `catFieldOfType` — a category
  with no module-level space (`idxSpaceFor` returns nil by design, since the field space is
  per-struct-type and 0021 does not retain field names): `retainIdxIn`'s numeric fast path
  encodes a **numeric** field index without ever consulting that space, and every failing
  `struct.wast` vector uses a numeric field index, so a uniform reader suffices and only a
  **symbolic** field name (`struct.get $s $fieldname`) reaches the refusal, unchanged from
  before. Verified against the merged decoder (round trips for `array.copy`, `array.new_data`,
  `array.new_elem`, `array.new_fixed`, and both `struct.get`/`struct.set` with numeric field
  indices, asserting `Instr.Op`/`Prefix`/`Imm0`/`Imm1`) and added as seven new
  `encodableModules` rows in `internal/text/encode_test.go`. Board: default lane fail 606 →
  436 (-170, exactly the vectors `TestGatedVectors`' new allowlist entries account for — every
  one re-keys to an honest GC decline, since the default lane runs with GC off), gated 3087 →
  3257 (+170); all-gates-on pass 36878 → 36879 (+1), fail 1017 → 1016 (-1), gated stays 0 — the
  small all-gates-on movement is the co-blocking finding stated plainly: retention answers the
  *encoder's* refusal, and `internal/interp` has no arms yet for the `0xfb`-prefixed opcodes
  these vectors need to actually execute, so most of the 170 re-key to interpreter buckets
  (`no arm for opcode fb NN`, element-expression evaluation) rather than draining to pass.
  `unsupported` unmoved at 27099 (default lane). Not in scope, and not converted by this PR:
  field-name resolution for a symbolic `struct.get`/`struct.set` index. **This is a real,
  named remainder, not a declined-because-unneeded gap** — six `struct.wast` vectors
  (`get_0_y`... — `assert_return` commands expecting real execution, e.g.
  `(struct.get $vec $y (local.get $v))`) are genuine corpus consumers of exactly this
  resolution and stay `fail` after this PR, correctly, with the existing "(#8)" message.
  Resolving them needs a per-struct-type field-name space this stratum does not have —
  `structtype`'s local `fields` binding (`internal/text/types.go`) is discarded when the
  function returns, and nothing threads it to encode time. Filed as its own follow-up
  (issue tracked separately) rather than folded into this PR, since the five easy `ARRAY_*`
  mnemonics and `ARRAY_NEW_FIXED` needed no new infrastructure while this genuinely does.

- **The text encoder writes a struct's or array's field list**: decision
  [0021](docs/decisions/0021-a-field-type-is-a-value-type-or-a-packed-width-plus-mutability.md)'s
  encoder-side implementation (option C), completing the pair the decoder-side PR (#186)
  opened. `internal/text/types.go`'s `storagetype`/`fieldtype`/`fieldtypeList`/`structtype`/
  `arraytype`/`comptype` stop discarding what they parse: `storageType`/`fieldType` are new
  unresolved shapes mirroring `binary.StorageType`/`FieldType`, and `compType` grows a `kind
  compKind` (three states — `compFunc`/`compStruct`/`compArray`, replacing the old `isFunc
  bool`, mirroring `binary.CompKind`) plus `fields []fieldType`. The deferred phase
  (`runDeferred`/`resolveFields`/`resolveStorage`, `internal/text/typetable.go`) resolves
  each field's storage exactly as a functype's param resolves — a `(field (ref $t))` may
  forward-reference a type defined later in the same field list, through the same
  `resolveVal`/`resolveTypeIdx` machinery — producing `resolvedComp{kind, ft, fields
  []resolvedField}`. The encoder (`internal/text/encode.go`) gains `tagStruct`/`tagArray`
  (0x5f/0x5e) beside the existing `tagFunc`, and `w.fieldType`/`w.storageType` write a
  struct's `vec(fieldtype)` or an array's bare fieldtype (no count, matching `arraytype`'s
  own arity) — a full value type through the existing `valTypeBytes`, or one of the two
  packed forms (`0x78`/`0x77`, the `-0x08`/`-0x09` fold). `encodableOrErr`'s former
  unconditional struct/array refusal now asks the same per-field `valTypeBytes` question a
  functype's param already answers; packed storage always has a byte, so the practical
  effect is that a struct or array type no longer refuses outright. Field *names* stay out
  of scope, unchanged from the decoder-side PR (no wire representation, 0016's `LocalGroup`
  precedent), as does the `struct.get`/`array.get`-family instruction-immediate encoding
  capacity (#183's separately-tracked two-blocker chain — `struct.wast`'s 18 fails are all
  behind that blocker and this PR does not touch them) and any `internal/interp` consumer
  (0020's later PR). No gating change. Board: default lane fail 734 → 606 (-128: the "fields
  are not retained" bucket empties completely, 126 vectors — 117 + 6 + 3 by the split
  `no instance`/`register` messages carried, reconciling to the 110-of-126 sole-mechanism
  estimate plus the 16 partially-shadowed ones, all landing as expected), gated 2959 → 3087
  (+128, the freed vectors that need GC on to actually decode); all-gates-on fail 1024 → 1017
  (-7, `array.wast`'s residual — the other 119 of the freed 126 land as new *passes* in that
  lane, since GC is already on there), pass 36877 → 36878, gated stays 0. `unsupported`
  unmoved at 27099 (default lane) — every moved vector was already in `fail`/`gated`, not
  `unsupported`. Verified against the merged decoder directly (`TestStructAndArrayFieldsRoundTrip`):
  a struct with mixed packed/full-valtype fields and mixed mutability, and an array's single
  field, encoded then decoded through `(&binary.Decoder{Features: binary.Features{GC:
  true}}).DecodeModule`, asserting the decoded `CompType.Fields` matches what the text named.

- **`binary.CompType` retains a struct's or array's field list**: decision
  [0021](docs/decisions/0021-a-field-type-is-a-value-type-or-a-packed-width-plus-mutability.md)'s
  decoder-side implementation (option C). Two new types — `StorageType` (a full `ValType`,
  or one of the two packed widths i8/i16 the reference's `packtype` admits) and `FieldType`
  (a `StorageType` plus a mutability bit) — and `CompType` gains `Fields []FieldType`: one
  entry per declared field for a struct, in declaration order, or exactly one entry for an
  array, matching `arraytype`'s own arity. `decodeStorageType`/`decodeFieldType`
  (`internal/binary/sections.go`) stop discarding their reads, writing through new
  `Decoder.storageType`/`Decoder.fieldType` out-parameters — the same convention
  `decodeValType` already uses, forced by the identical shape: both readers are passed to
  `either`/`decodeVec` as `func(*reader) error`, which cannot return a value. Field *names*
  are out of scope (no wire representation, per 0016's `LocalGroup` precedent), as is the
  `struct.get`/`array.get`-family instruction-immediate encoding capacity (a separate
  frontier, #172 item 5) and any `internal/interp` consumer (0020's `gcObj`/`gcField`, a
  later PR). No gating change — this is retention, not acceptance, per 0018's precedent for
  the same class of decision. Board: unmoved in both lanes (default 34227/734/27099/2959,
  all-gates-on 36877/1024/0 gated) — decoder-only retention with no producer/consumer yet,
  the identical shape 0018's decoder-side PR (#179) converted zero vectors on.

- **The text encoder writes every parameterized reference form**: decision
  [0018](docs/decisions/0018-a-wide-value-type-mirrors-the-wire-forms-heaptype-not-the-text-sides-resolvedval.md)'s
  encoder-side implementation. `valTypeBytes` (`internal/text/encode.go`, replacing the
  byte-only `valTypeByte`) now emits the twelve nullable-abstract single-byte abbreviations
  (unchanged from before), plus — new — the general parameterized production for every other
  form: a non-null abstract heaptype (`(ref i31)`, `(ref func)`) or the indexed form at either
  nullability (`(ref $t)`/`(ref null $t)`), each written as `decodeRefType`'s prefix byte
  (`0x64` non-null / `0x63` nullable) followed by `heapTypeBytes`' existing, unchanged
  `heaptype` encoding. Every call site that used to write one byte (`w.valType`, a func's
  locals via `funcLocalBytes`, `select (result …)`'s `selectResultBytes`, a block's single-result
  form in `blockTypeBytes`) now writes a variable-length slice; `writeLocals`'s run-length fold
  compares by `slices.Equal` rather than `==` for the same reason. Verified against the fixed
  decoder (grave [#180](https://github.com/scttfrdmn/burroughs/issues/180)/[#181](https://github.com/scttfrdmn/burroughs/pull/181)):
  `(ref func)` round-trips to a `ValType` distinct from `binary.FuncRef`, and `funcref` round-trips
  equal to it — the assertion #181's fix made possible, since before it the two decoded identically.
  Board: the "parameterized reference encoding" bucket (489 vectors, measured with the harness)
  empties in both lanes; default lane fail 1135 → 734, all-gates-on fail 1284 → 1024, all-gates-on
  pass 36629 → 36877 (`allOnPassFloor` raised to match), `unsupported` unmoved at 27099 in both
  lanes. 401 of the 489 land as honest GC declines in the default lane (`TestGatedVectors`'
  allowlist grew accordingly, one reason per file read off the module's own header); the remainder
  reconciles into the sibling "struct or array type's fields are not retained" bucket (+115, a
  different, still-undecided frontier — decision 0019 — this PR does not touch) and a 27-vector
  drop in "assert_unlinkable expected: incompatible import type" (those vectors now decline one
  command earlier, in the same script).

- **`binary.ValType` widens from a byte to a three-field struct (`kind`, `null`, `idx`)**,
  decision [0018](docs/decisions/0018-a-wide-value-type-mirrors-the-wire-forms-heaptype-not-the-text-sides-resolvedval.md)'s
  option C, implemented decoder-side. `decodeRefType`/`decodeHeapType` (`internal/binary/sections.go`)
  now write the resolved kind/nullability/index for every GC-gated `reftype`/`heaptype` form
  instead of the `NoValType` sentinel — the ten abstract heaptypes and the two parameterized
  indexed forms (`(ref $t)`/`(ref null $t)`) are representable for the first time. The five
  numeric/vector constants and the two Wasm 2.0 reference constants (`FuncRef`, `ExternRef`) keep
  their exact pre-widening byte behavior, so every existing `t == I32`-style comparison is
  unaffected. `Instr`'s blocktype packing (`BlockType`, `Imm0`/`Imm1`) is redesigned to carry a
  resolved index for an indexed-form single-result block, verified against every
  `immBlockType`-carrying opcode rather than assumed. Encoder-side retention (`internal/text`
  building this type instead of returning `(0, false)`) is deferred to the next PR in the GC-gate
  implementation ladder; **board unmoved** (34227/1135/27099/2558 default, 36629/1284/0
  all-gates-on, byte-identical before and after), exactly as the co-blocking probe predicted for a
  representation change with no encoder consumer yet — retention alone does not make the encoder
  emit the new forms, the same zero-conversion shape [#161](https://github.com/scttfrdmn/burroughs/pull/161)'s
  `ref.null` PR measured.

  **`ValType.null` retains the wire's *spelled* null bit, not semantic nullability, and that is
  deliberate rather than a gap**: `funcref`/`externref` (Wasm 2.0's abbreviations) spell non-null
  for backward compatibility with every existing `t == FuncRef` comparison, while the reference's
  own model treats both as nullable — the same wire-spelling-over-derived-reading law
  `LocalGroup` and the delimiter productions already follow. `Nullable()` is the new accessor for
  semantic nullability (the fact a subtype check needs — non-null under nullable, never the
  reverse), diverging from `Null()` for exactly those two forms and agreeing everywhere else;
  pinned by `TestNullableDivergesFromNullForWasm2Forms`, falsified by reverting it to `return
  t.null` and confirming only the FuncRef/ExternRef assertions fail.

- **Six table and reference opcodes: `table.get`, `table.set`, `table.size`, `table.grow`,
  `table.fill`, `ref.null`, `ref.is_null`, `ref.func`** ([#7](https://github.com/scttfrdmn/burroughs/issues/7)).
  `opTableFC`'s 18 sub-opcodes are now all answered (`table.grow`/`table.size`/`table.fill` were
  the last three), and the main switch's three reference-family gaps are closed. `table.get`
  reports `"out of bounds table access"` — a distinct wrapper from `call_indirect`'s
  `"undefined element N"` over the same underlying bounds check, the reference's own choice
  (`eval.ml`'s `Table.load` wrapped by `table_error` for `TableGet`, by `any_ref`'s own text only
  for `func_ref`'s dispatch) rather than a shared string. `table.grow` is total — failure reports
  `-1` in the result rather than trapping, `memory.grow`'s contract exactly — and grows the
  table's own retained `Limits.Min`, `memory.grow`'s #164 fix ported to the sibling type before a
  vector could expose the same staleness twice.

  **A second defect found in the making, upstream of any table opcode**: `Instance.invoke`'s
  post-call arity check counted only the numeric stack's delta against the callee's declared
  result count, so *any* function returning a ref-typed value reported "declares 1 results and
  left 0 values on the stack" — right for a numeric callee, wrong unconditionally for a
  ref-returning one, since a ref result lands on the parallel reference stack the check never
  read. Unreachable before this PR: nothing produced a ref-typed function result through `call`
  or `call_indirect` until `table.get`/`ref.func` existed to be called through.
  `table_get.wast`'s `is_null-funcref` is the corpus's own specimen. Fixed by counting the
  callee's results per kind and checking each stack against its own count.

  `execFailCeiling`: 196 → 118. `TestUnhandledFCSubOpcodeStaysOnTheWorkList`'s subject dissolved
  rather than moved — with `opTableFC` fully drained there is no nineteenth unhandled sub-opcode
  left on the corpus to re-point the tripwire to, so it now calls `execFC` directly with a
  sub-opcode the decoder itself can never admit into an accepted module.

- **A funcref names the instance its function index belongs to, and `call_indirect` resolves
  through it instead of through the caller** ([grave #163](https://github.com/scttfrdmn/burroughs/issues/163),
  0017 Q2). `ref` was `{Null bool; Addr uint32}`, and `Addr` was read as an index into the
  *calling* instance's function space — correct for a module-local table, wrong the moment a
  table slot could hold a funcref another instance's element segment wrote into it (0017 Q1's
  registry made that possible for the first time). `ref` now carries `Inst *Instance`, set at
  every construction site to the instance whose index space `Addr` names — `segmentRefs`'
  index-form arm and `constExprRef`'s `ref.func` arm, both already `*Instance` methods — and
  `callIndirect` resolves `r.Addr` against `r.Inst` rather than against the caller.

  **The issue's own synthetic reproducer names the defect precisely**: a supplier's table slot 0
  funcrefs its own function returning 11; an importer imports that table, declares a decoy
  function 0 returning 99, and calls slot 0 — the old code returned 99 (the importer's own
  function at that index) instead of 11. `linking.wast:342-353` is the corpus's version, `$Ot`
  writing its own functions into `$Mt`'s imported table. This is accept-direction (§9 G-3) in the
  most literal sense: the wrong answers are plausible small integers, and the vectors score green
  whenever the two instances happen to agree at an index, which most cross-instance modules in
  the corpus do most of the time — found by writing the flow account and probing, not by reading
  a failure. `TestCallIndirectResolvesTheFuncrefsOriginatingInstance` and
  `TestCallIndirectAfterCrossInstanceElemWrite` pin both reproducers, each falsified by reverting
  the resolution and confirming the exact wrong numbers (99, 4) reappear.

  `execFailCeiling`: 211 → 196, all 15 departures from `assert_return value mismatch` and zero
  arrivals elsewhere (measured by joining the full pre- and post-fix bucket dumps, not by reading
  either board's total). `mismatch_test.go`'s per-row registry — pre-existing, and the reason this
  session didn't have to rediscover which rows were which — drops its 15 Q2 entries and keeps its
  7 unrelated ones (5 blocked by the `MultiMemory` gate, 2 by `#8`'s missing `(start …)` encoder
  support) exactly as they were.

- **The decoder retains a non-func import's descriptor, and the linker compares types instead of
  only kinds** ([#164](https://github.com/scttfrdmn/burroughs/issues/164)). `decodeImport` used to
  read a table's element type and limits, a memory's limits, or a global's type and mutability and
  then discard them — `binary.Import` now carries `Table`, `Memory`, `GlobalType` and
  `GlobalMutable`, and `Instance.link` compares them against what the supplier actually built:
  `sameFuncType` (already used by `call_indirect`) for a func import's own signature, `matchLimits`
  for table and memory limits (the reference's `match_limits`, min and max compared in opposite
  directions — a supplier may offer *more* room than declared, never less), and byte equality for a
  global's type and mutability.

  **42 `assert_unlinkable` vectors were accepting a module the spec refuses**, table/memory limit
  mismatches and global type/mutability mismatches scoring green because a matching *kind* was
  already enough to link — an accept-direction gap (§9 G-3) the suite's rejection-only vectors could
  not have found without the linker checking anything: `TestImportTypeMismatchIsRejectedPerKind` and
  its accept-direction counterpart `TestImportTypeMatchLinksPerKind` cover both readings per kind,
  each row falsified individually. **38 of the 42 convert to pass; 4 remain** — `type-subtyping.wast`'s
  `rec`/`sub` rows declare a func import's type as a *nominal subtype* rather than a plain signature,
  which `sameFuncType`'s structural-equality reduction cannot see (its own doc comment already
  declares the gap under GC). The other 82 of the original 124-vector bucket were never reachable by
  this fix: 35 are blocked by an EH-gated register target, 14 by a GC-gated one, 4 by a
  Memory64-gated one (same mechanism — the module a `register` names never decodes, so every import
  from it reads `unknown import` rather than `incompatible import type`), and 29 by a GC-gated
  *import descriptor* on the importer's own side. Measured by joining the pre- and post-fix bucket
  dumps rather than reading either board's total alone: 38+4+35+14+4+29 = 124, no residual.

  `table_grow.wast` moves independently by +1 net, and it is not a regression: two rows re-key from
  an opcode-missing bucket to an import-type-mismatch one (same cause, sharper instrument — the
  table never actually grows without a `table.grow` arm, so the next module's declared size
  genuinely mismatches), and a third is a new fail one command earlier in the same chain, caught
  sooner rather than differently.

  Found in the making: `memory.grow` reallocated a memory's bytes without updating its retained
  `Limits.Min`, so a grown memory re-exported for another instance to import reported its stale
  pre-growth minimum. `imports4.wast`'s own comment names the corpus vector this breaks — "imported
  memory limits should match, because external memory size is 2 now" — and `TestGrownMemoryReexportsItsCurrentSize`
  pins it directly, falsified by removing the one-line fix.

- **The text encoder writes `ref.null`'s heap type — the whole `heaptype` production, all thirteen
  forms** ([#8](https://github.com/scttfrdmn/burroughs/issues/8)). It previously answered `func` and
  `extern`, the two Wasm 2.0 forms, which made `ref.null` the one entry in `encodableShapes` that was
  only partly encodable. The twelve absolute forms are now a byte table machine-checked against
  `encode.ml`'s own arms, and the thirteenth — a **type index** — is a `typeuse s33` rather than a
  byte, the same production the blocktype writer uses and the reason `blockTypeIdxBytes` is now
  `typeuseIdxBytes` with two callers instead of a second copy of itself.

  **A heaptype is not a reftype, and the two agree on twelve of thirteen forms**, which is what makes
  calling the wrong one undetectable on a corpus: `reftype`'s abbreviation arms *are* `heaptype`'s
  singletons, so `funcref` and `func` are both `0x70`, and they diverge only on `null` and on the
  bare index. `ref.null`'s immediate has no nullability of its own — the instruction is the null — so
  it gets its own writer and its own diagnostic spelling.

  Ten of the twelve forms are GC's, so the decoder declines them with a *feature-named* error while
  the gate is off: the module is written and then honestly gated, rather than refused one layer up
  where that would spoof the decoder's configuration.

  **This converts no vectors, and the board says so — 1699 fail before and after.** The 609-vector
  bucket this was taken for was *shadowing*: `ref.null` is the first refusal a GC vector meets, so
  all 609 re-bucketed one layer up into the reftype frontier (+446) and the cast-immediate frontier
  (+163) rather than passing. The arm is right and required, and the estimate was an upper bound on
  a chain rather than a count of reachable work.

- **A symbolic type index in `ref.null` resolves in the deferred phase, so field order no longer
  matters** ([#8](https://github.com/scttfrdmn/burroughs/issues/8)).
  `(module (func (drop (ref.null $t))) (type $t (func)))` is a valid module that the encoder
  rejected with `unknown type $t` — measured, not hypothesized. The type space permits forward
  references by construction, which is why `heaptype` returns its token unresolved at all. No corpus
  vector distinguishes the two orders (a sweep of all 254 `.wast` files finds zero uses preceding
  their declaration), so this is an accept-direction defect the suite scores green by construction
  and it lands with a *derived* vector asserting both orders produce the identical image.

- **Same-script linking: `register`, the five module-naming command Kinds, and
  `InstantiateLinked` — 605 fails become 13**
  ([#157](https://github.com/scttfrdmn/burroughs/issues/157),
  [#7](https://github.com/scttfrdmn/burroughs/issues/7)). Decision 0017's first half, and it is a
  *harness* mechanism with an engine entry point rather than contract §3's host-import surface:
  the registry is a `map[string]Instance` the run loop threads through the script, so every import
  it satisfies is answered by **another module in the same file** or by `spectest`. The negative
  0017 records is what made that the right shape — re-measured on the current board, **605 of 1265
  fails** wanted an import and **zero** of them wanted a Go host function, so the §3 API the
  thesis makes this engine's most Go-shaped feature is still the one thing the corpus cannot
  score.

  Two maps, not one, because they are two namespaces: `registry` keyed by the *module name* an
  import asks for and written only by `(register …)`, `named` keyed by the script `$name` and
  written by every module command that carries one. A module may be in both, either, or neither.
  Merging them would make `(register "a" $M)` imply that `(invoke "a" …)` is a legal action, and
  that form is not in the grammar at all.

  `spectest` is **synthesized as wat and instantiated through the same door every vector's module
  takes** (0017 part 3), which is why the resolver has no builtin arm — 174 import sites resolve
  against it. Its own instantiation failure is a `panic` rather than a fail: the source is *ours*,
  so charging it to the engine's column would report a broken fixture as an engine that cannot
  link. The 14-export build is retried at 13 when the engine *declines* the `table64` export, the
  gate being read off the engine's own answer rather than guessed from the lane, and the panic
  names which of the two builds failed.

  `assert_unlinkable` scores 200 vectors and **requires the linker rather than degrading to the
  unlinked path** — the one asymmetry in the run loop, defended at its site: instantiating an
  unlinkable module *without* its imports fails for a different reason whose text could contain
  the expected string, which would award 200 passes nothing earned.

  `Instantiate` is now `InstantiateLinked` with a nil resolver rather than a second body, and
  `Imports` is a func rather than a map because resolution is the *script's* semantics, not the
  interpreter's.

- **The text encoder emits `table.init` and `memory.init`, and the interpreter executes them with
  `elem.drop` and `data.drop` — `fc 0c`, `fc 08`, `fc 0d`, `fc 09`**
  ([#8](https://github.com/scttfrdmn/burroughs/issues/8),
  [#7](https://github.com/scttfrdmn/burroughs/issues/7)). Both halves in one PR because they are
  not separable: an arm without drop state is a *different instruction*, since `run_elem`
  (`eval.ml:1264-1276`) emits `TableInit` followed by **`ElemDrop`** for every active segment, so a
  module's own segments are already empty before any exported function can reach them.
  `bulk.wast:250-270` asserts exactly that — `init_active` with length 0 succeeds, length 1 traps —
  and an engine that skipped the drop would answer by copying a reference the reference cannot see.
  The segment instances (`elemInstance`, `dataInstance`) are the reference's `Value.ref_ list ref`
  and `string ref`: one mutable cell each, `drop seg = seg := []`.

  **The two immediates are encoded in the reverse of the written order, and only for these two of
  the four mnemonics that share the code path.** `encode.ml:294` and `:411` write `idx y; idx x`
  where `TableCopy` (`:293`) and `MemoryCopy` (`:410`) write them in order, and `decode.ml:674`
  reads them back the same way — so the *segment* index is written second and emitted first. The
  sugar arm reverses again and the two cancel: `TABLE_INIT idx` is
  `table_init (0l @@ …) ($2 c elem)` (`parser.mly:589`), so its one written index is the element
  segment and it lands *first* on the wire with the defaulted table index 0 behind it. Getting
  either backwards emits a well-formed image denoting a different instruction — contract §9 G-3,
  invisible to the suite — which is why `TestInitReversedKindsMatchTheReference` reads all fourteen
  of `encode.ml`'s index-pair arms and requires the reversing set to be exactly the four the
  reference reverses, crediting `call_indirect`'s two to `callIndirectImm` by name rather than
  scoping the control to the two this path encodes.

  **`table.init`'s length is i32 whatever the table's address type is, and `table.copy`'s is not.**
  `valid.ml:641` types the arm `[numtype_of_addrtype at; I32T; I32T]` — only the destination takes
  the table's width — against `:632`, where the copy's length is a real address type. A segment is
  *indexed* rather than addressed, so its bound has no width. The first draft ran the length
  through `tableAddr` on the symmetry argument and the validator was read second;
  `table_init64.wast` is 774 vectors, so the file that would have exposed it is the one the arm
  exists for.

  Two accept-direction controls the suite cannot supply landed with it, both derived from
  `parser.mly` and both scoped to the whole production rather than to this tier:
  `TestIdxPairLookupKindsMatchTheReference` (every arm passing two lookup categories, in written
  order — a wrong *second* category resolves `table.init $t $e`'s element index in the table space)
  and `TestFieldOfTypeIsNeverAFirstCategory`, whose name `instr.go` had been citing before it
  existed ([#116](https://github.com/scttfrdmn/burroughs/issues/116)'s class).

  **A vacuity floor that could not tell its own instrument from the weaker one.** The pair
  extractor exists because the single-index control's reader — a ten-way alternation of space names
  — cannot see `struct.get`'s `$3 c (field x.it)`, so it finds 8 two-lookup arms where the
  positional reader finds 10. The floor was set at 8, which is **exactly** what the degraded reader
  yields: stubbing the regexp to the alternation pattern passed the floor and then reported drift in
  the table under test, a control's blind spot presented as the subject's defect. The floor now sits
  beside an exact check — the field expression must actually appear among the lookups — which is
  *floors bound the catastrophic case; only an exact instrument sees a small silent loss*
  ([#108](https://github.com/scttfrdmn/burroughs/issues/108)) found by the falsification exercise
  rather than by review. The prose in `idxPairLookupKinds` had said 9, a figure typed before the
  instrument that measures it.

  **Ten mutations, and two of the findings are about the mutations rather than the controls.** The
  `TABLE_INIT` deletion **passed** on its first attempt and the control was right to pass: the
  script's pattern matched `initSugarKinds`, which holds a byte-identical `"TABLE_INIT":  true,`
  line, so a row in a different map was deleted and the subject was untouched — field attribution is
  not first-match, pointed at a mutation instead of at a generator. And the field-position
  mutation's expected count was written as 49 and measured at **15**, 49 being the number of
  lookup-passing arms while only the fifteen `catType` ones pass `type_`. Both are recorded at their
  sites: a falsification that passes is either a stillborn control or a mutation that did not apply,
  and the two are indistinguishable without printing the diff.

  A `load` method on `elemInstance` was deleted as a `deadcode` classification: `eval.ml:427`
  bounds-checks the whole extent before reading, so `execTableInit` does the copy in one slice
  expression and there is no per-element read. The tell was the asymmetry rather than the finding —
  `dataInstance` has no twin and never needed one — which makes it a method written from the
  reference's shape rather than from a consumer's requirement (0006).

  Board: pass **31898 → 33356** (+1458), fail **3401 → 1699** (−1702), gated **2264 → 2508** (+244),
  and 1458 + 244 = 1702 exactly. `unsupported` **unmoved at 27501**, and structurally so: every
  vector touched was already being asked and already answering `fail`, an encoder frontier being a
  fail rather than an unsupported. Stratum: encode **2775 → 994**, exec **626 → 705**.
  Set-differenced on `(file, line)` keys, the encode drain is **1781 departures, 0 arrivals** — the
  largest this column has taken — flowing 1453 to pass, 244 to gated, 84 to exec.

  **The +244 gated is the largest batch this board has admitted and every one of them was a
  *fail*.** 151 quoted `cannot yet encode memory.init (#8)` and 93 `cannot yet encode
  table.init (#8)`: modules the encoder can now build well enough for a declined feature to be
  *reached*. Probed rather than read off filenames — 232 memory64, 12 multi-memory — because
  `memory_init0.wast` is a multi-memory file despite its name, gated by its `(i32.load8_u $mem2 …)`
  read-back carrying memarg flags bit 6 against four declared memories, not by the instruction it is
  named for. All 244 are honestly **red** in the all-on lane where they are not gated, which is the
  structural bound that keeps a deferral from becoming a disappearance: 223 pass there and 21 stay
  failed at contract §3's table-slot linking frontier.

  Exec **rose** by 79, which is reclassification and not a regression: **84 arrivals, all one
  reason** — `table slot N names function M, which is an import, and linking is not implemented
  (contract §3)`, 42 in `table_init.wast` and 42 in `table_init64.wast` — against **5 departures**
  that are exactly this PR's drop arms, and **0 same-key-new-reason**. All-on lane **33851 → 35533**
  (+1682).

- **The harness asks `(assert_trap (invoke "f" arg*) "text")` — a trapping call is now a
  scorable command** ([#157](https://github.com/scttfrdmn/burroughs/issues/157)). The classifier
  had a Kind for `assert_trap` wrapping a *module* (instantiation trapping, 0015) and none for the
  far commoner form wrapping an *action*, so **4923** commands reached the run loop as
  `unsupported` and were never asked. 4876 classify; the 47 that do not are the shapes
  `invokeAction` declines structurally — 20 naming a module with `(invoke $M …)`, 27 taking a
  reference-typed argument — and they stay in the unsupported column rather than being admitted
  as questions the harness cannot phrase. **Zero engine lines.**

  The action is read by the **same** `invokeAction` that `assert_return` and bare `invoke` use.
  Copying it would have been the re-derived-shape grave
  ([#105](https://github.com/scttfrdmn/burroughs/issues/105)) a fourth time; sharing it means the
  `$M` and NaN-class declines are one decision in one place.

  **`Engine.IsTrap` is injected for the reason `Engine.IsGated` is.** The harness must not sniff
  error text, so it cannot ask whether a message starts with `trap: ` — and it must not, because a
  *non-trap* error whose message happens to contain the expected phrase would score a pass
  invisibly on the board (contract §9 G-3, the accept direction the suite cannot falsify). The run
  loop asks the predicate **before** the substring match, which makes that impossible rather than
  unlikely. Falsified in both directions: `TestAssertTrapActionScoring`'s imposter row uses an
  error whose text is deliberately *identical* to the real trap's, so an arm that sniffed would
  pass the row for the wrong reason.

  **A no-op branch was found by its own falsification not failing.** The arm's first draft
  returned `unsupported` early for a declined `(module definition …)` form; the mutation that
  should have killed that branch left every control green, because `invokeAction` is keyed on the
  head atom and such a node falls through it anyway. Removed, with the stillbirth recorded at the
  site — *a control isn't born until it has been watched die*
  ([#108](https://github.com/scttfrdmn/burroughs/issues/108)) applies to engine branches too.

  **The recon's own census was wrong, in the direction its rules predict.** It quoted the
  population as 4903 with 27 declines; asked through `invokeAction` itself the answer is 4923 with
  47. The recon's probe required the action's element 1 to be a string — which is `invokeAction`'s
  *own* accept condition — so it was structurally unable to see the twenty `(invoke $M …)` forms
  that fail on that exact test, and both halves of its figure were short by the same twenty. Grave
  [#106](https://github.com/scttfrdmn/burroughs/issues/106) in a census: *a premise measured over
  the same sample the code reads is an echo*. The classifier count is the drain's own arithmetic
  (4876) and was never in doubt; what was wrong is the number quoted *beside* it.

- **The interpreter executes the bulk memory and table operations — `fc 0a` `memory.copy`,
  `fc 0b` `memory.fill`, `fc 0e` `table.copy`**
  ([#7](https://github.com/scttfrdmn/burroughs/issues/7)). All three are one shape — pop
  `n`/src/dst, check bounds on every region, exit on a zero length, move — which is
  `eval.ml:549`, `:567` and `:395` three times over. The reference's shared bounds predicate
  `oob i n j` lands as `outOfBounds` beside the memory primitives.

  **The bounds check precedes the zero-length exit, and that ordering is the whole subtlety.** A
  zero-length fill or copy at exactly the end of a region **succeeds**; one byte past it **traps**.
  `bulk.wast` asserts it in its own words — `:49` "Succeed when writing 0 bytes at the end of the
  region" against `:52` "Writing 0 bytes outside the memory traps" — so the natural
  `if n == 0 { return nil }` fast path at the top of each arm is exactly the early-return grave
  ([#41](https://github.com/scttfrdmn/burroughs/issues/41)): faster, plausible, and wrong on four
  vectors. `TestBulkZeroLengthChecksBoundsFirst` pins both halves, and its must-*succeed* rows are
  the half that catches a bound off by one in the other direction.

  Go's `copy` is a `memmove`, so the reference's forward/backward branch on `I64.le_u d s`
  collapses to one call — the branch is absent by construction rather than forgotten. Since a
  property nothing exercises is a claim, both directions are pinned anyway
  (`TestBulkCopyHandlesOverlapInBothDirections` and its `[]ref` twin).

  **Thirteen mutations, eleven deaths, and the two survivors are one finding.** Dropping
  `outOfBounds`'s wrap arm and dropping `tableAddr`'s `uint32` narrowing both leave the suite and
  every local row green — because `pushI32` zero-extends every i32 slot, so no i32 operand can
  express the state either guard refuses. Both arms are correct, both are unreachable today, and
  both are documented at their definition instead of guarded by a test that would have to
  fabricate a stack the decoder cannot produce
  ([#125](https://github.com/scttfrdmn/burroughs/issues/125)). A control drafted for the narrowing
  was **stillborn** and deleted; the paragraph replacing it is the finding. `table.copy`'s
  destination/source order is the third case: no local row can see it, and the board can —
  swapping `Imm0`/`Imm1` moves the exec stratum 608 → 632 on `table_copy.wast:774`, so the suite
  is left to own it.

  **The `assert_return value mismatch` bucket reached zero, and its census was re-pointed rather
  than retired.** 284 of its 308 rows became passes and 24 moved to the linking frontier. The
  file's own vacuity check instructed its reader to retire it on an empty bucket; that instruction
  was not followed, because *a tripwire whose subject dissolves is re-pointed, never closed* — the
  risk it names ("the engine returns a wrong value on a module that ran") outlived its population.
  The direction inverts: empty is now the assertion and any row is the finding, which is the
  stronger claim, since a row appearing from here on has no missing arm to hide behind. Watched
  die by mutating `execMemoryFill` to write `k+1`: 239 rows, partitioned **0 downstream / 239 not
  downstream** — the classifier's first dissent on real data, and a verdict it could not have
  delivered before the trio landed because the setups would have masked it.

  **`call_indirect`'s index narrowing routes through `tableAddr` too**, the two open-coded lines
  at its call site being the same width decision. Removing the duplicate is what a free function
  was for; deferring it would have kept a second place knowing one fact for the length of a queue.
  The retrofit is behaviour-identical on both lanes, and collapsing `tableAddr` to `return slot`
  leaves both boards green — so the lines removed from `call.go` were as unobservable as the ones
  in `bulk.go`, for the same `pushI32` reason, which is now recorded at that site.

  Board: pass **28594 → 29005** (+411), exec fail **1019 → 608**, encode unmoved at 1353, gated
  unmoved at 1721; `unsupported` **unmoved at 32377**. Set-differenced on `(file, line)`: **411
  departed, 0 arrived, 60 same-key-new-reason.** The 60 are one finding — all of them in
  `table_copy.wast` and `table_copy64.wast`, moving from a value mismatch or `uninitialized
  element` to `table slot N names function M, which is an import`: the copy now happens, relocates
  a slot holding an imported reference, and `call_indirect` meets contract §3's frontier. The arm
  did not fail them, it moved them one layer down.

  **82 vectors named the work and 411 moved** — a 5:1 multiplier, the largest this column has
  recorded, and it is a fact about the corpus rather than the engine. `memory_copy.wast` and
  `table_copy.wast` are generated, each bulk instruction sitting in its own module behind up to
  forty read-backs, and those read-backs were carried in *other* buckets. So a bucket named after
  a missing opcode understates an arm whose absence corrupts state that later vectors read: the
  mirror image of *bucket size estimates the reward, not the job*, with the same remedy — measure,
  then quote. All-on lane **29714 → 30477** (+763); the 352-vector excess over the default lane is
  itemized in `allOnPassFloor`'s comment and is entirely gate-declined files, five of them the
  memory64/table64 twins that put `tableAddr`'s and `memory.addr`'s i64 branches on a board for the
  first time.

- **The interpreter executes the eight saturating truncations, `fc 00`–`fc 07`**
  ([#7](https://github.com/scttfrdmn/burroughs/issues/7)). `i32`/`i64.trunc_sat_f{32,64}_{s,u}` —
  the same three-way range analysis as the trapping `0xa8`–`0xb1`, with the verdicts replaced:
  NaN → 0, below the low bound → the minimum, at or above the high bound → the maximum
  (`convert.ml:97-143`, `:198-248`). They are **total functions**, so the helpers have no error
  return at all; giving them one would be the `memory.grow` mistake, since `conversions.wast`
  asserts every case as `assert_return` and a trap would convert passing vectors into trap answers.
  `TestTruncSatNeverTraps` is the signature stated as a control.

  **The NaN test comes first, and its control is architecture-dependent.** Every comparison against
  a NaN is false, so a NaN reaching the range tests falls through to the conversion — which Go
  leaves implementation-dependent, and the two gated arches disagree: `int32(NaN)` is `0` on arm64
  and `-2147483648` on amd64. Deleting the check is therefore invisible on an Apple laptop and
  fails four rows in CI. The falsification pass found this by running the mutation on both, and the
  numbers are in the test's comment rather than a claim that the rows fail.

  **This is the first prefixed instruction to execute**, which is one structural hazard: `Instr.Op`
  holds the *sub*-opcode, so `fc 00` and `unreachable` are both `Op == 0x00`. The prefix is consumed
  by a gate in `exec.go` that dispatches 0xfc to its own switch and still sends every other prefix
  to `unsupported`; `TestPrefixedInstructionIsNotDispatchedByOpAlone` asserts both directions,
  because a wrong engine passes either one alone.

  Two of the implementation's own comments were **measured false and corrected before landing**: the
  unsigned low arm's witness is `-1.0`, not `-0.5` (Go truncates `-0.5` to negative zero, which is
  not `< 0`, so the `(-1, 0)` vectors pass with the arm deleted), and `int64(d)` above 2^63 does not
  yield the sign bit as drafted — it saturates to max on this host. Both were prose asserting a
  mechanism instead of reporting one.

  Board: pass **28414 → 28594** (+180), exec fail **1199 → 1019**, encode unmoved at 1353, gated
  unmoved at 1721; `unsupported` **unmoved at 32377**. All eight buckets are absent, not smaller.
  Set-differenced on `(file, line)`: **180 departed, 0 arrived, 0 same-key-new-reason** — the zero
  in the third category interrogated per grave
  [#106](https://github.com/scttfrdmn/burroughs/issues/106) rather than quoted, since it joined
  2372 live pairs and detects a change on a synthetic one. The flat account is the finding: these
  vectors have nothing behind them, where `global.get`'s had linking and further opcodes. All 180
  are in `conversions.wast`, which goes **347/527 → 527/527**.

- **The interpreter executes `global.get` and `global.set`**
  ([#7](https://github.com/scttfrdmn/burroughs/issues/7)). The 76 arrivals the previous entry
  named, answered. A global's storage is **two slots and a declared type** — a `uint64` and a
  `ref` — which is `stack`'s split for 0002's reason rather than a new decision: `global.get` of
  an externref must push onto the reference array, and a `uint64` holding a reference is invisible
  to the collector. Which slot is live is decided by the *declared type*, never by inspecting the
  bits, because a null reference and the integer zero are the same eight bytes.

  **Instantiation now fills globals before tables and memories**, and the order is load-bearing
  rather than tidy. `eval.ml:1296`'s fold runs globals ahead of both, and `init_global` evaluates
  each initializer against the **partially built** instance (`eval.ml:1206`), so `(global i32
  (global.get 0))` reads a global initialized one step earlier and a table's segment offset can
  read a global. An allocate-then-evaluate engine gets a different answer; probing the mutation
  showed it reports *"global 0 was declared but not initialized"* rather than a silent zero, which
  is the nil-slot convention turning a wrong value into a loud one.

  Immutability is **not** checked here: `global is immutable` is an `assert_invalid` string, so
  the verdict is [#9](https://github.com/scttfrdmn/burroughs/issues/9)'s and the `mutable` field
  is recorded and unread. `TestImmutableGlobalIsNotRefusedHere` asserts the *absence* — a tripwire
  that starts failing the day the validator lands, which is how it should be found.

  This spends the last of 0002's reference-stack pin: `stack.refs` has its first writer, and both
  halves of the pin retired to consumers their own comments did not predict — the `ref` *type* to
  element-segment initialization, the `refs` *array* to a global rather than to `ref.null`.

  Board: pass **28398 → 28414** (+16), exec fail **1215 → 1199**, encode unmoved at 1353, gated
  unmoved at 1721; `unsupported` **unmoved at 32377**. Both buckets reach zero — `no arm for
  opcode 23` (15) and `opcode 24` (14) are absent, not smaller. The 29 they held is not the 16
  gained: **13 vectors changed cause without changing verdict**, hitting §3 linking (9),
  `table_set` (3) and `ref_is_null` (1) behind the arm that landed. Set-differenced on `(file,
  line)` keys, so departures are 16 and arrivals **zero**.

- **The wat encoder writes the global section, and `ref.null` writes its heap type**
  ([#8](https://github.com/scttfrdmn/burroughs/issues/8)). The two frontiers behind 47% of the
  fail column, and they interlock: nearly every `(global funcref …)` in the suite is initialized
  by a `ref.null`. Section 6 is `vec(globaltype expr)` where `globaltype` writes **valtype then
  mutability** — the reverse of both the OCaml constructor order and the text's `(mut i32)`
  spelling, so the byte order comes from `encode.ml:193` rather than from either surface form.

  **`heaptype` is not `reftype`**, one byte at the same offset: `ref.null`'s immediate is a *bare*
  heap type with no nullability field, and the two productions agree only on func `0x70` and
  extern `0x6f`. Rendering one in the other's spelling would assert a nullability the grammar
  never supplied — grave [#36](https://github.com/scttfrdmn/burroughs/issues/36)'s
  invented-evidence class, so `TestRefNullEncodesItsHeapType` pins the emitted bytes directly.
  It has to: the *decoder* does not retain the heap type yet, so `ref.null func` and `ref.null
  extern` decode to identical instruction pairs and no structured comparison can tell `d0 70`
  from `d0 6f`.

  Board: fail **3682 → 2568**, pass **27451 → 28398** (+947), gated **+167** — the column
  conserves exactly, and the 1114 departures partition 966 `(global …)` + 148 `ref.null`. The
  +167 are the drain's own consequence: vectors carried forward to the *next* frontier, a feature
  gate, and registered per-vector in `TestGatedVectors` with each root module's gate string
  probed rather than inferred. Exec fail rose 1139 → **1215** on 76 arrivals and zero departures
  — `global.get`/`global.set` reaching the interpreter, which names the next work.

- **`call`, `call_indirect`, and `return_call_indirect` execute; element segments are retained and
  encoded** ([#7](https://github.com/scttfrdmn/burroughs/issues/7),
  [#8](https://github.com/scttfrdmn/burroughs/issues/8), decision
  [0016](docs/decisions/0016-unbounded-immediates-live-beside-the-body.md)). The largest bucket on
  the board, and it needed a table with something in it — so `decodeElemSegment`, which validated
  and discarded all five of its fields, now stages an `ElemSegment` in the module's index order,
  and the wat encoder writes all eight element flags.

  **A wasm frame is a Go frame**, which is 0002's giant-switch reasoning applied to calls: the
  recursion costs nothing per *instruction*, where an explicit `[]callFrame` pays at every
  dispatch. It has a known expiry — v1's stack switching (contract §7) cannot capture a
  continuation out of the Go stack — and the expiry is a phase rather than a defect. Exhaustion is
  the reference's counter (`flags.ml:9`), not a stack probe, because `assert_exhaustion` is written
  against a counter's behaviour; the budget is **10000** rather than the reference's 256, since
  nothing in the spec bounds recursion at 256 and a Go guest's own depth routinely exceeds it (§1's
  thesis workload deciding, as in 0002).

  `call_indirect`'s three failures are checked in the reference's order — bounds, then null, then
  type — and the order is not stylistic: checking nullness first reports `uninitialized element`
  for an out-of-range index on **every table this engine builds**, because a fresh table is
  null-filled. The type test is **structural** (`Match.match_deftype`, which reduces to equality
  for MVP functypes), so a call naming `$b` accepts a function declared with a structurally
  identical `$a` — 2 vectors in `type-rec.wast`, and the accept direction no rejection corpus can
  see. The two immediates are **type first, table second** on the wire (`encode.ml:275` is
  `op 0x11; idx y; idx x`), the reverse of how the text reads them, pinned by a fixture whose two
  tables are both filled so an inverted reading transposes the *answers* rather than trapping.

  **The board: pass 26833 → 27451, fail 4319 → 3682, gated 1535 → 1554, `unsupported` unmoved at
  32377** over 254 files. The 2255-vector `call_indirect` bucket is drained; the columns close
  exactly (−637 fail = +618 pass, +19 gated), while the *bucket* flow overshoots by 18 because
  other buckets drained in the same motion. Of the drain, 469 moved to the exec/encode strata
  rather than to pass — 420 blocked on linking, 57 on the missing `fc 0e` (`table.copy`) arm, and
  the rest on later frontiers — each counted from the harness's own `Failure.Stratum` classifier
  rather than from a message prefix, which is the distinction grave
  [#129](https://github.com/scttfrdmn/burroughs/issues/129) exists for and which a first attempt
  got 6 short of.

- **`br_table`, both halves: the label vector is retained, encoded, and executed**
  ([#8](https://github.com/scttfrdmn/burroughs/issues/8), decision
  [0016](docs/decisions/0016-unbounded-immediates-live-beside-the-body.md)). The first immediate
  this engine keeps that does not fit `Instr`'s two words. It lives in a **side table beside the
  body** — `Func.Labels`, keyed by instruction index — rather than in a `[]uint32` field on every
  instruction, which would tax a megabyte-scale Go guest 24 bytes per instruction to serve one
  opcode in 256. Read through `LabelVector`, whose second result keeps "no vector" distinct from
  "an empty vector": a `br_table` with zero labels is legal and means *every* index takes the
  default.

  **The wire form is not the written form, in three ways, and the encoder does all three.** The
  text is `idx idx_list` — no count, no separator, never empty — while the encoding is
  `vec(labelidx) labelidx`: a count the parser does not yet have precedes the members, and the
  **last written** label is the default rather than a member (`Lib.List.split_last`). So
  `br_table 0` is an *empty* table plus default 0, three immediate bytes and not two. Each
  transformation has a round-trip row whose unique catch was measured rather than asserted —
  including one that catches a *deduplicating* encoder, the defect the other two are blind to.

  Execution follows the reference exactly where it is easy to get wrong: the operand is
  **unsigned** and out of range takes the default rather than trapping (`eval.ml:298-301` is
  `I32.ge_u`), and the index is popped **before** the branch, so the selected label's arity counts
  what is left below it. Reading the operand as signed sends −1 into the vector's tail; both
  readings are pinned by a row that a wrong engine answers with a different number.

  **The board: pass 26567 → 26833, fail 4635 → 4319, gated 1485 → 1535, `unsupported` unmoved at
  32377.** The bucket this drained held **1330** and is now **0**, of which 266 became passes — and
  the other 1064 are accounted for individually rather than described, a full bucket-set diff
  showing exactly three nonzero deltas: **+1006** to `cannot yet encode the call_indirect
  instruction`, **+8** to `interp: no arm for opcode 10` (`call`), and +50 to `gated`. That is the
  **dependency closure**: `br_table.wast`'s own module calls `call_indirect`, so the file is still
  1/147 with every fail re-keyed to the instruction it now waits on. The 50 new gate entries are
  `align0`'s multi-memory memarg flags and `align64`'s i64 index type, their causes read off the
  engine's own decline strings and the decoder's memory census rather than off the files' names.

- **The `assert_return value mismatch` bucket is pinned as downstream of a failed setup, so an
  arithmetic defect cannot hide in it** ([#140](https://github.com/scttfrdmn/burroughs/issues/140)).
  The board keys that bucket by a fixed string, a vector wanting a *value* having no expected error
  text to bucket by — so one name covers both "the engine computed a wrong answer" and "the engine
  never ran the code that would have written the right one", which are opposite work plans. The
  bucket grew 48 → 280, read as a family with a shared semantic root, and **the measurement killed
  that reading**: 280 mismatches in 16 modules, every one of them behind its own module's failed
  setup `(invoke …)`, 280 of 280 `i32 want / i32 got`, and the 62 failing setups all naming
  mechanism — `fc 0a` 22, `fc 0b` 13, `memory.init` 10, `table.init` 10, `call_indirect` 7. The
  bucket scales with modules *admitted*, not with executed surface. It is now a claim rather than a
  comment: when it breaks, the engine returned a wrong value on a module whose setup ran, and that
  arrives as a red board with line numbers instead of an argument. Two instruments were corrected on
  the way — a discriminator reading the shape of `Got` was **backwards**, because `checkRange`
  returns an index and non-zero is the *un-copied* signature; and a whole-file scope was unanimous
  because it could not dissent, repaired to per-module span and pinned by
  `TestMismatchClassifierCanDissent`.

- **The block family, both halves: `block`, `loop`, `if`/`else`, `br`, `br_if`, `return` and
  `select` encode *and* execute** ([#7](https://github.com/scttfrdmn/burroughs/issues/7),
  [#8](https://github.com/scttfrdmn/burroughs/issues/8)). The interpreter is no longer straight-line.
  A control stack of labels, one entry per active construct; `matchEnd` finds a construct's extent by
  paren-free nesting depth; `elseOf` matches an ELSE at depth 1 only, so a nested `if`'s ELSE cannot
  be taken for the outer one's. **The one asymmetry is the continuation**: a loop's is its own header
  and a block's is past its END, and the arity a branch sees follows it — branching to a loop supplies
  its *parameters*, branching to a block yields its *results*. Both directions are pinned by a row
  that a wrong reading fails rather than mis-answers.

  **Taken as one mechanism because taking half of it drains nothing.** The encoder half alone converts
  encode fails into exec fails; the interpreter half alone has nothing to run, since `internal/text`
  refused every module with a block in it. The scope was recorded on #7 before the work started —
  *the PR takes both halves or it is not worth taking* — and `call_indirect` was measured **out** of
  it on the same rule: executing it needs a table index space and element segments `binary.Module`
  does not retain, so it stays behind #8's frontier with its reason at the refusal site.

  **The board: pass 26307 → 26567, exec fail 440 → 662, encode fail 4693 → 3973, gated 1031 → 1485,
  `unsupported` unmoved at 32377.** The pass column's +260 is two things and they are stated apart:
  **+257** from the family and **+3** from grave #135 below. The exec ceiling *rising* is the same
  motion seen from the other side — vectors that could not be built before now instantiate and run.
  The +222 is `+227 memory_copy.wast` and `+5 memory_fill.wast` value mismatches (their `checkRange`
  export is a `loop` around an `if`, so nothing in those files could be built at all), `+7 10 call`,
  `+9 fc 0b`, and `−26 0f return`. The 662 partition to exactly 662 by cause: 309
  opcode arms this phase does not have, 280 value mismatches (every one measured to stand behind a
  failed setup invoke, and the classifier falsified to prove it distinguishes), 52 naming linking, 21
  the encoder's frontier met at instantiation.

  **The gate allowlist grew by 454, from 24 module heads no entry had ever named** — memory64 315,
  SIMD 102, multi-memory 37. None could inherit an existing entry: these modules had never reached
  the decoder, because the text encoder refused them at a block. The verdict arithmetic closes
  without slack: fail −711 = pass +257 + gated +454.

- **`internal/interp`: linear memory — loads, stores, `memory.size`, `memory.grow`, and
  instantiation that can trap** ([#7](https://github.com/scttfrdmn/burroughs/issues/7)). The engine
  has a memory. `memory` is a flat `[]byte` grown by reallocation, which is `memory.ml`'s own shape
  and what §1's workload wants — a Go guest that loads once and runs for hours has a steady state of
  one contiguous slice with no per-access indirection. The load/store family is a **table** rather
  than 23 switch arms (`memop`), machine-checked against the generated opcode table's mnemonics:
  `i64_load16_s` parses to (8-byte slot, 2-byte access, signed) and is compared, so the widths and
  sign-extension flags are *derived* from an authority with a conformance record instead of
  transcribed by hand. Getting any one of those three wrong yields a plausible value for a valid
  module, which is an accept-direction defect the suite scores green by construction (§9 G-3).

  **The board: pass 17923 → 26307, exec fail 8713 → 440, unsupported 32764 → 32377, gated
  957 → 1031, files 253 → 254 — and it sums.** +8384 pass is the largest single earn in the
  project's history, and a movement this size is not understood by silhouette, so the arithmetic
  is stated rather than implied: `8384 = 8057` (fail drop) `+ 387` (unsupported drop) `+ 14` (new
  commands) `− 74` (gated growth). The subtracted term is the one worth writing out — all 74 new
  declines are `kind=invoke`, the Kind this PR added, so they are new *questions* rather than new
  answers and they take 74 off what the pass column could otherwise claim. Against the
  pre-registered 8,424 the delivery is short by **40**, that cohort being the
  `assert_trap (module)` forms: predicted as conversions, delivered as 34 exec fails needing
  element segments or a start function. The 289 residue came out **exact**. The
  `2d i32.load8_u` bucket went **8001 → 0** and with it the whole load/store region. The 440 that
  remain partition to exactly 440 and are named at `execFailCeiling`: 291 opcode arms this phase
  does not have, 48 downstream of the missing `fc 0a`/`fc 0b`, 41 naming linking, 34
  `assert_trap (module)` forms awaiting element segments or a start function, 26 `memory.copy` and
  `memory.fill` reached directly.

  **The gain was measured rather than quoted, and the census was wrong in both directions.**
  Pre-registered: ~8,424 payable, 289 residue. The 289 came out exact — and two buckets appeared
  that the census had not predicted, 95 value mismatches and 41 memory-index errors, where 95 had
  been **zero**. Quoting the 8,000 without them would have been the flattering half of a
  measurement. Of the 95: **22 were the memory index space being shared between imports and
  definitions, imports first.** Sizing the table `len(m.Memories)` made `memory.size $mem1` return
  `$mem3`'s page count — not an unimplemented import reported honestly, a *wrong answer about a
  different memory*, and green on 22 vectors across two files by construction. The other 73 were the
  harness's: a bare top-level `(invoke …)` was never run, so a following `assert_return` read a
  memory nobody had stored to. Both fixed; the 48 remaining are the `fc 0a`/`fc 0b` line above.

  **Instantiation gained a trapping path without gaining an opinion**
  ([0015](docs/decisions/0015-instantiation-is-execution-at-time-zero.md)): copying an active data
  segment out of bounds is a *runtime event*, so `Instantiate` returns `*Trap` and a verdict cannot
  travel through it even by mistake. Two kinds of failure, two channels — verdicts belong to the
  validator forever, traps belong to execution, and instantiation is execution at time zero. The
  taxonomy is the suite's rather than this engine's, which is what settled it: the corpus contains
  54 `assert_trap` forms wrapping a bare `(module …)`, of which `data1.wast` is 14.

- **`internal/spec`: two harness Kinds — bare `(invoke …)` and `assert_trap` wrapping a module.**
  `KindInvoke` shares an arm with `KindAssertReturn` because a bare invoke *is* an assert_return
  with no expectation. It drains the `invoke` unsupported head 357 → 10. **A harness that drops a
  state mutation is not neutral about the vectors after it** — this is *a skip is not a verdict*
  with the roles swapped: the skip passed by asking nothing and made a *neighbouring* vector fail.
  `KindAssertTrapModule` admits the bare-wat module-wrapping shape and accounts for the other 40 of
  the 387 unsupported drop. All **54** such forms are on the board and only **40** are that drop,
  because the remaining 14 are `data1.wast` — every form in it is this shape, so it held no scorable
  command, `boardFiles` did not select it, and its 14 arrive as new commands **in a new file**:
  253 → 254, the one place this PR moves the denominator. `54 − 40 = 14` would have called an
  admission a conversion; the difference was measured by set-differencing the `assert_trap` command
  keys against `b11a664`.

  ***A conversion lowers the column; an admission raises the denominator*** — two operations with
  different honest signatures, and the pair is now board vocabulary, recorded at `Result.Total`
  where the columns' meanings already live. A conversion moves a command the harness already saw:
  one column falls, another rises, the command count does not move. An admission makes a command
  *exist*: the count rises, `Total()` rises, nothing falls. Both read as progress and only one
  drains anything, so a delta quoted without saying which it is cannot be checked. The practical
  test, reasoning-by-subtraction being precisely what failed here: **difference the command keys
  against the previous revision, never subtract totals** — totals agree with both stories, key sets
  do not.

- **`binary.Module.ImportedMems`**, the memory index space's offset, sharing `importedCount` with
  `ImportedFuncs`. The same rule as its sibling and it had to be paid for separately, which is the
  lessons-indexed-by-shape rule arriving as a bill.

- **`internal/text`: the data section, the data count section, and the memory field's inline-data
  sugar** ([#8](https://github.com/scttfrdmn/burroughs/issues/8)). Section 11's three arms are
  discriminated on the *resolved* memory index per `encode.ml:1092`, so `(data (memory 0) …)` and
  `(data …)` produce identical bytes and the `Declarative` arm is unreachable from wat — `data` has
  no declarative production in `parser.mly:1094`, only `elem` does. Section 12 is emitted on the
  reference's own condition, which is about **instructions and not segments**: `free.ml:217` makes a
  segment contribute nothing to the `datas` set, which is fed only by `memory.init`, `data.drop`,
  `array.new_data` and `array.init_data`, so a module with a hundred segments and no data-referencing
  instruction gets no section 12 and `(func (data.drop 0))` with no segments at all gets one holding
  zero. Both directions are pinned, because the obvious `len(datas) > 0` test gets them backwards.

  **The board moved 9082 vectors and earned none of them, which is the honest headline.** Encode
  stratum **13775 → 4693**; of that 9082, **8272 became execution fails and 810 became gate
  declines, and zero became passes**. A `(data …)` module that now encodes still needs a *load arm*
  to answer its `assert_return`, so `interp: no arm for opcode 2d` went 9 → 8001 and is now the
  largest single number on the board. `unsupported` is **unmoved** at 32764: this payment landed in
  the encode column, not that one, and the two are not interchangeable. `execFailCeiling` is re-based
  441 → 8713 with the construct named, per the licence its own comment wrote.

  **The sink escapes `funcField`, and that is the grammar's shape rather than scope creep** (Scott's
  ruling): section 11's grammar puts a constant-expression in a *module field*, so the instruction
  sink that `funcField` owns has to be swapped in and restored outside it. Documented at the site as
  grammar-forced and falsified at birth — deleting the restore leaks the offset's `i32.const 0` into
  module-field scope, where the visible symptom is a *later* field refusing at the wrong layer.

  **The prose at that site was wrong and the correction is the finding.** It claimed the saved sink
  "is genuinely non-nil in one case"; a counter on both branches over all four `(data …)` spellings
  and the memory sugar reports `nil=1 nonNil=0` on every one. A control written to the old claim
  would have hunted for a non-nil outer sink, found no input producing one, and been **stillborn** —
  so `TestDataOffsetRestoresTheOuterSink` asserts the direction that exists instead.

  **Section 11 is witness-blind and says so at the site.** `binary.Module` retains no data segments
  (#7 will force it, when the interpreter's memory tests need them), so the round trip cannot see
  this section at all and the witnesses are byte-level over `Section.Payload`. Saying so keeps the
  round trip's silence from impersonating a check.

  *(Superseded within this same `[Unreleased]`, one entry later: #7's memory work forced the
  retention on exactly the predicted schedule, so `Datas` now exists and this section is no longer
  witness-blind. Left standing rather than edited — the prediction and its due date are the part
  worth keeping, and an entry rewritten to match the present cannot show that a filed expectation
  came in.)*

  `internal/testenv` licenses a fifth reference authority, `interpreter/syntax/free.ml` — the
  authority for *which index spaces an instruction references*, which neither `decode.ml` nor
  `encode.ml` answers alone, and which `internal/binary`'s `dataRefOps` has cited since #22 with
  nothing resolving it. Its floor is 5000 bytes against the other four's 20000, which is the
  argument for per-file constants arriving as a measurement rather than as a principle.

- **`internal/text`: the memarg emitter, and the default alignment table it needs**
  ([#8](https://github.com/scttfrdmn/burroughs/issues/8), 0007). The load/store immediate — flags
  byte, optional memory index, offset — is `encode.ml:221`'s `memop`, and it is the largest single
  immediate shape on the board's biggest wall: **fail falls 14330 → 14216**, the encode stratum
  **13974 → 13775**, and twelve load/store buckets appear for the first time in the *execution*
  column (`28`, `2d`, `36`–`3e`), which is what an encoder gap closing looks like from downstream.
  The 9,529-vector census was upper-bound-shaped, exactly as `code.go:195` pre-registered: a bucket
  keyed on a module's *first* refusal moves it only if nothing else in it is unencodable, and the
  freed `address*.wast`/`memory_copy*.wast` sweeps hit `(data …)` next — now the largest bucket at
  **8882**, 7.6× the runner-up.

  **`internal/text/memarg.go` is generated, and the reason is that its 45 defaults are invisible to
  the suite.** A wrong natural alignment writes an image that decodes clean and differs from the
  source only in a flags byte no `assert_malformed` inspects — contract §9 G-3, and decision 0007's
  argument for machine derivation applied to a third table. `memarggen` reads `lexer.mll`'s
  `opt a N` per arm, `make memarg-drift` asserts continued agreement, and the floors are **per
  partition with the exact count pinned beside each** (grave #105's lesson: a floor catches a moved
  file, never a 6% silent loss).

  **The emitter has two witnesses because one of them is structurally blind.** `decodeMemop`
  discards alignment — it is a validation constraint with no execution semantics — so the
  round-trip table cannot see the whole 45-row table, in precisely the way it cannot see the limits
  address-type bit. `TestEncodeWritesTheNaturalAlignmentDefault` reads the flags byte directly, in
  three named partitions. Six falsifications were installed and each now fails at a named
  assertion; **the most valuable outcome was one that passed** — the presence-vs-value defect
  (`has_idx` read as "the text wrote an index" rather than "the index is non-zero") emits
  `28 42 00 00` where the correct image is `28 02 00`, and because the emitter writes its own
  body-size field the decoder returns identical `Imm0`/`Imm1`. A self-consistent wrong image
  round-trips clean; only the flags byte distinguishes them, so that property moved to the
  byte-level witness and the round-trip row records that it is stillborn for it.

  **`MultiMemory` joined the decode-side gate set by a row failing, not by reasoning ahead.**
  `memopIndex` records its bit-6 decline on the context rather than returning it — but `release()`
  hands the recorded decline back once the body's grammar completes, so a deferred decline is still
  a decline. Third derivation of that rule and third time the prose was wrong until an input said
  so.

- **`internal/interp`: the interpreter, and the first instruction Burroughs ever executed**
  ([#7](https://github.com/scttfrdmn/burroughs/issues/7), 0002). Decoder → internal form →
  execution, over the 139-opcode numeric core: constants, locals, drop/nop/unreachable, the full i32
  and i64 arithmetic and comparison sets, the f32/f64 sets, and the conversions. The board's fourth
  column stops being hypothetical — **`assert_return` falls 52638 → 24530 unsupported**, and the
  whole 60872 → 32764 movement in the unsupported column is that one head.

  The opcode set is what a measurement recommended rather than what was easy. The numeric core makes
  **13671** `assert_return` commands answerable; adding all of block/loop/if/br/br_if/br_table/
  return/call/call_indirect takes that to **13699**, `select` adds **zero**, and globals add **7** —
  so the structural instructions are not what the board is waiting on. The remaining ~38900 sit
  behind the text encoder's instruction frontier (#8) and behind v128 and reference types.

  0002's three pinned choices are implemented as pinned, each with its measurement at the site: a
  **giant switch** rather than a closure table (21.5µs and 72 B/op against the switch's 11.9µs — the
  form that looks like a dispatch table is both slower and allocating on the workload §1 names, and
  `internal/interp/dispatchbench` keeps the reproducer so the negative is not re-derived); `[]Instr`
  **is** the program, walked by an index a branch target will write to, with no second lowering; and
  the value stack is **bare `uint64` slots plus a pinned parallel reference array**, empty throughout
  v0 and declared rather than discovered.

  **Three error kinds, because the suite scores them in opposite directions.** `Trap` carries the
  spec's own trap texts (an `assert_trap` matches against them); `ErrUnsupportedOp` names the
  engine's own gap by opcode, so the fail bucket for this layer reads as a work list keyed by
  instruction; `ErrNotValidated` is the layering debt for what #9 would have rejected, and it must
  never render as a spec verdict — `type mismatch` is the validator's string, and reporting it from
  here would put #9's answer where #9 cannot be tested from. `Instantiate` **cannot fail** for the
  same reason: every failure a real instantiation reports is a validation verdict.

- **`spec.TestHarnessAndEngineLiteralReadersAgree`: the second opinion, cross-checked over the
  corpus's own spellings** ([#7](https://github.com/scttfrdmn/burroughs/issues/7), 0007). The harness
  reads an `assert_return`'s expected literal with `readConst`, derived independently from the
  reference's fxx.ml/ixx.ml; `internal/text` reads the module's constants with its own. 13670 of the
  13671 answerable vectors get their module from wat source, so without a cross-check a conversion
  bug would shift both sides together and the vector would pass by construction — grave #106's shape,
  landing hardest on `const.wast` and `float_literals.wast`, whose entire purpose is literal
  conversion.

  It compares on **6498 distinct `(TYPE.const LEXEME)` spellings read verbatim out of the `.wast`
  files**, each compiled into a one-instruction module whose return value *is* the engine's
  conversion of that exact text. The rejected design rendered a `Val` back to a lexeme and
  round-tripped it, which exercises one canonical form per value and never `0xa0ff.f141a59a`,
  `nan:0x200000`, `1_000_000` or `+0x1p-149` — the spellings that matter. Comparison is on `Bits`,
  never `Matches`, because `Matches` admits the NaN classes and would let a payload disagreement pass
  as a class agreement. A legality disagreement is an **error, not a skip**: a spelling the harness
  converts and the engine rejects is an over-rejection, the class no reject-direction corpus can
  falsify. It found grave #125 on its first run.

- **`internal/text/code.go`: the code section and its companion the function section — #8's largest
  bucket, and #7's door** ([#8](https://github.com/scttfrdmn/burroughs/issues/8), 0011 part 2). The
  parser starts *retaining*: every reader in `instr.go` was called for its error and its value
  discarded, which was correct while the product was a recognizer and is what blocked 1358 of 2143
  parser-accepted modules. The suite's encodable population goes **196 → 926**, and the `func`
  frontier bucket is gone from the census histogram entirely rather than reduced.

  Retention is deliberately shallow — an instruction becomes an opcode plus its already-encoded
  immediate bytes, appended to a flat list in emission order. It is **not** 0002's internal form and
  must not grow toward one: 0002's form is the interpreter's, built from `binary.DecodeModule`'s
  output on the path that has a conformance record, and a second representation growing out of the
  *text* path is precisely the option 0011 refused.

  **The independent witness moved by an order of magnitude, and its size distribution is the part
  that matters.** Against #67's wabt corpus, joined on (file, ordinal): **878** of the 926 join, and
  all 878 agree **byte for byte, 0 disagreements**. Before this section the largest agreement was
  `type#0` at 148 bytes; now it is `names#2` at **7584**, with `float_literals#0` at 3010 and four
  SIMD arithmetic modules between 842 and 1944. A seven-kilobyte image agreeing byte for byte with a
  toolchain that has never seen this parser is a different order of claim from a preamble, because a
  code section is where an off-by-one immediate lives. The 48 unjoined fall in exactly nine files,
  every one of them in the manifest's `skipped_files` with wabt's own reason.

  **The frontier moved *inside* an instruction, which is new.** Sections 1–7 are each a field the
  parse retains in full or refuses; a function body is a sequence whose members are individually
  encodable or not, so `refuseUnencodable` refuses per *instruction* and the module withdraws. Five
  of sixteen immediate shapes are writable — the largest set whose emission is a lookup, no natural
  alignment defaults and no block types — and the remaining tiers measure 249 memarg and 364
  structural-and-control, both reproducing the pre-landing forecast (231 and 363) now that the
  buckets can be read off real refusals.

- **`gate:extended-const` — the ninth `Features` gate, and the first one that governs a *position*
  rather than a byte** ([#109](https://github.com/scttfrdmn/burroughs/issues/109)). The six
  instructions the proposal adds (`i32.add`/`sub`/`mul` = 0x6a/0x6b/0x6c, `i64` = 0x7c/0x7d/0x7e) are
  MVP opcodes that stay ungated in function bodies and become admissible inside a constant
  expression, so nothing in the image distinguishes the gated construct from the ungated one. That
  ruled out a `gatedOpcodes` row: `gateCheck` dispatches on the opcode alone, so an entry there would
  decline `i32.add` in every function body — reintroducing, as its own fix, the accept-direction
  defect #109 was filed for. The gate lives in `gatedNonOpcodes` and is checked in `constLegal`,
  where the position is known.

  **Contract §9 G-2 was amended to say extended-const is tracked** (Scott's stamp on #109; the
  amendment cites it in its provenance line, and G-2's parenthetical was swept against the 3.0 merged
  list once, completely, so the enumeration is auditable rather than illustrative). The nine
  extended-const modules therefore move to gated-by-default in v0's posture and are **earned in the
  all-on lane**, where `gated` must be 0 — the structural bound that keeps a deferral from becoming a
  disappearance.

- **`internal/gen/xcorpus/accept_test.go`: the permanent accept-direction control** (contract §9 G-3,
  and product work under the standing rule for exactly that reason). Two walks over the 1954
  committed corpus images: under the tracked union the decoder must accept **every one, zero
  rejections exactly** — an equality rather than a ratchet, because every module here is one a
  conforming producer emitted from a must-succeed suite module and there is no honest nonzero value —
  and in v0's default posture every rejection must be an `errors.Is` feature decline that does not say
  *malformed* (#5, pinned over 692 modules rather than over one probe). The measurement that found
  #109 was made by hand and then discarded; this is the *artifacts become oracles* rule applied one
  step later than it should have been.

- **`internal/text/encode.go`: the export section emitter, both spellings**
  ([#8](https://github.com/scttfrdmn/burroughs/issues/8), 0011 part 2). Section 7 joins 1, 2, 4 and 5
  in id order, and all five extern kinds export. The suite's encodable population goes **141 → 196**
  of 2150 parser-accepted text modules, and the `export` frontier bucket at 39 goes to **zero** — it
  is gone from the census histogram rather than reduced.

  **A bucket of 39 paid 55 modules, and the arithmetic is the finding.** An export is not a field one
  writes a section for so much as a construct that appears *inside* five other fields, so draining it
  also moved `memory` 104 → 31 and `table` 29 → 13: those were modules whose first blocker was an
  inline export on a memory or table, which the field's own emitter could not withdraw for. The
  memory/table PR observed that draining a bucket re-sorts the queue instead of emptying it; this is
  the first counter-example, and it sharpens the rule rather than refuting it — a bucket keyed on a
  construct other fields *embed* pays more than its size, one keyed on a field that embeds others
  pays less. `func` at 1361 is the second kind.

  **This is the first section with no payload branch, and that is a fact about `externidx`.** An
  `externtype` carries the thing's type, so section 2's five kinds each write a descriptor; an
  `externidx` carries only an index into a space that already holds the thing, so all five collapse to
  a kind byte and a `u32` (encode.ml:1009-1014). The kind bytes are `externtype`'s own — identical
  order in both grammars — so `externKindByte` is **reused**, and the reuse's premise is now
  machine-checked against the reference rather than assumed: `TestExternKindByteAgreesForBothSections`
  extracts both arm lists from encode.ml and compares **by constructor, not by position**, because a
  positional check on two lists reordered together passes while every byte moves. Its region bound and
  in-bounds sample check are grave [#106](https://github.com/scttfrdmn/burroughs/issues/106)'s lesson:
  an unbounded reader finds the other grammar's arms and the comparison becomes a tautology. Removing
  the bound reports 12 arms where 5 exist, which is the vacuity floor doing its job in the loud
  direction.

  **The two spellings differ in whether they look anything up, and the sugar's answer is "no".**
  `(export "e" (func $f))` resolves `$f` in one of five index spaces; `(func (export "e"))` takes the
  index from the enclosing field — the reference's arm is `fun d c -> Export ($3, d @@ $sloc)`, the
  index arriving as a *parameter* supplied as `$1 (FuncX x) c` where `x` is `bindidx_opt`'s result. So
  `bindidxOpt` now returns that index, read **before** either arm binds, matching the reference's
  `bind` returning `space.count` before the shift. A non-zero-index row pins it:
  `(module (memory 1) (memory 2) (memory (export "third") 3))` fails if the emitter writes 0 *or* if
  it reads the count after binding and writes 3.

  **The `exported bool` parameter is gone from `inlineImportTail`, and its disappearance is the
  section landing.** It existed to suppress the frontier withdrawal, an inline export having made a
  field unencodable; now the withdrawal is unconditional and the five call sites that computed it have
  nothing to compute. Removed rather than left as an always-false argument, because a parameter no
  caller can vary is a branch no test can reach. Seven rows moved out of
  `TestEncodeRefusesWhatItCannotWrite` into `encodableModules` with paired-spelling assertions, and
  that move *is* what the section is.

  **The export withdrawal check fires in both directions, unlike the memory/table tripwire it copies.**
  `exportsSeen` is incremented in `exportHead` — the `LPAR EXPORT name` prefix both spellings share,
  which is a real production boundary rather than a factoring convenience — and `len(exports)` comes
  from `defineExport`, so the two numbers are produced by different code. Deleting `defineExport` from
  `inlineExports` fails ten rows with `0 exports retained, 1 parsed`; doubling it fails them with `2
  … 1`; and the mixed-spelling row reports `1 … 2`, which is the check distinguishing *which* spelling
  forgot. The forgetting is not hypothetical: the sugar parsed and discarded its name for four PRs and
  every board was green, because a module missing an export it never claimed to have is not a decode
  error.

  **`TestExportResolvesInEverySpace` is scoped to the space rather than to today's cases**: per index
  space, an unbound name is rejected with the reference's own word, and a name bound in each of the
  *other four* spaces is still rejected — 5×4 cross-space rows. Wiring the global arm to the memory
  space fails it in the **accept** direction, `(module (memory $x 1) (export "e" (global $x)))` being
  accepted, which is a valid image naming the wrong thing and exactly what no suite vector covers.

  **The independent witness: 156 joined against wabt's corpus, 156 agree byte for byte, 0 disagree,
  147 longer than a bare preamble** — from 101/101/0/92. Both figures were measured with the same
  probe, the baseline re-run against the merge-base rather than quoted from the previous revision of
  the comment that holds it, because a delta between two instruments is not a delta. The 40 unjoined
  did not move, which is the expected shape: those six files are excluded wholesale by the corpus
  *generator*, so nothing this encoder learns to write can join them.

- **`internal/text/encode.go`: the import section emitter, both spellings**
  ([#8](https://github.com/scttfrdmn/burroughs/issues/8), 0011 part 2). Section 2 joins 1, 4 and 5 in
  id order, and all five external kinds encode — func, table, memory, global, tag. The suite's
  encodable population goes **51 → 141** of 2150 parser-accepted text modules, and the encoder's
  largest frontier bucket, `import` at 177, goes to **zero**.

  **The wat grammar spells an import two ways and the reference builds one thing from both**, so the
  section is emitted from a single retained list: the `(import …)` field, and the inline-import sugar
  inside `func`/`tag`/`global`/`memory`/`table`. A section fed by one spelling would be short by every
  occurrence of the other. The three payload readers (`importedGlobal`, `importedMemory`,
  `importedTable`) are therefore shared *across* spellings rather than per-field — #82's one concept,
  one trigger — because the failure that matters is silent: a kind byte or a mutability flag right in
  one spelling and wrong in the other round-trips green for every vector that happens to use the
  spelling that works.

  **The kind byte is a mapping and not a cast, and probing it was worth more than asserting it.**
  `importKind` is ordered by the reference's *message* table (tag, global, memory, table, function)
  and the binary kind bytes run the other way (func 0x00 … tag 0x04) — exact reversals, so a
  `byte(kind)` writes a *legal* byte for every kind and the wrong one for four of five. Substituting
  the cast gives 16 fails and 8 passes, and the passes are **every memory row and only those**:
  memory is the fixed point of a five-element reversal. All 16 failures are the decoder rejecting a
  mismatched *payload*, not the want column disagreeing — so today's round trip catches this through
  the payload grammars happening to differ, which is a property rather than a guard. Two kinds with
  the same payload shape would swap silently. Recorded at the site, because the first draft of that
  comment claimed the accept-direction failure and the probe says otherwise.

  **The withdrawal check fires, and this is the arm the memory/table PR said did not exist yet.**
  That PR kept its retention-count check as a labelled tripwire it could not make fire, the frontier
  refusing every offending module before the loop was reached. The sugar spellings are the population
  it was waiting for: deleting `defineImport` from `inlineImportTail` now fails nine rows with `0
  imports retained, 1 parsed`. A declared-and-tracked deferral discharged by the work that gave it a
  subject.

  **An import descriptor can name a type index that does not exist yet** (`imports.wast:62`), so it
  is retained as a stage-2 thunk — the reference's own arm has the same shape, an outer function that
  binds at reduction and an inner one that produces the descriptor. `inline_functype_explicit` split
  into a value-returning `checkExplicit` for this, and its deferred path returns the **named index**
  rather than 0: 0 is a plausible wrong descriptor on the commonest corpus spelling, and no oracle
  reads it.

  **The independent witness is no longer near-vacuous: 101 joined against wabt's corpus, 101 agree
  byte for byte, 0 disagree — and 92 are longer than a bare preamble**, against 10 joined and one
  non-trivial agreement when the type section landed. Two thirds of the joined population are import
  modules, so the agreement is a claim about *this* section. The zero was earned twice: an interim
  reading of 99-agree-1-disagree was a probe whose ordinal counted `assert_malformed`'s inner quote
  modules, and before that 0-of-141 was a probe joining on `token.wast` where the corpus keys
  `token`. *Exactly zero on a join that used to work* is the same tell as exactly zero on an
  agreement, and neither reading was a finding about the encoder.

- **`internal/text/encode.go`: the memory and table section emitters, and a frontier that is now
  per-arm rather than per-field** ([#8](https://github.com/scttfrdmn/burroughs/issues/8), 0011 part
  2). `(memory i64 1 4)` and `(table 1 funcref)` encode; sections 4 and 5 join section 1 in id order,
  and `w.limits` is shared because both spend the same flags byte. The suite's encodable population
  goes **15 → 51** of 2150 parser-accepted text modules.

  **The retention question inverted here, and asking it honestly found two rejects the reference
  performs and we did not.** The type section grew out of retention the grammar already had
  (`inline_functype_explicit` needs it); `limits`, `addrtype`, and `reftype` retained *nothing*, so
  the emitter is their first consumer — the load-bearing spot 0006 warns about. Reading the values
  for real is what surfaced both gaps: `limits`' nats are `nat64` in the reference, so
  `(memory 18446744073709551616)` is `i64 constant out of range` *from the parser* and we accepted
  it; and a table's element type resolves in the reference's stage 2 (parser.mly:1341-1347), so
  `(table 1 (ref null $undefined))` is rejected upstream and accepted here. The first is fixed with
  the retention (grave [#112](https://github.com/scttfrdmn/burroughs/issues/112)); the second is one
  of **nine sites of one shape** — every valtype position that discards its type accepts an unknown
  one — and is filed as [#111](https://github.com/scttfrdmn/burroughs/issues/111) rather than swept
  in, because a nine-site shape is not an encoder's business. Neither has a suite vector: the accept
  direction §9 G-3 names. (The import section closed four more of #111's rows the same way, for the
  same reason; #111 stays open on the remaining six.)

  **The frontier is now per-arm, and the asymmetry is deliberate.** `(memory 1)` encodes while
  `(memory (data "abc"))` does not, though both dispatch on `memory` — so the record is made at the
  dispatch and *withdrawn* by the one arm that retained everything. Default is refusal: forgetting to
  withdraw costs a visible refusal, forgetting to record silently drops a section. The withdrawal is
  **identity-bound to its keyword token**, the same law as binding a CI verdict to its SHA — unkeyed,
  a `(memory 1)` after an unencodable `(func …)` withdraws the *func's* refusal and emits a module
  with the function dropped. That defect was installed and is now caught by four mixed-order rows;
  before them it passed every test in the file, because every row put its unencodable field first.

  **Nine defects installed, seven died, two were diagnosed — and the two are the findings.**

  - **Dropping the limits flags' bit 2 passed twice.** First because no row exercised an i64
    addrtype (fixed: `Memory64` on in the decode helper, four i64 rows). Then *again* with the rows
    present — because `binary.Limits` is `{Min, Max, HasMax}` and carries **no address type**, so a
    memory64 and a memory32 with the same minimum decode identically. The round trip is structurally
    blind to that bit, which is why the assertion had to become byte-level
    (`TestEncodeWritesTheAddressTypeFlagBit`, expected flags read from `encode.ml:187` rather than
    from our own output). A control that cannot fail is worth more as a discovery than as a control.
  - **The retention count check cannot currently be made to fire, and now says so at its site.**
    Four defects against it all passed: the frontier refuses the offending module *before*
    `encodableOrErr` reaches the loop, so it only runs on the population where the counts agree by
    construction. Kept as a labelled tripwire for an arm that does not exist yet — declared and
    tracked, per the `ErrTrailingData` ruling — rather than deleted or left claiming a guard it does
    not provide.

  **`unparam` deleted a function this PR wrote.** The retaining `tabletype`/`memorytype` returned
  values no caller read: the inline-import arms discard (an imported table is an `Import`), and the
  *defining* arms cannot call them at all, because the sugar branch needs a lookahead between
  `addrtype` and `limits`. Retention built for a hypothetical consumer is the
  generality-without-a-Go-shaped-consumer non-goal in its smallest costume; both are error-only again.
  Separately, `govet`'s `shadow` and `gocritic`'s `sloppyReassign` demanded opposite things in the two
  field functions — two curated linters describing one fact, that an error was held live across arms
  with no business in it. Resolved by extracting `memoryDataSugar`, `tableElemSugar`, and
  `importedExternType`, not by suppressing either.

  **What the independent witness can and cannot say, measured rather than assumed.** The wabt corpus
  joins **15** of the 51 encodable modules, and only **5** of those have a non-empty table or memory
  section — the other ten agree empty-to-empty, which is no agreement. Two do carry the i64 bit
  (`memory64-imports#12`/`#13`, both `01 70 04 0a`). The reason it is not deeper: wabt cannot parse 31
  suite files, among them `memory`, `memory64`, `table`, and `table64` — all four rejected on
  `(module definition …)`, a spec form wabt does not implement, which fails the whole file. **Every one
  of the 36 modules this PR made encodable is in a file wabt skipped.** The second opinion is absent
  from exactly the region the work is in, which is the scope-controls-to-the-space law pointed at a
  witness: an independent oracle has a blind spot too, and it is not the same one.

  **The frontier census is re-measured, and it turned a stated caution into a number.** Draining the
  two largest buckets (`memory` 467, `table` 251 — 718 modules) yielded **36** newly encodable and
  re-sorted the rest into the next field they contain (`func` +233, `elem` +157, `data` +115).
  Two thirds of the largest two buckets bought a 5% move. *Bucket size estimates the reward, not the
  job* — and the honest estimate for the next bucket is not its size but the count of modules whose
  *last* blocker it is, which a histogram of *first* blockers cannot answer. Stated as the limitation
  it is rather than re-measured into a nicer shape.

- **`internal/text/encode.go`: the type-section emitter — `EncodeModule`, the bridge's first module
  that runs** ([#8](https://github.com/scttfrdmn/burroughs/issues/8), 0011 part 2). Text in, binary
  image out, checked by decoding: `(module (type (func (param i32) (result i64))))` becomes
  `00 61 73 6d 01 00 00 00 01 06 01 60 01 7f 01 7e`. The return is bytes and an error, never a
  module value — 0011's surface rule holds, `binary.Module` stays the codebase's sole module
  authority, and `ReadModule` stays error-only.

  **It reads retention the grammar already had, and the direction of that dependency is the whole
  design.** `inline_functype_explicit` (parser.mly:245) compares inline signatures *structurally*,
  so `runDeferred` already resolves the type index space into `c.typeCtx` for a reason that predates
  any encoder. The emitter consumes that; it did not ask for it. The alternative — a retention pass
  shaped by what an encoder wants — designs the parser's memory from its only consumer's
  requirements, in the load-bearing spot 0006 rules on. Each further section is therefore a question
  about what the *grammar* needs, asked one section at a time.

  **A frontier is not a malformedness, so the emitter refuses rather than under-writes.** Every
  module holding a field or a type this cannot fully encode is declined by name — `cannot yet encode
  a (func …) field … (#8)` — and the refusal never borrows a spec word, because reporting
  "malformed" for a module the spec calls well-formed lies about the input to conceal a gap in the
  engine (the #5 ruling, pointed at an unfinished encoder instead of at a gate). Emitting a module
  with its function section silently dropped is the accept-direction defect no vector can see (§9
  G-3), which is the failure this bridge exists to be checked for. The frontier check reads the
  parse's own dispatch record, never the source text and never `space.count`: an import advances the
  count and an export binds nothing, so a count-based test could not tell `(func)` from
  `(import "" "" (func))` and would emit an export-only module with the export gone.

  **Three defects in the first draft, all found by building the controls rather than trusting the
  comments, and each a different class:**

  - **`(ref func)` was emitted as `0x70`** — the nullable byte for a non-nullable type, a wrong
    module that decodes clean. `funcref` and `(ref null func)` *are* the same type (the parser
    normalizes the abbreviation), which is what made the omission plausible; `{null: false}` needs
    GC's `0x64` prefix. Fixed by folding the encodability predicate and the encoding into one
    `valTypeByte(v) (byte, bool)`, so the frontier check and the emitter are one fact asked twice
    and cannot drift — one concept, one trigger.
  - **A control that passed with its defect installed**, the stillborn case, and interrogating *why*
    was worth more than the control. It asserted `len(typeCtx)` across `encode` on the stated
    reasoning that a second `runDeferred` drops the interned implicit types. Measured: `typeCtx`
    comes back **identical**, because the thunks re-intern the same signatures in the same order.
    What drifts is `types.count` — 1→2 on `(module (func))`, 2→4 on two distinct inline signatures.
    So a plausible mechanism written into a comment was replaced by a measured one, the assertion
    moved to the half that falsifies, and the table is still checked because the thunks'
    idempotence is *contingent on what they currently do*.
  - **`heapWat`'s general form was false while its comment asserted the property the code lacked** —
    the defect-stated-as-the-rule shape. A keyword *kind* is a token class, not a spelling:
    lower-casing yields that kind's own keyword for **96 of 173**, and `BINARY` → `binary` lexes to
    a *different* kind (`BIN`). Narrowed to the twelve absolute heap types where the derivation does
    hold, with the measurement at the site, a control ranging over `absoluteHeaptypes` so a
    thirteenth is covered the day it is added, and the frontier message changed to quote
    `Token.Text` (grave #36's rule for message text no oracle reads).

  **The round-trip table states each module's types independently, by hand, and that is what makes
  it a check.** An encoder and decoder that both had params and results backwards would round-trip
  perfectly; the `want` column is a second reading of the wat. All six controls were installed with
  the defect they name and watched to fail — params/results swap caught in both directions, the
  `0x70` defect caught, a LEB-instead-of-fixed version field caught, a dropped frontier check caught
  on ten subtests, a widened `absoluteHeaptypes` caught on both the count floor and
  `heapWat("NUMTYPE")`.

- **`internal/text/writer.go`: the encoder's byte layer — LEB128 and section framing**
  ([#8](https://github.com/scttfrdmn/burroughs/issues/8), 0011 part 2). The first engine lines of
  the text→binary bridge, whose veto 0011's appendix lifted once #98 gave `binary.Module`
  something to represent. The decoder is the authority for every encoding decision, so the writer
  is its inverse and is falsified by round trip — nothing here has an opinion about what a LEB is.

  **Minimal width, and that is a choice rather than the obvious reading.** `uleb` *accepts* any
  width up to the field budget — `80 80 80 80 10` is a legal five-byte small number, which
  `binary-leb128.wast` asserts — so an encoder is free here, and freedom is where a silent
  divergence lives. Minimality is checked against an independently derived width, because a
  padded LEB **round-trips correctly**: the round-trip half of the test passes on `80 80 80 80 00`
  and only the width assertion fails. Byte equality with wabt is deliberately *not* the criterion
  and must not become one — the corpus is an authority on which module the text denotes, not on
  encoding style, which is why the comparator compares `[]Instr`.

  **`sleb` is not `uleb` with a cast**, the writing-direction mirror of grave 0003's lesson: a
  correct signed encoder and a cast one agree on every non-negative value, so a round trip cannot
  tell them apart. The trap is `0x40` — as a final payload byte it sign-extends to **-64**, so
  stopping when the magnitude runs out writes 64 and means -64, a wrong value on valid input that
  no vector can see (§9 G-3). Watched to fail on exactly that: `s64(64) wrote 40, read back as
  -64`. Section lengths are **measured by splicing a nested buffer, never predicted**, so the
  both-signs size disagreement grave #34 documents is unreachable from this side; also watched to
  fail, on an off-by-one prediction.

- **`testdata/xcorpus`: an independently produced binary image of every suite text module that
  must succeed** ([#67](https://github.com/scttfrdmn/burroughs/issues/67),
  [#8](https://github.com/scttfrdmn/burroughs/issues/8)). #67's second half asks whether the
  encoder emits *the right module*, not merely a well-formed one, and our encoder compared
  against our decoder is one witness talking to itself — inadmissible per 0011's second
  appendix. So `wast2json` supplies the second opinion: **1954 modules, 312866 image bytes, cut
  from suite `de54fd27` by wabt 1.0.41**, committed with a provenance manifest. wabt touches the
  repo once as a generator and is never invoked by a test, never in CI, and never in the verdict
  path — the same posture the generated tables hold toward the reference interpreter.

  **`wast2json` is a second *splitter*, not just a second compiler**, which is why it is
  stronger evidence than compiling text we ourselves parsed: the two command sequences are
  independently derived and have to be joined. **The join is an ordinal, not a line, and that
  was measured rather than assumed** — in `comments.wast` we report the opening `(`'s line and
  wabt reports the `module` keyword's, one apart, and both readings are defensible. A key two
  readers legitimately disagree on is not a key. Line is retained as a *corroborating* signal
  precisely because it is unfit to key on (0014's correction, grave #106): 1953 of our 2238
  module commands join, one pair exceeds a ±2 window, and **every unjoined command lives in a
  file the manifest names as skipped** — the gap is accounted for, not merely stated.

  **The corpus raises the accept population from 74 modules / 18 instructions to 1262 accepted
  modules**, which is the oracle the accept direction has never had: all 4162 green vectors are
  rejections, so contract §9 G-3 defects score green by construction. It paid for itself on the
  first run — see #109 under Fixed.

  **Flags are the tracked union, enumerated to match `Features`, and this replaced
  `--enable-all`.** A feature flag does not only decide what wabt accepts; measured over the
  whole suite, three flags *re-encode* modules the standard grammar already describes —
  `function-references` 105 (element segments in expression form), `compact-imports` 45 (an
  import as `00 7f`), `multi-memory` 2. Under `--enable-all` the corpus disagreed with our
  decoder in 58 × `malformed import kind: 0x7f`, which was the **generator** handing over a
  proposal encoding: a cross-check corpus that quietly re-encodes its subject is the
  wrong-module failure #67 exists to detect, arriving through the instrument. The criterion is
  not "does it re-encode" — `function-references` re-encodes the most and *is* tracked (0008
  folds it into the GC gate) — it is contract §9 G-2, and the six omitted flags buy **one**
  module between them. `TestFeatureFlagsCoverTheTrackedGates` reflects over `Features` so a
  ninth gate fails there rather than silently narrowing the corpus.

- **`internal/text/opcodes.go`: the mnemonic→opcode table, generated as a *join* of the two
  tables that already exist** ([#8](https://github.com/scttfrdmn/burroughs/issues/8),
  decision 0014). #8's encoder must answer "which byte does `i32.add` emit?" 494 times.
  Neither authority states that: `parser.mly` and `lexer.mll` name the reference's
  *constructor* (`i32_add`), and `optable.go` — itself machine-derived from the same
  revision's `decode.ml` — says which byte that constructor decodes from. So
  `internal/gen/opgen` reads no opcodes at all; it reads the **link** between two committed
  artifacts and emits their join. **494 rows, 58 named by the grammar's semantic actions, 436
  by the lexer's token payloads, 95 keywords with no opcode to have, 3 ambiguous.** Neither
  hand-written (this repo's measured hand-transcription rate is 7 wrong in 12, #37) nor a
  third extractor (which would make two places know `i32.add` is 0x6a with no derivation
  between them — 0006's drift risk).

  **The three ambiguous mnemonics are emitted, not resolved.** `select` (0x1b bare / 0x1c
  typed), `ref.test` and `ref.cast` (0xfb 0x14/0x15 and 0x16/0x17) are distinguished by the
  reference on *what follows the mnemonic*, so a map keyed on the mnemonic alone returns one
  of two and is wrong on the other spelling — with no board consequence, since both spellings
  decode clean. `ambiguousOpcodes` carries both codes and the encoder chooses on the operand.

  **Product work under the accept-direction exception, and three defects paid for the claim.**
  Every spec vector bearing on this table is a module the suite expects to *work*, so a wrong
  row emits a different instruction than the text denotes and scores green (contract §9 G-3).
  All three were found by *printing what the reader returned for named inputs*, none was
  findable from the board, and each is a distinct class:

  - **25 wrapped lexer arms silently absorbed** — 411 rows where 436 were measured. The head
    regexp required `-> TOKEN` on one line, true of 564 arms and false of the `const` family
    and the `v128.*_lane`/`_splat` group, which wrap. No error and no unrecognized arm: the
    under-matching trigger of #78/#82, which produces *no finding rather than a wrong one*.
    The floors did not catch it, because 411 clears a floor of 350 — **floors bound the
    catastrophic case, and only an exact count sees a 6% silent loss.**
  - **The grammar reader was scoped to `plaininstr`**, one of **five** instruction-building
    productions, so `select`, `block`, `loop`, `if`, `call_indirect`, `return_call_indirect`
    and `try_table` — every control-flow construct in the language — were named by neither
    authority and joined to nothing. See 0014's Correction: the ADR's premise was *measured
    with a probe scoped to the same production the reader read*, so the gap came out at
    exactly 0 because neither could see it. **A premise measured over the same sample the
    code reads is not a premise, it is an echo.**
  - **`constructorIn` was fed the arm's symbol list, not just its action**, so
    `| LOOP labeling_opt block END labeling_end_opt` resolved `LOOP` to the *nonterminal*
    `block` — which is also a constructor the opcode table holds. **`loop` would have encoded
    as 0x02.**

  **`Extract`'s partition check was a no-op**, found by writing its falsification test and
  watching it *not* fail: `byGrammar` is keyed per kind and `byLexer` per keyword, so
  `byLexer[kind]` asked whether some keyword is spelled `BINARY` — never true, uppercase.
  It read as a partition check and could not fire on any input. The corrected gap control uses
  a detector drawn from **outside both readers** (the mnemonic's own spelling, rejected as a
  join key and therefore fit as a second opinion), because asking whether every kind the join
  resolved was resolved is a tautology — *the* tautology that hid the seven.

  Floors are **per-partition, not one total**: a total of 400 passes on the lexer's 436 alone
  if the grammar side finds zero, which is an empty half absorbed by a full one — the vacuity
  law with a partner to hide behind. `make opcodes-text` regenerates, `make opcodes-text-drift`
  asserts agreement with the pin, and `Emit` refuses an empty table because an empty map
  compiles, formats, and reads as "no mnemonic encodes to anything".

- **The internal form: the decoder now retains modules instead of only judging them**
  ([#7](https://github.com/scttfrdmn/burroughs/issues/7), decision 0002). Before this,
  nothing in the codebase could represent a module — 28 of the 29 `decode*` functions
  returned a bare `error`, and `Module` was `{Version, Sections}` of payloads aliasing the
  input image, which is a verdict about bytes rather than a program. That single missing
  artifact was behind 93.6% of the board: #7 execution, #9 validation, #67's half-2
  comparator, and the text encoder's target all waited on it.

  `internal/binary/module.go` is 0002's `[]Instr` form — fixed-width instructions with
  pre-decoded immediates in two 64-bit words, plus types, functions, globals, imports,
  exports, tables and memories. It is grown out of the **decoder**, which has a 4162-vector
  conformance record, rather than out of the text parser, which has never accepted a module
  (0006's load-bearing-spot rule; 0011's appended ruling sequences the form before the
  encoder).

  **The producer seam 0002 left open is resolved to the descent**, on `sawDataRef`'s
  precedent: one grammar, one traversal, state on the `Decoder`. A second pass would be a
  second grammar over the same bytes. The cost is stated rather than hidden — `decode*`
  signatures stay error-only, so the shape those 4162 rejection vectors are proven against
  does not change, and retention lands on the decoder's in-progress module. Retention is
  per-decode state, reset at `DecodeModule`'s top rather than its bottom so the zero-value
  path is covered, and `mod()` creates the module lazily so a production driven below
  `DecodeModule` — every unit test does this — cannot make its own correctness depend on its
  caller.

- **Three accept-direction controls, because the suite scores this whole direction green by
  construction** ([#7](https://github.com/scttfrdmn/burroughs/issues/7)). Every one of the
  decoder's 4162 green vectors is a *rejection*, so a decoder that recognizes every malformed
  module correctly and retains **nothing** scores 4162 too. That is contract §9 G-3 in its
  purest form, and it is why these are product work rather than overhead.
  `internal/spec/retention_test.go` asserts the form over the real accept population — **74
  accepted modules, 218 sections, 27 bound functions, 47 instructions**, measured and printed
  every run, with four vacuity floors because the interesting assertions have much smaller
  populations than the outer loop. `TestIsRefPartitionsTheValTypeSpace` pins 0002's
  reference/numeric split, whose failure mode is a pointer the Go collector cannot trace.
  Domains are derived by AST walk over the declared constants, never enumerated, so a form
  added upstream fails rather than defaulting.

- **A cited test name must resolve, and the sweep is now a control rather than a habit**
  ([#93](https://github.com/scttfrdmn/burroughs/issues/93), ruling on
  [#91](https://github.com/scttfrdmn/burroughs/issues/91)).
  `TestFixtureProvenance` machine-checks `<file>.wast:N` citations; a comment claiming "pinned by
  <some test>" was the same kind of claim one class over with nothing checking it, and #88's hand
  sweep found five stale ones. `TestEveryCitedTestNameResolves` reads every comment in the tree and
  requires each cited test, fuzz, or benchmark name to name a function that exists — **476
  citations, 257 distinct names, against 277 definitions** — unless the sentence marks it as
  historical.

  **Made a control on measured evidence, not on principle.** Effectiveness was measured against
  `e4bfd62^` — the tree immediately *before* that hand sweep, where all five defects were live —
  rather than the repaired tree: **5/5 caught, 0 missed, 1 structurally-excludable false positive**,
  and 5/5 correctly exempted once fixed. Recall measured against history is what made the
  measurement worth anything: the first exemption draft passed flawlessly on `main` while *excusing
  two of the five real defects* on the pre-sweep tree, because a control with nothing left to catch
  cannot distinguish a working exemption from a leaking one.

  The exemption for historical references ("it was …", "previously cited …") is scoped
  **per-sentence**, since the block-scoped version excused live present-tense claims sitting in long
  comments that happened to contain a past-tense word elsewhere: *an exemption scoped more widely
  than the claim it excuses will excuse claims it never examined.* The one false positive is
  excluded by **declaration shape** (`func Name(` is a code sample, not a reference) rather than by
  ignoring backticks, because seven real citations live inside backticks and discarding a whole
  citation style to kill one false positive is the overfitting failure pointed at a control.

- **A gate census: every accepted opcode-table arm and the gate governing it, checked arm by arm**
  ([#91](https://github.com/scttfrdmn/burroughs/issues/91), decision 0012). `gatedOpcodes` holds
  whole-region entries — `{prefix: 0xfb, lo: 0x00, hi: 0xff, gate: gateGC}` — which is a claim about
  every arm the region will *ever* hold, and both existing controls walked the **mapping**, so an arm
  arriving upstream inside a region range would inherit its gate with every control still green.
  `internal/binary/testdata/gate-census.txt` now records all **499 accepted arms** (298 gated — SIMD
  236, GC 37, RelaxedSIMD 20, ExceptionHandling 3, TailCall 2 — and 201 ungated), regenerated by
  `make gate-census` and exact-compared, so a new arm is a build failure demanding classification.
  Exact rather than slack-bounded because both inputs are committed artifacts: unlike the board
  counts below, this number cannot move because upstream moved. *The strongest control the inputs
  admit, at each site.*

  **The census covers the ungated arms too, and that widening is the substance.** #91 framed the
  risk as an arm *inheriting* a gate; the mirror is an arm arriving with **no** gate, decoding clean
  with every gate off, which is [#48](https://github.com/scttfrdmn/burroughs/issues/48) itself rather
  than a cousin of it. A gated-arms-only census would have been scoped to the population today's
  risk lives in. `0xfc` is the case that proves it: bulk-memory and the non-trapping conversions,
  entirely core Wasm 2.0 at the pin, so every one of its arms correctly reads `-` — an answer a
  narrower census could not express. Two arms needed a label at all (`0x05`/`0x0b`, ELSE and END,
  the table's `reason` rows) and rendered a **blank fourth column** that a whitespace-delimited
  golden file swallows silently; found by reading the generated file rather than trusting it, since
  *a golden file is its own expected value*, and now pinned by `TestCensusRowsAreWellFormed`.

- **Board bounds assert their own slack, so a bound cannot silently drift from what it bounds**
  ([#87](https://github.com/scttfrdmn/burroughs/issues/87), decision 0013). `allOnPassFloor` was
  **798 against an actual 4178** and had been since #56, fifteen commits back — falsifiable in the
  ordinary sense and still decoration, because the defect was not in the assertion but in the
  **unasserted distance between the assertion and the measurement**. Every bound now routes through
  `boardBound`, which checks the constraint *and* the distance, and `TestEveryBoardBoundIsChecked`
  reads the package AST to require it — the `TestEverySkipSiteIsLicensed` pattern, because a rule
  saying "all of these go through one door" needs something asserting that they do.

  **The control falsified its own decision record within minutes of existing: the space is eight
  bounds, not four.** 0013 was drafted naming `passFloor`, `allOnPassFloor`, `binaryFailCeiling`,
  `textFailCeiling`; the first run also found `unsupportedCeiling`, `unimplementedCeiling`,
  `totalFloor`, `filesFloor`. The ADR was corrected and its body preserved, and the trigger caught
  what the author's memory missed precisely because it keys on the naming convention rather than a
  list. Two design consequences followed: **a ceiling goes stale in the opposite direction** —
  `unsupportedCeiling` drains as capabilities land, so it rots *by working*, where the floor needed
  fifteen commits of neglect — and **a third kind exists**, the vacuity floor
  (`totalFloor` 2000/2143, `filesFloor` 230/242), whose looseness is its *function*; slack-checking
  those would fire on controls working as designed, so they route through a `vacuityBound` kind that
  names the exemption rather than sitting outside the door. Board unchanged: **4162 / 0** default,
  **4178 / 0 / 0** all-on — these are controls, not features.

  **The slack number's justification was itself wrong on first writing, which is the same defect one
  level up.** The draft argued 250 from "ordinary progress" and quoted two `passFloor` steps over
  250; the actual steps are +636, +209, +313, +12, +39, +2130, +37, +2, +1 — *three*, one of them a
  middling stratum — a figure quoted from the remembered shape rather than the subtraction, inside
  the decision about numbers going stale. Worse, it answered a question the mechanism does not ask:
  a PR that moves the board and raises the bound together leaves a distance of **zero**, so no step
  size trips this. What the slack actually absorbs is **corpus drift between fetches**, the suite
  being unpinned ([#42](https://github.com/scttfrdmn/burroughs/issues/42)). 250 stands; the reasoning
  is corrected in place, and #42 is now the last remaining reason these counts are not exact.

- **Symbolic label resolution, which closes the `unknown label` bucket and finishes #64**
  ([#80](https://github.com/scttfrdmn/burroughs/issues/80)). `labelSpace` in `context.go` — a stack
  of the enclosing blocks' names, innermost last — with `lookupLabel` reporting `unknown label  $l`
  (two spaces, reproducing the reference's `lookup "label "` at parser.mly:161 rather than tidying
  it), wired at the five `plaininstr` arms that take one (`br`, `br_if`, `br_table`, `br_on_null`,
  `br_on_cast`) and at both handler productions. **Board 4159 → 4161 pass, 4 → 2 fail**;
  `token.wast` 59/61 → 61/61. The residue was quoted here as 1 lexer vector and 1 decoder vector,
  **neither of them the text parser's** — half wrong, and the correction is
  [#83](https://github.com/scttfrdmn/burroughs/issues/83) below.

  **The scope was decided by a measurement, and the measurement is what kept the change small.**
  Read literally the reference resolves *every* symbolic index in the parser — the lookup category
  is a parameter of `idx` (:487-489), supplied by all 83 `plaininstr` arms — which would have made
  this a job spanning nine index spaces. Matching every `unknown *` vector's module body for a
  symbolic name in a use position its own module never binds returns **exactly two rows across all
  253 files**, both labels. Labels are the one space whose scope is *lexical*, so they resolve where
  they are read; the other spaces need #64's deferred phase, and the 13 `assert_invalid "unknown
  label"` vectors are `(br 1)`-shaped — `idx`'s NAT arm is `nat32 $1`, a width check with no lookup,
  so they are **validation's** and answering them here would be overfitting to the oracle (§9 G-3).

  **The controls are scoped to the mechanism, because the two vectors are the same shape.** Both are
  a `br_table` whose first index is unbound inside a func holding one named block, so a fix that
  resolved nothing and errored on any `$name` reaching `br_table` scores 2/2. `label_test.go` pins
  the four facts no `assert_malformed` states — the cleared space at `enter_func`, the anonymous
  level every unnamed block occupies, `catch`'s target resolving in the **outer** context (`$4 c
  label`, `c` not `c'`, identical in both handler productions), and `br_table` resolving every
  member of its tail — with three drift controls read from `parser.mly`'s semantic *actions*: the
  category is not in the grammar at all. Census: 14 label lookups in 3 of 137 productions.
  `productionBody` was carved out of `productionArms` to make the actions readable.
- `internal/spec`: the `.wast` harness, phase 1 (decision 0003) — a pure-Go
  s-expression reader, wast string-literal decoding, the `(module binary ...)`
  form, and `assert_malformed` matched by substring. No wabt, no non-Go tool
  in the conformance loop. **First suite numbers: `binary.wast` 49/127 pass,
  78 fail, 0 unsupported.**
- Boards bucket failures by expected spec error string, ordered largest first,
  so the suite schedules the decoder work: the biggest bucket is the next
  issue and a bucket reaching zero is a PR's measure of done. Pinned by a test
  that asserts the ordering property, not just the counts.
- Parser-robustness sweep: all 257 upstream `.wast` files must parse, even the
  ones phase 1 cannot execute. A parse error and an unsupported command are
  different numbers, and conflating them would hide the real unsupported count.
- Semantic Versioning 2.0.0 and Keep a Changelog 1.1.0 adopted, recorded in
  `CLAUDE.md` so the convention survives session boundaries.
- Decision 0004: engine SemVer and contract version are independent, joined
  by a per-release contract-version statement. **Resolves contract §10.7.**
- GitHub as the project tracker: milestones are the phase ladder, issues
  replace the in-repo queues, `label:type:grave` is the graveyard, and PR
  descriptions carry the session-report format. `PROGRESS.md` and
  `docs/reports/` retired (archived in #1).
- CI runs on x86-64 (TSO) and AArch64 (weakly ordered) — build, vet, test,
  `-race`, and `gofmt`. Green on both from the first push.
- Two disciplines ratified into `CLAUDE.md`: *unreachability is a grave only
  when it's silent — declared and tracked, it's a TODO with an audit trail*,
  and *bucketed failures are the work plan*.
- Two disciplines ratified into `CLAUDE.md` from #28: **a stateful instrument
  measures history until its state is controlled** — *fuzzing is stateful, so a
  measurement that doesn't clear the corpus is measuring the last run*, with the
  sibling law that a fuzzer's two halves fail independently and must be certified
  independently (seed-replay by a reintroduced known defect, exploration by a
  mutation-only needle no seed can reach) — and **a design debt is discharged by a
  tripwire, never by an intention**, the declared-and-tracked ruling pointed at
  architecture instead of at a constant.
- Three more disciplines ratified from this PR's review. **A control scoped to the
  current sample inherits the current blind spot; scope controls to the space** —
  the general form of #33's widening past the eight opcodes the reader needs, and
  the overfitting law (§9 G-3) turned on the controls rather than the engine:
  *derive the domain, never enumerate it.* **A ruling retroactively falsifies prose
  written before it**, so accepting a ruling includes sweeping for the sentences it
  orphaned — *truth has a maintenance cost*, and a comment citing a tracking
  location that no longer exists is the drifted-citation defect in other clothes.
  And **second-order honesty: apply the discipline to its own output** — catching a
  figure as fiction earns nothing if its replacement carries the same
  overconfidence; n=1 cannot separate a property of an environment from an accident
  of one scheduling.
- Grave #34's lesson ratified into `CLAUDE.md` as a discipline: **a test named for a
  partition must be checked against the partition, not against its own case labels** —
  the coverage cousin of *a green that survives the bug it names*, and the failure
  mode where the pass count is right and the coverage is wrong. Its corollary is the
  mechanism, and is why the defect was invisible: **when a partition's members share
  an error value, `errors.Is` is not a partition check**, so the discriminating field
  gets asserted or every member scores as every other. The check is to print what the
  code actually returns for each case rather than read the labels.
- `internal/binary`: the `constexpr` production and the three section grammars that
  need it — global, element, and data (#25). A constant expression is not
  length-prefixed; its extent is discovered by reading instructions to the END
  opcode, which is why those sections could not be decoded until the decoder knew
  opcodes at all. **`binary.wast` 104/127 → 114/127; `binary-leb128.wast` 57/91 →
  73/91; phase 1 total 707 → 733 pass.** `section size mismatch` 5→1, `unexpected
  end of section or function` 6→3, `integer too large` 22→12 in leb128.
- A signed LEB128 reader (`sleb`/`s32`/`s64`), which is **not `uleb` with a cast**:
  it sign-extends, and its range check is *two-sided* — the out-of-width bits of the
  last byte must all match the sign rather than all be zero. That is the bulk of the
  `binary-leb128.wast` gain, since the const-expr immediates are where signed values
  first appear.
- Element-segment flags decoded as a **bitfield**, with the type-field presence rule
  (`flags&(passive|explicit) != 0`) derived from every element-segment encoding the
  suite contains rather than patched per failing vector. Two cheaper rules each fit
  all but one row; the table of six encodings and which row kills which rule is in
  the code, and all six are pinned as fixtures.
- `TestFixtureProvenance` now verifies **fragment citations** — a `<file>.wast:N`
  naming one source line inside a `(module binary ...)`, which is what a
  reader-level test needs when the unit under test is a segment grammar rather than
  a whole module. The bytes are compared against the `"\hh"` escapes on that line.
  It caught two of #25's seven fragment citations pointing several lines off the
  moment it was written. The alternative — marking them `synthetic` — would have
  declared a transcription unverifiable when a transcription is precisely the hazard
  the file exists for.
- Decision 0005: tooling gates. Quality is enforced by pinned tools wired into
  CI rather than left to habit, because a convention that depends on
  remembering decays across session boundaries.
- Decision 0006: the const-expression opcode table is **not** shared with the
  interpreter yet — `internal/binary` gets its own `constexpr` reader. Sharing from
  the start would shape #7's central structure from the decoder's requirements
  before a second consumer exists, and `internal/interp` currently holds only a
  benchmark, so "shared" would be shared with nobody. Unblocks #25.
- The accepted form of 0006 carries a **pre-registered agreement test** (#33) as part
  of #7's definition of done: when the interpreter's opcode table lands, a test
  asserts its const-legal subset and the decoder's reader agree over the *full*
  opcode space — membership, immediate extent, and rejection. The design debt 0006
  accepts is only "convertible into a failing test" if the conversion is an
  obligation with a tripwire rather than an intention, so it is filed, milestoned,
  and required to be falsified before it is trusted.
- `tools/go.mod` pins every quality tool via `tool` directives —
  `golangci-lint` v2.12.2, `govulncheck`, `deadcode`, `benchstat` — so the
  versions are repo state and a green board means the same thing on a laptop
  and in CI. The engine's own `go.mod` stays dependency-free.
- `.golangci.yml`: a curated linter set, each enable carrying its rationale,
  with gofumpt as the formatter. Never `enable-all` — lint noise is its own
  kind of dishonest board.
- Four native fuzz targets: `FuzzDecodeModule` (total behaviour — a module or a
  *declared* error, never a panic), `FuzzULEB` (width invariant at 32 and 64
  bits), `FuzzWastLexer`, and `FuzzParseNodeProgress` (a successful parse
  consumes ≥1 byte). Corpora seed from the spec suite at run time — 809 module
  images from 257 files, no transcription step.
- `TestFixtureProvenance` machine-checks the `binary.wast:N` citations in
  hand-written fixtures against the suite: 62 cited vectors verified, 7
  declared synthetic. `TestEveryFixtureFileIsChecked` guards the guard — the
  file list it reads is hand-maintained, so a new fixture file nobody registers
  would be silently unchecked; the set is now derived from disk and compared
  both ways.
- `make check` as the single local gate mirroring CI, plus `make fuzz`,
  `make bench`, `make vuln`, `make tidy`. CI gains lint, vuln, fuzz-smoke, and
  `go mod tidy` jobs; a weekly `nightly.yml` runs 10-minute fuzz per target and
  re-runs `govulncheck` against moving vulnerability data.
- `-shuffle=on` on all test runs: test order is never load-bearing.
- Two disciplines from the section-order work: **the spec is the objective
  function; the suite samples it** — the oracle answers what it is asked and does
  not define correctness, so pass count is never bought with a check that is wrong
  about inputs the suite has no vector for — and **a verdict without an identity
  check is hearsay**, which is why CI results are bound to the SHA they judge.
- Three disciplines added to `CLAUDE.md` from this work: *parsers prove progress,
  they don't assume it*, *fixtures cite the suite, and the citations are
  checked*, and *verdict channel and mechanism channel are different instruments*
  — an exit code can't tell you why, and output can't tell you whether.
- Two rulings recorded in decision 0005: the `deadcode` allowlist becomes a file
  with a reason per entry at its third entry (an unexplained allowlist entry is a
  suppression wearing a disguise), and fuzz crashers are committed — the
  never-commit rule was about provenance, and a crasher is a grave's reproducer
  this project authored.

- Decoder: **section order and uniqueness enforced**, and `ErrTrailingData` is
  reachable at last — it had been declared-and-tracked since the genesis commit.
  Order and duplicates are one predicate: section ranks must strictly increase,
  so a repeated section fails for the same reason a misordered one does. The rank
  table is deliberately **not** section-id order — the data count section is id 12
  but the grammar places it before code (id 10), so ranking by id accepts a module
  `binary.wast:1194` says is malformed. `binary.wast` **49/127 → 84/127**.
- Decoder: malformed section ids rejected. The lookup that ranks a section is the
  lookup that validates it, so ordering and id-legality are one table.
- Decoder: cross-section count agreements — function/code body counts, and the
  data count section against the data section. An absent section counts as zero,
  which is what makes "one present, one absent" fall out of the same comparison
  rather than needing its own case.

- Decoder: **section payload grammars** — the decoder stops taking a section's
  declared size on trust and descends into type, import, function, table, memory,
  export, start, data count, and custom sections. `binary.wast` **84/127 →
  104/127**; phase 1 total **136 → 179 pass**. `binary0.wast` and `custom.wast`
  reach **7/7** and **11/11**.
- The declared-size check is **one comparison with two signs**, and the spec text
  is selected by the direction of the inequality: grammar wanting more than
  declared is *unexpected end of section or function*, grammar finishing with
  declared bytes left over is *section size mismatch*. A sign error would swap the
  two messages while keeping the pass count superficially plausible, so both
  directions are pinned independently.
- Payload grammars are bounded by the **image**, not the section. Over-reading
  past a section boundary is required rather than tolerated: `binary.wast:754`
  expects *length out of bounds* because the grammar reads the next section's id
  byte as a name length, and `binary.wast:92`'s own comment documents the
  reference interpreter consuming a data section's `\0b` as an END instruction.
  The custom section is the sole exception — its tail is opaque bytes, so no later
  grammar step exists to catch an over-read.
- A `Features` config struct on `Decoder` gates per-section acceptance for
  exception handling, SIMD, threads, and memory64. The zero value is v0's
  posture: every 3.0 gate present and off (contract §9). The structural id-range
  check stays gate-blind.
- Harness: a **third verdict, `gated`**, for vectors the engine declined because a
  feature gate is off. Its own board column, checked before the substring match so
  a gate error containing the expected text cannot buy a pass, and pinned by
  `TestGatedVectors` — an enumerated allowlist with a feature named per entry, since
  a third verdict is otherwise a way to make a board look better by moving
  failures into it. `TestVerdictsPartitionCommands` holds the arithmetic: every
  command lands in exactly one verdict.
- Doctrine ratified into `CLAUDE.md`: **gates never manufacture malformedness.**
  *Malformed* belongs to the grammar of the tracked union (contract §9), so the
  tag section (id 13) is well-formed and ≥14 is malformed regardless of any gate.
  A gate-off engine must *reject* a gated construct — accept-and-ignore silently
  changes the module's semantics — but with a feature-named error, never a spoofed
  spec string. Asserted directly, because a gate that impersonated a spec string
  would score itself green for rejecting a module the spec calls well-formed, and
  no pass count can see that.
- Buckets closed by this work: *malformed limits flags* 7 → 0, *malformed import
  kind* 6 → 0, *length out of bounds* 1 → 0, and `custom.wast`'s *unexpected end*
  2 → 0, all added to `TestClosedBuckets`. Two buckets drained without closing —
  *unexpected end of section or function* 9 → 6 and *section size mismatch* 8 → 5 —
  and earn no entry; their remainder needs the code, global, and element grammars.

- **CI `conformance` job — the suite is the oracle, so CI now actually runs it.**
  Two lanes sharing one suite fetch: default features (every 3.0 gate off, v0's
  posture) for the board numbers, regression floor, closed buckets, and the gated
  allowlist; and **all tracked gates on, where the gated count must be zero**. The
  second is the structural control on the third verdict — under full features every
  vector answers on the merits, so a vector parked in `gated` on the default board
  is simultaneously being honestly *failed* in the all-on lane and stays failed
  until its feature actually works. A deferral that cannot become a disappearance.
  `make conformance` is the local mirror.
- `TestAllGatesOnLeavesNothingGated` discovers the gate set by **reflection over
  `Features`**, not from an enumerated literal: adding a fifth gate and forgetting
  to list it would leave the all-on lane running with that gate off, letting a
  vector hide in `gated` in *both* lanes — the exact failure the lane prevents. A
  non-bool field fails loudly, because "I could not turn this on" must never read
  as "it is on". Both failure modes were verified by deliberately breaking them.

- Decoder: **name validation** — a `name` must be well-formed UTF-8. Phase 1 total
  **179 → 707 pass** (57 fail, 0 unsupported, 2 gated), closing all three
  byte-string `utf8-*.wast` files at **176/176** each. The largest single bucket in
  the corpus, and the check is nine lines.
- The rule is `utf8.Valid`, which *is* the spec's side condition (`name ::=
  b*:vec(byte)` with `b* = utf8(name)`) — not a list of rejected byte patterns
  derived from the suite. The suite enumerates 176 violations per file, and a check
  written from that enumeration would pass every vector while being wrong about
  byte sequences the suite has no vector for. The stdlib predicate was measured
  against all 528 executable vectors as *evidence it is implemented correctly*,
  never as the source of the rule. Unit tests are organised by violation **class**
  — overlong forms, unpaired surrogates, past U+10FFFF, truncations, 5- and 6-byte
  sequences — with the accept direction pinned just as hard, because "reject
  everything" would score 528/528 while making the decoder reject valid modules.
- The predicate is on `name()`, not `byteVec()`: a data segment's contents are
  `vec(byte)` with no encoding constraint, so the cheap generalisation would pass
  every vector and reject modules the spec accepts. `utf8-invalid-encoding.wast`
  stays off the board — its 176 forms are `(module quote ...)` text-format modules
  phase 1 cannot execute, and they belong to #8.
- The `//nolint:unparam` on `byteVec` is **removed with its purpose fulfilled**,
  which is what a declared-and-tracked suppression is supposed to look like at the
  end. `name()` returns only an error: the bytes are consumed by the predicate, so
  the same classification question gets the opposite answer on different facts.
- `phase1Files` is one definition instead of four copies. Adding the utf8 files to
  the board list alone would have left the gated allowlist, the verdict partition,
  and the bucket-ordering property scoped to a narrower corpus than the board
  reports — three controls quietly watching less than the number beside them.
  `TestClosedBuckets` keys are pinned as a subset.

- **`internal/testenv` and a skip-forbidden CI mode** — the class behind the
  passing-by-not-running grave, closed rather than just its instance. Every skip
  license in the tree routes through one helper, each names what it licenses
  (local dev on a clone without `make spec-tests`), and `BURROUGHS_NO_SKIP=1`
  revokes them all: `requireSuite` fails instead of skipping, and the two fuzz
  seeders that silently *degraded* to literal seeds — the same shape one step
  quieter, since nothing but an `f.Log` said the corpus was missing — fail too.
  The flag is set **workflow-wide** in CI, not per job: a job added next month
  inherits strictness rather than needing someone to remember, so the `build`
  job now vendors the suite because it must.
- `TestEverySkipSiteIsLicensed` reads the AST for `Skip`/`Skipf`/`SkipNow` across
  the tree and requires an inventory entry per site, both directions. Without it
  the mechanism would have the shape it exists to forbid — a rule enforcing all
  skips route through `testenv` while nothing asserted that they do. The tree has
  exactly one skip site, which is what makes one env var able to revoke them all.
- `make strict` mirrors the CI mode locally, and the harness's own controls are
  pinned from both sides: present corpus, absent corpus, *partial* corpus (three
  files satisfy an `os.Stat` and then yield a board over three files), and the
  flag on and off. Probing the inventory control with a deliberate unlicensed skip
  caught a real defect in the strictness helper — it reported a fail *and* a skip,
  because `Fatalf`-then-`Skip` leans on `runtime.Goexit` to not return.

- **Decision 0007 (proposed) and `make spec-ref`: the reference interpreter as the
  opcode table's authority.** The table #39 needs is ~530 immediate-shape facts, and
  every suite vector bearing on it is `assert_malformed` — so a table that wrongly
  *rejects* a valid opcode is invisible on the board by construction (contract §9
  G-3). The principle is stamped and normative: **the table is machine-derived from,
  or machine-checked against, the reference; hand-trusted is not on the menu.** The
  ADR argues only the mechanism, and recommends a pure-Go extraction of
  `decode.ml`'s arms — measured as tractable (504 arms, 368 with no immediates, a
  16-reader immediate vocabulary, 4 genuinely irregular arms) and preferred because
  no OCaml toolchain exists on the dev box or is assumed on runners.
  Mechanism endorsed with **four conditions**, inherited by #39 as definition-of-done:
  the extractor is born falsified *including a vacuity control* — an extraction
  finding zero arms must fail, since a silently broken parser otherwise emits an empty
  table and a drift check comparing empty to empty agrees perfectly; the four
  irregular arms are cited or derived, being few earning no exemption from provenance;
  the committed table carries a generation header (reference SHA, extractor version);
  and CI asserts table equality against the pinned source, which is affordable
  *because* the mechanism needs no toolchain.
- `scripts/fetch-spec-ref.sh` vendors `WebAssembly/spec` **pinned by SHA**, verifying
  the pin and the presence of `decode.ml` after every path rather than trusting the
  fetch. The contrast with the unpinned suite fetch is stated at the site: the suite
  is the thing being *reported*, so its drift moves the board loudly, while the
  reference is an *input* to a generated table, where drift would arrive as a diff
  nobody ordered. Falsified on all four paths, and the missing-file probe found a
  real defect — the already-at-the-right-rev path returned early and skipped both
  assertions, the precondition excusing the check that polices it.
- `internal/binary/optable.go`: the opcode immediate-shape table, **machine-extracted
  from the reference interpreter** and committed with its provenance header (authority,
  revision, extractor version, arm count). 542 arms — 218 single-byte including the
  three prefix escapes, 31 `0xfb`, 18 `0xfc`, 275 `0xfd`. `make opcodes` regenerates,
  `make opcode-drift` asserts the committed file still agrees with the pinned source
  and refuses to run without it. Every spec vector bearing on this table is
  `assert_malformed`, so a table that wrongly *rejects* a valid opcode is invisible on
  the board by construction (contract §9 G-3); the extractor is the accept direction's
  only witness.
- The extractor errors on any arm it cannot read, never skips one, and carries the
  vacuity control 0007's condition 1 required: per-region arm floors, so an extraction
  finding nothing fails instead of producing an empty table that a drift check would
  find in perfect agreement with an empty committed table (grave #29's shape relocated
  into a code generator). Falsified per mechanism rather than per sentinel — the locate
  check and the floors share `ErrVacuous`, and two of four subtests stayed green until
  the assertion moved to the discriminating message text (#34's lesson).
- The agreement test decision 0006 pre-registered (#33), landing in the same PR as the
  table it cross-checks. Seven controls over **all 256 single-byte opcodes** and every
  prefix region, derived from the table rather than enumerated: immediate-vocabulary
  totality, the const set as a subset of the authority, a differential extent
  comparison, the full rejection partition (38 absent / 3 escape / 21 illegal / 186
  present / 8 const-legal = 256), dispatch coverage both ways, and invariance across
  all 16 tracked feature configurations. Each one falsified by inducing the defect it
  names.
- **`immBytes` is an enrolled witness**, which is the ruling that settles decision
  0006's shape once: *every copy of a fact is either an enrolled witness or a derived
  artifact* — three copies with only some checked is a drift farm. So the seam between
  the authority's vocabulary and this package's readers now testifies. Every entry
  cites the `decode.ml:N` definition it mirrors and quotes it, machine-checked against
  the vendored source (`TestImmBytesCitationsResolve` — the fixture-provenance
  mechanism pointed at a reader table; it caught two drifted citations of its author's
  on its first run). And every flat reader is measured **on its own** against a derived
  vector stating the reference rule that entails its extent
  (`TestEveryReaderAgreesWithItsAuthorityDefinition`), because composition over the
  const set reaches eight opcodes out of a nineteen-entry vocabulary. On disagreement
  the reference-derived table is the presumptive authority.
- `internal/text`: the wat lexer, reject-direction first (#53) — a decoder for the
  reference's `rule token` transcribed in source order, with ocamllex's two
  disambiguation rules (longest match, then earliest arm) modelled rather than
  approximated. Both are load-bearing and both have vectors: `i32.wrap/i64` needs the
  longest match to report the whole lexeme, `offset=0` needs the earliest-arm
  tie-break. Not yet wired to a board — this PR declares no capability, so the suite
  counts do not move, and a board that didn't move is telling the truth.
- `internal/text`: production-level controls scoped to the byte space rather than to
  vectors, because **both `unknown operator` producers emit byte-identical text** — so
  any defect in the `keyword` production is invisible to a test that reads a message,
  including deleting the whole arm. All 256 bytes are checked in both positions; the
  symbol class is independently retyped with a size floor; the accept direction is
  derived from the generated table rather than enumerated.
- `FuzzLexerProgress`, budgeted in **executions** (350k in CI, wall clock in
  `make fuzz` and nightly, each unit stated at its site). The size comes from this
  target's own measured throughput — ~5k/sec against `FuzzWastLexer`'s ~44k/sec on one
  box, corpus cleared between runs — not from the 2:1 convention, which assumes a
  comparable per-execution cost and would have bought ~27 minutes against a 15-minute
  failsafe. Both halves certified separately (#28): seed-replay by reintroducing the
  over-long-length defect, exploration by the measurement that every byte 0x00–0xf4
  appears in the suite while **0xf5–0xff appears nowhere**, making the ill-formed lead
  bytes mutation-only.
- `TestTextFixtureProvenance`: the text arm of the citation checker. A wat fixture's
  vector is source text plus an expected string, not a module image, so the
  byte-literal checker cannot see one — and registering the files there would have
  satisfied `TestEveryFixtureFileIsChecked` while checking nothing. *A registration is
  not a check.* 24 cited fixtures verified.
- **The wat lexer wired to the board, and the fourth verdict drained to zero** (#53,
  decision 0010's intended ending). `CapWatReader` moved from `capabilityIssues` to
  `engineCapabilities` — declared, entry deleted, population to 0 — in one commit,
  which is what guard 6 requires and what the entry's own `Retires` condition
  demanded on the day it was written. **Board: 68 files — 1419 pass / 601 fail /
  26742 unsupported / 15 gated / 0 unimplemented**, from 783 / 1 / 26742 / 15 / 1236.
  The largest board movement since genesis, and every number in it was forecast
  before the code existed.
- `ReadTextFunc`, the wat entry point injected the way `DecodeFunc` is, so the
  harness never imports the engine it scores (contract §0). It is a *required*
  parameter of `RunGated`/`RunWith` rather than an option: a board runner callable
  without the component it declares would score 1236 vectors against a nil pointer,
  and the run loop panics on that combination, so the compiler and the loop between
  them make the omission unshippable.
- `Failure.Kind`, and with it **two fail ceilings over a structural partition**
  instead of one number. The reader raised fail 1 → 601, and a single ceiling of 601
  is precisely the invisibility decision 0010 was written to prevent: a new decoder
  defect would arrive as 602 and read as text-layer noise. Now a decoder regression
  fires as `binaryFail 2 > 1` no matter what the text column does. The partition is
  on `Kind`, not on the bucket string, because **the two layers share strings** —
  `malformed UTF-8 encoding` is 10 lexer vectors and 176 parser ones — and *when a
  partition's members share a value, an equality on that value is not a partition
  check* (grave #34). Falsified by swapping the arms: binaryFail reads 600, textFail
  1, and both ceilings fire.
- `TestBareQuoteFormsPassUnearned`: **seven passes reported as unearned rather than
  as progress.** Seven bare `(module quote ...)` forms lex clean and therefore pass,
  but six of them (annotations.wast 32/33/36/55/206/207) test annotation
  *placement*, which a lexer has no notion of, and the seventh (comments.wast:83)
  tests module validity through nested comments. They pass because nothing above the
  lexer can disagree yet — overfitting arrived at by omission (§9 G-3), which is the
  kind no board can see. The test enumerates all seven with what each actually
  tests, floors the count at exactly 7, and fails in *both* directions: an unlisted
  clean-lexing form is an unnamed unearned pass, and a listed form now rejected is an
  accept-direction defect.

- **The UTF-8 position partition, pre-registered as a control before the parser it
  guards** (#62). `utf8-invalid-encoding.wast` is 176 vectors and the text column's
  largest bucket, and it is the cheapest bucket to buy for the wrong reason: all 176
  are `(func (export "<bad bytes>"))`, so rejecting any string token that is not valid
  UTF-8 takes every one of them and is *wrong about the grammar*. Measured with the
  lexer rather than a grep — 864 suite tokens decode to invalid UTF-8, 177 sit inside
  quote forms, and the accept direction inside quote forms is **empty**, so the suite
  cannot falsify the blanket check at all. Three grammar facts are pinned against
  `parser.mly` instead: `name` (:46) and `var` (:49) decode and reject, and
  `string_list` (:342) concatenates without decoding — the accept direction that data
  segments and binary payloads route through. All four assertions falsified by editing
  the vendored authority.

  The implementation half **asserts in both states and never skips**: every row of the
  five-row partition requires the source to *lex clean* whether or not a parser exists,
  and the three-way verdict is checked once the probe stops returning its sentinel. That
  design was the second attempt. The first licensed a skip that expired when the parser
  arrived, and CI rejected it — the *no test declined to answer* step forbids a SKIP line
  under `NO_SKIP=1` outright, which is the ruling this repo's skip policy already implied.
  The rejection was right twice, because the accept direction was checkable today at a
  layer that does exist: the wrong fix (a blanket UTF-8 check in `emitVarString`, attempted
  in #60) is reachable in the lexer now. Hence the lesson, recorded in the skip inventory:
  **a pre-registered control that wants a skip has usually not found the layer where its
  property is already checkable.** Falsified by reintroducing #60's defect and watching
  `(func $"\ef")` fail the lex-clean assertion.
- `TestFetchScriptAssertsEveryAuthority` — `fetch-spec-ref.sh` asserted the presence of
  `decode.ml` alone, and had kept doing so after `lexer.mll` became a second authority:
  a presence check silently narrowed to a third of its subject, the early-return defect
  its own comment records, one scope out. The script now loops over all three and the
  set is derived from `testenv.LicensedRefPaths()` rather than restated, with a vacuity
  floor because a containment check over an empty set agrees with anything.

- **The bare `(module <wat body>)` form is scored** (#69), which is the accept-direction
  oracle the wat parser had been developed without. Every node the s-expression reader
  produces now retains its byte extent (`start`/`end`, `node.span`), so a module written as
  wat text — rather than as a `(module quote "…")` string — can be handed to `text.ReadModule`
  as source. New `KindModuleText`, its own bucket key, and a classifier guard.

  **Not one line of the reader changed. Board 1992 → 4122 pass, 253 files (was 68).** The
  admission is *coverage*, not correctness: 2130 modules the parser already accepted stopped
  being invisible. Decomposed, because a floor that does not know what it bounds is not an
  assertion — inside the 68 files already on the board, pass rose 1992 → 3106 while
  unsupported fell 1119; the other 1016 come from **185 files that enter the board**, because
  `boardFiles` selects on "holds one scorable command" and a new Kind moves the *file set*.
  Unsupported rises 26742 → 60872 in the same motion, which is corpus admitted.

  **Why this precedes #64's second half, and the reason is a measurement.** Typeuse
  resolution is a **rejector** — it can only turn passes into fails — and the board's entire
  accept-direction oracle for it was **7** must-succeed modules out of 1126. Installing a
  rejector against 7 vectors makes over-rejection invisible by construction, which is the
  overfitting law (§9 G-3) at its purest: the cheap wrong check and the correct one score
  identically on a corpus that asks nothing. Against 2130, an over-eager resolver fails loudly.

  **The classifier guard is the sharp edge, and it manufactured 9 of the first 22 failures.**
  `definition` and `instance` are *script* grammar — `script_module` is `LPAR MODULE
  definition_opt …` (parser.mly:1422), `definition` sits outside `module_` (:1389), and
  `instance` (:1439) names a module with no fields at all — so handing either to the wat
  reader invents a red indistinguishable on the board from an engine defect. Caught by the
  *clustering* of the reds, not by their count. *Gates never manufacture malformedness*,
  generalized: a harness that asks the wrong reader is the same offence one layer out.

  Two real accept-direction defects were found *by* the admission and are deliberately not
  fixed here, board-shape work travelling alone: `elem_list`'s `reftype elemexpr_list` arm
  shadowed by the offset-sugar lookahead (parser.mly:1155, **3** vectors — #75), and
  `lane_imms`' bare `| laneidx` arm eaten by `memarg`'s greedy memory index (:661–673, **9**
  vectors — #76). They are the work plan this admission exists to produce, and the 9/3/1 split
  is the *corrected* one: it first read 10/2/1 because each mechanism's file set came from
  memory rather than from the board, which put `array.wast:219` — an `elem_list` vector — in the
  lane bucket as a hedged tenth. *Derive the domain, never enumerate it*, applied to the work
  plan instead of to the engine.

  The forecast was **150–400 fails centred near 250, and 13 landed** — wrong by an order of
  magnitude, in the direction of expecting a reject-direction-built reader to over-reject
  valid input. Recorded rather than rounded: the reasoning was sound and the conclusion was
  still wrong, which is the only kind of forecast error worth writing down.
- **The flat instruction grammar** (#63): `internal/text/instr.go` and the immediate readers
  in `num.go`. The dispatch is **derived, not enumerated** — `plaininstr`'s 83 arms collapse
  to 16 immediate shapes, and the generated keyword table (decision 0009) already maps all
  589 mnemonics to the reference's own token kinds, so the only hand-written fact is 16
  kind→shape rows, re-derived from `parser.mly` at test time. The readers: `memarg`
  (`offset=`/`align=`, ordered as the production writes them), `constImm` at four widths,
  `vecConst`, `laneIdxList`, `laneidx`, `br_table`'s idx list, and the `expr1` minimal arm —
  which is here, not in #64, because **seams follow defect ownership, not surface form**
  (ruling: Scott). `offset`, `elemexpr`, `elemexpr_list`, `elem_list` and `constexpr1` are
  wired too, so `(data (offset ...) ...)` and `(elem (item ...) ...)` are read rather than
  deferred. **Board: 1628 → 1922 pass, 392 → 98 fail**; the text fail ceiling moves 391 → 97
  with a per-bucket reconciliation in the test's own comment.
- **The flat block family** (#63, same issue, second pass): `blockinstr` and its five arms
  (`block`, `loop`, `if`, `if … else`, `try_table`), `labeling_opt` / `labeling_end_opt` with the
  `mismatching label` check, `block`, `handlerBlock` with all four `(catch …)` clause forms, and
  `atBlockTerminator` — because `instr_list`'s follow set inside a block is larger than `)`/EOF
  once blocks exist, and menhir derives from the grammar what a recursive-descent reader must
  state. The handler clauses are deliberately **not** terminators: they precede the body, so
  treating them as stops would mask a syntax error. **Board: 1922 → 1941 pass, 98 → 79 fail**;
  ceiling 97 → 79, floor 1922 → 1941.
- **The reconciliation above is corrected in the same PR that made it wrong.** It attributed all
  92 unanswered vectors to #64 by reading their *surface* — a `block` is a block — when #63's own
  Scope list names `blockinstr` (parser.mly:726) and the block family (:740–:792), and the seam
  ruling moved the `expr1` arm *in* without moving anything out. Measured by classifying each
  vector on whether its boundary token is a block keyword or a `(`: **17 flat (#63's) and 75
  folded (#64's)**, matching the forecast's own "17 flat" row exactly. So the shortfall against
  353 is **42, not 59**, #64's inventory is 75 rather than 92, and the lesson is the seam ruling
  restated: *seams follow defect ownership, not surface form* — reading a bucket's members off
  their spelling is the same manoeuvre as reading a test's coverage off its case labels.
- #63's forecast was 353 and 294 landed, an **under-delivery of 59 that is not one shape** —
  and the partition is by the engine's error text, because the failure buckets name what the
  *suite* wanted and that is the wrong key for asking why we fell short. 92 are
  `unimplemented: instruction body`: vectors whose fault is in one of #63's readers but
  reachable only through a `block`/`loop`/`if`/`try_table`, so the seam ruling was right
  about ownership and the forecast was wrong about *extent*. They are #64's inventory, and
  #64's forecast starts from them rather than from a fresh classification. Five more want a
  **type context** — `(type $sig)` against inline params/results, and a type index space —
  which is neither stratum's and is the whole accept-direction remainder of the column.
- **The wat parser's skeleton stratum** (#62): `internal/text.ReadModule`, recursive
  descent over the reference's module-field grammar, returning an error and nothing else
  (decision 0011). `cursor.go` (a fully-lexed token slice with two-token lookahead),
  `types.go` (the type algebra in `parser.mly`'s own production order), `context.go` (the
  nine index spaces and the three checks the grammar itself makes — `duplicate <category>
  <name>`, `import after <kind> definition`, `multiple start sections`), `kinds.go` (the
  keyword constants the parser matches on, checked against the generated table), and
  `parser.go` (the module fields, stopping at a **named** `unimplemented: instruction body`
  boundary that #63/#64 retire). **Board: 1419 → 1628 pass, 601 → 392 fail**; the text
  fail ceiling moves 600 → 391 with a per-vector reconciliation of #62's forecast in the
  test's own comment. The forecast was 221–225 and 209 landed net: seven vectors whose
  checks this stratum implements sit behind an instruction body *in the same module*, so
  the check is never reached — the classifier asked whether a *vector* was
  instruction-bodied, and reachability turns on whether the *module* is. Named
  individually rather than netted out.
- Decision 0011: the parser's surface is error-only, and well-formed modules will be
  emitted as binary bytes into the proven decoder rather than into a second module
  representation. `binary.Module` stays the codebase's one module authority. The bridge's
  faithfulness is a pre-registered tripwire, not an intention (#67).
- The pre-registered UTF-8 siting control (#62) is now wired to the real `ReadModule`, and
  it grew a **fourth verdict** rather than taking either dishonest exit: a `wantBoundary`
  row is a vector whose site is correct and whose module reaches the instruction grammar,
  so it must fail with the *named* boundary — and it fails the day the offset grammar
  lands, forcing promotion. The `notImplemented` sentinel is kept, because deleting it
  would replace a stamped branch with an assumption that the parser is present, which is
  the deduce-don't-stamp mistake at the exact site built to avoid it.
- `TestBareQuoteFormsPassUnearned` gained a third state: **retired**. A listed unearned
  pass that stops passing may have been *regressed* (rejected on the merits — a defect) or
  *retired* (the reader advanced and declared the boundary honestly — progress), and
  collapsing them is dishonest whichever way it resolves. Discriminated on whether the
  error names itself `unimplemented`, and the staleness floor is now the **sum**
  (`want + wantRetired != 7`), because only the sum can distinguish progress from a stale
  list.
- **Four survivors of the falsification sweep, each closed with the control it exposed.**
  Every named property in the new package was broken and watched to fail, per *a control's
  green must be falsifiable*. Four mutations survived the entire board, and all four were
  the same defect in the control rather than in the code — a test narrower than its name:
  - `bindAbs` not advancing the index count. `TestBindAbsCountsAnonymousDefinitions` bound
    anon-then-named and checked the named one landed at 1, which holds even when `bindAbs`
    never increments, because `bindAnon` carries it. Now asserts the count after *every*
    binding and in both orders.
  - `atHeaptypeStart` dropping its `idx` arm. Both predicate-agreement sweeps ran over
    `keywords` — but heaptype's thirteenth arm takes `NatTok`/`VarTok`, which are not
    keywords, so the sweep was scoped to a space that *could not contain* the
    disagreement. The scope-controls-to-the-space law failing not by enumerating the wrong
    members but by picking a space one dimension too small. Widened to the whole token
    vocabulary, with `TestNonKeywordSourcesCoverEveryTokenKind` deriving the domain from
    `TokenKind`'s own extent.
  - `rpar` accepting any token, which **accepted a truncated module**: at EOF the cursor
    does not advance, so a permissive `rpar` consumes nothing, every production unwinds
    successfully, and `ReadModule`'s closing `at(EOF)` check is satisfied by the EOF that
    was never consumed. The suite cannot ask this — its `unexpected token` vectors are all
    *surplus*, never truncated, because a `.wast` whose parens do not balance is not a
    readable script. New `TestRparRejectsAndDoesNotConsume` and
    `TestTruncatedModuleIsRejected`, the latter with its own accept-direction vacuity half.
  - Error positions dropped from two of the three constructors. `TestErrorsCarryAPosition`
    covered `errf` only, so blanking the position in `errAt` and in `unexpectedAt` — the
    constructor behind the 152-vector `unexpected token` bucket — was invisible. Now
    parametrized over the *constructors* rather than the messages, with
    `TestErrorConstructorsAreAccountedFor` sweeping the package AST for `&Error{}` literals
    so the coverage claim is checked against the real set. It immediately found a
    **fourth** unlisted constructor (`bodyBoundary` composing the struct inline), which now
    routes through `errf`.

- **`derived` accepted as the third provenance category** — cited, derived,
  synthetic. A derived fixture is one the suite *implies* but does not contain:
  `TestLEBWidthIsPerField`'s accept half asserts a wide-but-legal limits minimum
  decodes, which `binary-leb128.wast` cannot state because it only asserts
  malformedness, and which `:529` and `:221` jointly **bracket** — ten bytes wants
  *integer too large*, eleven wants *integer representation too long*, so the only
  width satisfying both is 64. *Entailment from checked facts is legitimate
  provenance; unstated entailment is just synthetic with better manners.* So the
  category carries obligations and `TestDerivedFixturesStateResolvablePremises`
  enforces the half a machine can: a derived row states its premises
  (`derived from <file>.wast:N,M`) and every premise must **resolve** to a suite
  line carrying content. The inference is reviewed by eyes; a premise pointing at
  prose is caught by the same mechanism that catches a drifted transcription.
  Falsified four ways before being trusted — premise pointing at prose, premise
  past end of file, a `derived` marker with no premises at all (the laundering
  channel), and the category going empty, which fails rather than passing
  vacuously. (Ruling: Scott, PR #37.)

- **The instruction grammar, table-driven — `binary.wast` is 127/127 and the phase-1
  corpus is 764 pass / 0 fail / 0 unsupported / 2 gated.** All 26 previously-failing
  vectors drained. `decodeConstExpr`'s eight-entry accept set **dissolved**: the
  generated `opTable` answers existence, illegality, escape, and immediate shape over
  the whole opcode space, leaving `constOps` — seven bytes carrying the const-legality
  predicate the reference does not encode — as the only opcode fact this engine states
  on its own authority.
- Function-body grammar: `locals` (per-group count a u32 LEB, the *sum* checked at 64
  bits, so `integer too large` and `too many locals` are different fields), `memop`
  (flags bound checked *after* the LEB read, a u64 offset), `catch` clauses, blocktypes,
  and `sized` per body.
- **Gate flips closed as buckets, with their base counts:** `illegal opcode ff` (1),
  `illegal opcode` (1), `data count section required` (2 — this is
  [#22](https://github.com/scttfrdmn/burroughs/issues/22), closed inside #39 from
  `syntax/free.ml`'s four data-referencing opcodes rather than from a byte scan),
  `too many locals` (2), `END opcode expected` (1), `unexpected end of section or
  function` (3), `section size mismatch` (1), `integer too large` (2).
- `binary.wast:345` and `:1218` fell out **on contact** with the table, before any
  body-grammar work — 0007's postscript predicted them as a milestone and they were a
  lookup. Recorded because the mis-estimate is the transferable part.
- **The nine-defect falsification pass.** Each mechanism was broken on purpose and the
  board re-read, because *a green that survives the bug it names is a control in name
  only* and a 26-vector drain is the shape that most deserves the suspicion. Six defects
  refilled exactly the buckets they claim. **Three survived the entire suite** and are
  now `internal/binary/instr_probe_test.go`: per-body extent distinguishability, lane
  index width at the production reader, and the blocktype alternation's branch order.
  Each control was itself falsified before being committed.

- **Phase 1 of the decoder is done.** The byte-string corpus has nothing left to teach it:
  **764 pass / 0 fail / 0 unsupported / 2 gated** on the default board, **766 / 0 / 0** with
  every gate on, and `binary.wast` **127/127**. The two gated vectors are
  `binary_leb128_64.wast:1,16`, honestly parked on memory64 and simultaneously *failed* in
  the all-gates-on lane until that gate works. No version bump — `v0.1.0` is the full MVP
  core suite, and the remaining ~250 `.wast` files need the wat parser
  ([#8](https://github.com/scttfrdmn/burroughs/issues/8)) before that number means anything.
- **Gates now govern opcodes, not just sections**
  ([#48](https://github.com/scttfrdmn/burroughs/issues/48), decision 0008). The
  table-driven instruction dispatch consulted `Features` nowhere, so with every gate off the
  decoder accepted `throw_ref`, `try_table`, `v128.const`, and `ref.eq` in a function body —
  the accept-and-ignore half of the gate ruling. A hand-authored proposal→opcode mapping
  (`internal/binary/gatemap.go`) now covers **318 accepted opcodes** across 11 entries, and a
  gate-off engine declines them with a feature-named error.
- **Four gates that did not exist**: `Features.GC`, `TailCall`, `RelaxedSIMD`, and
  `MultiMemory`. Each is a *tracked* proposal (contract §9 G-2) with constructs in the table
  and no bool to gate them — the forgotten-fifth-gate scenario existing in the wild, four
  times over, and invisible to the reflection-derived lanes precisely because a gate that is
  not there cannot be reflected over. #48 named GC; reading the table's mnemonics against
  G-2 found the other three.
- **The inverse gate control**, `TestEveryGateOffDeclinesSomething`, landed in the same
  change as its subject rather than parked red — *a debt is discharged by a tripwire, never
  an intention*. `TestAllGatesOnLeavesNothingGated` bounds over-gating; this bounds
  under-gating, requiring every gate, turned off with all others on, to decline at least one
  construct. It carries a second obligation: its SIMD probe is a **blocktype**, so it
  permanently pins `decodeBlockType`'s branch order — move the valtype branch off last and
  the decline is overwritten with `malformed value type`, and the control goes red. One
  control, two obligations.
- The mapping is **cited** per entry to the proposal document that enumerates the opcode
  (`gc/MVP.md:809`, `tail-call/Overview.md:139`, …) and machine-checked two ways: every
  opcode it names exists in the table and is not an arm the reference defines in order to
  *reject*, and every gate governs at least one construct. `RequireProposalDoc` is the third
  licensed skip door, so a citation check cannot silently stop checking.

- **`Result.UnsupportedByHead` — the unsupported column bucketed by command head**, printed
  on its own board section largest-first, for the same reason failures bucket by expected
  spec text: *1345 is not a work plan.* Keyed by the head atom as written rather than by
  `Kind`, because every unsupported command shares `KindUnsupported` and keying by it would
  yield one bucket of 1345 naming nothing. The breakdown names the components: **504
  `assert_return`** is the interpreter, **398 `module`** and **308 `assert_malformed`** are
  the text grammar ([#53](https://github.com/scttfrdmn/burroughs/issues/53),
  [#8](https://github.com/scttfrdmn/burroughs/issues/8)), **110 `assert_invalid`** is the
  validator. `Command.Head` is recorded for every command to make it possible: `Kind` says
  what the harness can *do* with a command, `Head` says what the command *is*.
- **`TestDenominatorExcludesUnaskedCommands`** — `Total()`'s exclusion of `Unsupported` and
  `Gated` became load-bearing the moment the corpus was derived, and *a comment cannot
  fail*. Folding `Unsupported` in would render this board as 783/2136 and, worse, make
  the ratio improve when a *component* lands rather than when a *verdict* is earned. The
  denominator is over what was **asked**; one table case asks nothing at all and expects
  zero.
- **`TestUnsupportedIsBucketedByCommand`** — the buckets sum to the scalar, are non-empty
  whenever the scalar is, and carry no empty keys. The vacuity half is the point: a
  breakdown map that silently stopped being populated would agree with an empty comparison
  and print a board section with nothing under it.
- A **vacuity floor** on the derived selector (`>= 12` files), which earned itself
  immediately: the first draft said 20 on the strength of "27 files have no
  interpreter-dependent command" — a different set — and the floor failed on my wrong
  expectation rather than shipping it. *A comparison against an empty set succeeds, so the
  control that compares gets a plausible-size floor.* The instructive miss is `data.wast`,
  whose five `(module binary ...)` forms have non-string-literal elements and so are not
  scorable.
- Six `simd_const.wast` vectors added to `TestGatedVectors`' per-vector allowlist. They
  arrived with the derived corpus and the gate is **right**: `\60\00\01\7b` is a functype
  with a `v128` result, so a SIMD-off engine must decline. Verified by reading the vectors,
  not by assuming the new declines were over-gating — and each is simultaneously *failed* in
  the all-gates-on lane, where the gated count is zero.
- **A pass floor on the all-gates-on lane**, closing a gap #51 made concrete.
  `TestAllGatesOnLeavesNothingGated` asserted only `Gated == 0` and counted nothing, so the
  one lane best placed to see a *gated feature breaking* was blind to it: with every gate
  on, a broken feature turns a pass into a fail and leaves `Gated` at zero. Falsified by
  breaking the heaptype descent — 798 → 791 pass, `Gated` still 0, and the old assertion
  still green. The default lane cannot substitute, since there these vectors are honestly
  `gated` and a floor excluding them says nothing about whether GC works.
- **`TestRefTypeReadsTheReferencesFourteenForms`** — scoped to the space, not to the twelve
  forms #51 needed: every `s7` value a single byte can encode (−64..63), partitioned into
  ungated / feature-declined / malformed with the **counts** as the assertion (2 / 10 / 113)
  plus three named exclusions that sum the partition to 128. It found all three exclusions
  itself — the first draft asserted 2/10/114 over all 128 and failed, which is how `0x40`
  turned out never to reach `decodeRefType` at all (`decodeTable` peeks it first) and how
  `0x63`/`0x64` turned out to truncate rather than decide. *Measured, not predicted, and the
  three are excluded by name rather than by shrinking a count until it matched.*
- **`TestTableInitializerFormIsGatedNotMalformed`** and
  **`TestHeapTypeFollowsTheParameterizedForms`**. The first is #51's vector both ways —
  gate off declines by feature name and specifically *not* as `ErrMalformedRefType`, gate on
  decodes through `decodeConstExpr` — **cited** to `elem.wast:453`, its 45-byte image
  machine-compared by `TestFixtureProvenance` (falsified: one mutated byte fails the
  citation). All seven declines carry the byte-identical table entry
  `\40\00\64\70\00\01\d2\00\0b`, so one citation covers the class and the other six are
  named in the `TestGatedVectors` allowlist. Every new control was falsified by
  reintroducing the defect it names: removing the `0x40` branch restores #51 and fires
  three assertions; removing the abstract-reftype gate turns 2/10/113 into 12/0/113.
- `ErrMalformedRefType`, `ErrMalformedHeapType`, and `ErrZeroByteExpected`. The last is
  declared-and-tracked rather than reachable: no suite vector asserts `zero byte expected`
  today, because the sites calling it are gated constructs a gate-off engine declines
  first. Named at its definition site with the reason, per the `ErrTrailingData` ruling
  (#6).
- The GC gate's non-opcode constructs recorded in `gatedNonOpcodes` — the `0x40` table form
  and the twelve GC reftypes, with their check sites. A construct with no gate entry is the
  #48 defect, and silence is how it got there the first time.
- **`internal/text/keywords.go` — the wat keyword table, machine-derived from the reference
  interpreter's text lexer**: 589 keywords, 173 token kinds, at the same pin as
  `optable.go`. Decision 0007's argument one grammar over (**decision 0009**), and the
  asymmetry is starker here: every vector bearing on the table is `assert_malformed`, so a
  table containing **nothing at all** passes all 566 `unknown operator` vectors while
  failing every valid module. The extraction turned out *easier* than `decode.ml`'s — one
  `match` block, 589 one-shape arms, no alternations, no wrapped heads — which is the recon
  finding that let 8a stand alone instead of folding into
  [#8](https://github.com/scttfrdmn/burroughs/issues/8).
- **`internal/gen/keywordgen`** with 0007's four conditions discharged: an
  unrecognized arm inside the block is a hard error rather than an omission, a `Floor = 400`
  vacuity control against 589 measured, a generation header naming authority/revision/
  extractor version, and `make keyword-drift` in CI. One floor rather than `opcodegen`'s
  per-region map, because this grammar has one region and there is no analogous partition to
  under-count independently.
- **`checkShape` — the check with no counterpart in `opcodegen`.** `let keyword = ['a'-'z']
  (letter | digit | '_' | '.' | ':')+` (lexer.mll:111) is what ocamllex matched *before* the
  arm dispatch ran, so an arm head outside that charset is dead code upstream and would be a
  row here no input can reach. Notably `/` is absent from it, which is the character that
  routes `i32.wrap/i64` to the **second** `unknown operator` producer (`| reserved`, :839)
  rather than the keyword fallthrough (:809) — and that split, 8 + 3 across the eleven
  legacy mnemonics, is why the mnemonics need maximal munch and not a table lookup.
- **`internal/text/keywords_test.go` — an integrity check distinct from the drift check**,
  and the distinction is a finding rather than belt-and-braces. `keyword-drift` needs
  `third_party/spec`, so it cannot live in `make check`; on a fresh clone `DO NOT EDIT` is a
  *request*, and the row that could be hand-added is exactly `get_local`. So the committed
  table is asserted with no corpus at all: a size floor, the eleven absences, the `keyword`
  shape, and a content spot-check with its kinds. Each falsified by mutating the committed
  file — the obsolete row, an unreachable row, an empty kind, an emptied table, and a wrong
  kind, each firing only where named. Also measured, because the first draft of the
  reasoning guessed: `exclusions.generated: lax` does suppress `unused` on the table, but it
  is *not* why the linter is quiet today — these tests read the map, so `unused` is correct
  to say nothing. The exclusion is the silence that would remain if the tests were deleted,
  which is the change that would take the package from "table with a consumer" to "table
  with none" with nothing objecting.
- **`internal/gen` — decision 0006's condition discharged, not deferred again.** The second
  consumer arrived (`keywordgen` reads the same pin from the same script and formats the same
  way), so the pin reader and the formatter moved out of `opcodegen` and its shim layer was
  deleted rather than kept: one vocabulary for one fact. What is deliberately *not* shared is
  anything either generator's grammar knows — a shared `parseArm` would be the wrong seam.
  With its own tests, because both drift checks need a vendored reference and so nothing in
  the tree exercised this package on a fresh clone.
- `testenv.RequireSuiteFile` — a fourth licensed door, and the first for the *same* corpus as
  an existing one. The unit that earns a door is the **question**: `RequireSuite` asserts a
  count over 257 files, and a citation check against one named vector needs that file, which
  a full corpus missing it satisfies and a 249-file corpus does not. It exists because the
  skip was first written inline and `TestEverySkipSiteIsLicensed` failed the build — twice
  now that AST check has caught an author who knew the rule.
- `testenv.refFloors` — the reference size floor keyed by path constant rather than passed at
  the call site, since *a floor passed by a caller is a fact about a file typed somewhere
  other than where the file is named*, and the failure mode is a caller passing 0 and
  defeating the control it is calling. An unregistered path is a hard failure, never a
  default floor. `MinRefLexerBytes` is its own constant even though 20000 holds for both
  files at this revision: sharing it would make the two floors' agreement an accident a
  future upstream edit silently ends.

- **The `(module quote …)` corpus admitted, and the board's fourth verdict** (decision 0010,
  Scott's ruling on [#53](https://github.com/scttfrdmn/burroughs/issues/53)). Two changes in
  one entry because neither is honest alone.

  **The admission.** `classify` now recognizes `(module quote "…")` — bare and under
  `assert_malformed` — so the derived corpus (#52) admits the **54 files that hold nothing
  else scorable**. The board goes 14 → **68 files**, 2144 → **28777 commands**, and the
  unsupported ceiling **1345 → 26742**. *That rise is corpus admitted, never regression*: the
  selector did not change, it still asks which files hold a command whose `Kind` the run loop
  scores, and two new Kinds made 54 more files answer yes. The alternative — enumerating the
  quote files a lexer could already answer — is the `phase1Files` defect #52 removed, wearing
  a new name.

  **The fourth verdict, `unimplemented`.** The 1236 admitted vectors could not go in `fail`.
  Today that column means **defect** — the board's lone failure (`binary-gc.wast:1`) is
  visible *because* the column discriminates wrong-answer from not-built — and scoring 1236
  unread quote vectors as failures takes it 1 → 1237, so a genuine regression tomorrow
  arrives as 1238, invisible. A column that cannot surface a new defect has stopped being an
  instrument. So: **`unsupported` = the harness cannot ask; `unimplemented` = the harness
  asked and the engine lacks a named component to answer.** `gated` is the architectural
  precedent rather than the argument — absence-by-configuration there, absence-by-construction
  here.

  Guarded so it cannot become a dumping ground, which is the same risk the third verdict had
  to be fenced against (#27) with mechanisms that do not transfer at this scale:
  **capability-derived, never hand-assigned** (`classify` computes `Needs`, the run loop asks
  what the engine has, the gap is the verdict — no per-vector allowlist, because 1236 vectors
  cannot have one); a **closed registry where each entry carries its tracking issue**, and an
  unregistered capability panics rather than growing the column; the **partition asserts the
  fifth term**; and **guard 4 is a release gate** — no minor version while its milestone's
  `unimplemented` is nonzero, `v0.1.0` requires zero (appended to decision 0004, so the rule
  lives where releases are cut). The category exists to **drain**, and the version scheme is
  what enforces the draining rather than trusting it.

  Every control falsified by introducing the defect it names and watching it fail — eight of
  them, including Reading A itself (folding the column into `fail` fires the fail ceiling at
  1237), a classifier that stops setting `Needs` (the vacuity floor), an invented capability,
  a lost partition term, and a registry allowed to run ahead of the engine.

  **Board: 68 files — 783 pass / 1 fail / 26742 unsupported / 15 gated / 1236 unimplemented**,
  all 1236 attributed to `wat-reader` (#53). Pass and fail are unmoved, which is the honest
  reading: admitting a corpus earns no verdicts.

- **Capability registry entries are born with their retirement conditions** — the fourth
  verdict's second structural control, added on review of the above
  ([#58](https://github.com/scttfrdmn/burroughs/pull/58)) and **temporal where `gated`'s is
  spatial**. `gated` gets its anti-dumping-ground guarantee from a lane: turn every gate on
  and the count must be zero. That does not transfer, for the reason the category exists —
  absence-by-construction has nothing to switch on, so there is no lane to build.

  So a registry entry now states, the day it is written, the condition under which it must be
  **deleted**, and `engineCapabilities` states what the engine actually has rather than
  leaving it to omission (an absence cannot be read as a claim, and guard 1 makes the
  engine's half a declaration). `RunGated` derives from that declaration, so the board scores
  against what the engine says it has rather than what a call site remembered to pass — when
  the wat reader lands, one line moves the board.

  `TestNoCapabilityOutlivesItsComponent` enforces both directions: a capability the engine
  declares must no longer be registered, **and** must have drained its population to exactly
  zero. Retirement is one motion, and each half alone is a defect — a landed component that
  leaves vectors in the fourth column has converted a deferral into a disappearance, which is
  precisely what the ruling exists to prevent. An entry with no retirement condition panics in
  the run loop: an entry that cannot die makes its column permanent by omission rather than by
  decision. **An entry may not outlive its component; a capability with no population and no
  retirement is a squatter.**

  Four more falsifications, all fired where named: declaring the capability while leaving the
  entry (`retirement is one motion, and this is the half that was skipped`), a reader that
  lands leaving 1236 behind (`converted a deferral into a disappearance`), the registry
  emptied without draining (the vacuity floor — `compared nothing against nothing`), and an
  entry born without a death certificate (`an entry that cannot die outlives its component`).

  `CapWatReader`'s condition, from day one: retire when the reader is wired and
  `unimplemented(wat-reader)` is 0 — every vector converted to pass or fail, **none left
  behind**. That makes #53's done-when checkable by CI instead of by a reviewer.

### Changed

- **The §3 sentinel's doc names a narrowed gap rather than a missing linker, and six live "v0 has
  no linker" sentences were repointed**
  ([#157](https://github.com/scttfrdmn/burroughs/issues/157)). A ruling retroactively falsifies the
  prose written before it, so accepting the registry includes finding the sentences it orphaned:
  `ErrUnsupported` said "what is missing is *linking*, which is contract §3 and v2-or-later work",
  and `InstantiateLinked` falsified the second half of that clause. The category survives, because
  a module whose import *nothing supplied* is still well-formed and the engine's shortfall is
  still a component it does not have — but the gap is now specifically an unregistered module or a
  supplier whose own instantiation failed. The engine's four sites say `is an import nothing
  supplied (contract §3)` where they said `linking is not implemented`, the swap forced by grave
  #36: an engine with a linker cannot testify that it has none.

  The four archived board quotes in `spec_test.go` keep the retired string **verbatim** and the
  retirement is stated in the current section instead. A re-based ceiling's history is the part
  worth keeping unedited, and a reader grepping the old text should find a resolved rewording
  rather than a bucket key that silently stopped matching — which is the failure mode a rewording
  mid-drain has.

- **The instrument-PR stop condition counts *purpose*, not line-majority** — Scott's refinement on
  the flag [#159](https://github.com/scttfrdmn/burroughs/pull/159) raised. #159 lands
  `table.init`/`memory.init` end to end, drains 1702 fails, and reads **1:1.4**; by the old rule's
  letter that made it instrument-heavy and two-consecutive with #158, which is the counter misfiring
  on its own purpose. The stop condition exists to prevent drift *into meta*, so a PR that lands
  engine capability is product whatever its falsification bill — and #117's fit already predicted
  this, an arm being a *small* piece of work against a per-PR instrument floor that does not shrink
  with it, so a line-majority test on an arm PR is the disguised minimum-diff-size rule wearing the
  stop condition's clothes.

  What keeps the refinement from being self-serving is stated with it, the
  actor-never-classifies-the-actor rule being live: the classification is **named in the PR body and
  challengeable**, and the **line ratio keeps its own separate instrument** — still quoted every PR,
  still never compared to a threshold — so a purpose-classified product PR that is *also* drifting
  stays visible in the figure. A purpose classification is not an exemption, and the exemption rule
  is untouched. Scott holds the veto line, as on every governance edit; **#159 is product and the
  counter resets**.

- **Two instrument-craft laws named from #159's own findings**, both about a control that reports the
  wrong subject rather than staying silent.

  **A floor equal to the failure mode's output certifies the failure**, so a floor derives from the
  *authority* and never freezes at what the current reader happens to produce. The positional
  `plaininstr` reader's pair floor was set at **8**, exactly what the degraded alternation reader
  yields — 8 two-lookup arms against the positional reader's 10, the two extra being
  `STRUCT_GET`/`STRUCT_SET`, whose second lookup `$3 c (field x.it)` is not a word in the
  alternation. The misdirection is what makes it worse than a merely loose floor: stubbing the
  regexp *did* go red, so the control looked alive, and it reported drift in `idxPairLookupKinds`
  when the defect was in the reader — *the drift report was true, the attribution was the lie*. A
  reader following that message repairs the subject to match a broken instrument. The remedy is a
  **discrimination check** beside the floor, asserting the capability that separates the reader from
  its degradation, because a count cannot separate two readers whose counts overlap.

  **Print the diff**, permanently, as the discriminator between a stillborn control and a mutation
  that did not apply — nothing else tells the two apart, and the two readings differ in what you go
  and change next, with the flattering one blaming the control. #159's `TABLE_INIT` deletion passed
  on its first attempt and the control was *right* to pass: the pattern matched `initSugarKinds`,
  which holds a byte-identical `"TABLE_INIT":  true,` line one screen above the intended map, so a
  row in a different table was deleted. **Field attribution is not first-match** therefore extends
  from generators (`gateFor`'s narrowest-match) to the gated allowlist, fix sites, and the mutation
  scripts themselves: anchor on the containing declaration, not on the row.

- **The instrument-to-engine ratio is a quoted figure and not a threshold, because the
  recalibration measured it and found it is mostly a function of PR size**
  ([#117](https://github.com/scttfrdmn/burroughs/issues/117)). `scripts/ratio.sh` is the recorded
  command — the comparator is fixed by ruling and takes no arguments, which is the point — and it
  reproduces #113's published 1:6.6 exactly, which is how the methodology was pinned rather than
  re-chosen.

  Both series are quoted with their eras named. The uniform comparator barely moves the two
  windows that made the rule: **1:1.8 → 1:2.0** and **1:5.1 → 1:5.1**, the deltas exactly
  compensating at ±137 and ±5 lines, with `internal/spec/wast.go` and `sexpr.go` the only
  reclassified files. The old comparator is identified rather than guessed at: dropping
  `internal/spec` from the script's instrument list reproduces the published **463 / 2347**
  exactly, so the two definitions differ by that one directory and nothing else. The issue
  predicted `internal/gen` would be the main mover and for those
  windows it is not — they predate the generators. Where it bites is 0014, **1:0.4 ad-hoc versus
  1:2.9 uniform**. So the drift the old series recorded survives its own recalibration.

  What does not survive is reading the quotient as a rate. Over 31 first-parent merges,
  ρ(engine lines, ratio) = **−0.55** and instrument lines fit **486 + 0.79 × engine**: a fixed
  ≈490-line instrument cost per PR plus four-fifths of a line thereafter, which alone predicts
  1:5.7 at 100 engine lines and 1:1.0 at 2000. Every candidate threshold is therefore a disguised
  minimum-diff-size rule — at 1:3.0 the two worst offenders are an 18-engine-line board-bounds PR
  and a 28-line UTF-8 partition, and #113's 1:6.6 exceeds the model for its size by 0.56 of a
  residual standard deviation. A gate firing on #113 fires on its being 127 lines long. R² is
  0.51, so the fit is a description of the corpus and not a predictor.

  **Scott's era-band hypothesis was tested and is not supported.** Partitioned by the package
  receiving the engine lines: `interp` **1:1.7**, `text` **1:1.5**, `binary` **1:2.0**, with the
  arm era in the middle and per-era residuals of +28 / +61 / −155 lines against a 458-line sd.
  The four-crossing streak is those PRs being *small* — 190 to 297 engine lines against a corpus
  median of 344 — not costly to certify. The same finding in era clothes; recorded because a
  hypothesis measurement killed is worth more written than omitted.


- **`internal/gen/mllex`: the `lexer.mll` arm reader is one implementation, and the three generators
  call it** ([#105](https://github.com/scttfrdmn/burroughs/issues/105)). `keywordgen`, `opgen` and the
  new `memarggen` each read the same authority's arm heads, and the wrapped-arm defect that cost grave
  #105 (411 rows extracted where 436 were measured, silently) was **re-derived rather than copied** —
  which is the whole finding: a grave filed against one file reads as a fact about that file and is
  actually a fact about a shape. `FindBlock`/`Arms` now own the rejoining, the continuation rule, and
  the empty-block refusal in one place, so the third consumer inherited the lesson instead of paying
  for it. `crossover_test.go` asserts both wrap directions against the pinned reference.

- **The instrument-to-engine ratio has a fixed comparator, and stop-condition exemptions can no longer
  be granted by the actor** (rulings: Scott, PR #113). Two process laws, and both take the same shape —
  *the actor being measured does not choose the measure, and does not grant its own exception*.
  **Engine = code in the module path; instrument = tests, generators, harness — no per-file pleading.**
  #113 quoted 1:5.2 and argued its accept-direction control onto the engine side on the true premise
  that the standing rule calls such a control product work; the two readings differed by **1:5.2 versus
  1:1.1**, and the choice between two honest numbers was the dishonesty. Product-work classification
  (which governs *selection*) and ratio classification (which measures *drift*) are now deliberately
  different questions with different answers for the same file. The uniform rule quotes uglier — #113
  is **1:6.6** — so the threshold is recalibrated once against it and historical quotes stand with
  their era noted. And **stop-condition exemptions are spent only by a principal's order or stamp**:
  "this PR wasn't elective" is a plea every drifting PR can make, so it is inadmissible from the actor
  however true it is. The actor flags; the principal rules.

- **Identifiers in doc comments are citations now, and the class is un-frozen**
  ([#116](https://github.com/scttfrdmn/burroughs/issues/116)). The class was left as convention on the
  explicit criterion *convention until first drift*, recorded in #93's scope note. The drift arrived
  and was measured: `constWalk` was cited in three comments across three PRs and has **never existed**
  — not renamed, not moved, fiction from the first keystroke, in prose describing where a gate is read.
  So the criterion has fired and the fixture-provenance treatment extends to prose-in-code: an
  identifier named in a comment resolves to a definition, or the comment is phrased historically.
  The control is `TestEveryCitedTestNameResolves` widened from test names to identifiers generally, and
  its three paid-for trigger lessons are recorded to be **copied rather than re-derived** (#105):
  rejoin hyphenated line wraps, scope the historical exemption **per sentence** not per block, and
  exclude declaration-shape spans rather than backticked ones. The still-unchecked sibling is the
  **issue-number** class from #84 — different oracle, so it stays split at the seam.

- **The three code generators live in `internal/gen/`, and a repo-relative path is now
  *derived* rather than counted** (decision 0014). `opcodegen` was
  `internal/binary/internal/opcodegen` and `keywordgen` was `internal/text/internal/...`,
  which is right while each generator serves one package and wrong the moment a third one
  reads both — `opgen` imports the pair, and a generator nested under one of its own inputs
  cannot. All three are now siblings under `internal/gen/`, with `cmd/` subpackages
  unchanged in shape.

  **The move surfaced as a *skip*, not a failure, which is the part worth recording.** Five
  sites reached the vendored reference by climbing `../../../..`, correct only at the
  pre-move depth. A wrong path did not error — it made a *vendored* reference look **absent**,
  so `testenv.RequireSpecRef` licensed a skip and every generator drift check passed by
  asking nothing. Only `BURROUGHS_NO_SKIP=1` turned it into the failure that found it: *a
  skip is not a verdict*, and here the skip was **bought** by the defect. Fixed at the door
  rather than at the five literals — `gen.FromRoot` walks up to `go.mod` and fails loudly, so
  the class is gone rather than the instance: *derive the domain, never enumerate it*, applied
  to **location**. `RequireSuiteFile` takes a file *name* now for the same reason;
  `RequireSuite`/`SuiteFiles` keep their directory parameter because their own falsification
  tests point them at empty temp dirs.

  Each emitter also declares its own `Output` constant, so `make <target>` and the drift check
  name one fact once. Previously both spelled it — the Makefile from the root, the test from
  its own directory — and the move falsified the test's copy while the Makefile's stayed
  right: two places knowing one fact (0006), where the fact is a location and the mover is a
  directory rename.

- **`opcodegen.Arm.Mnemonic` is load-bearing, and its comment said the opposite**
  (decision 0014). It read "kept for the generated table's readability and for error
  messages. Not load-bearing" — true when written, and false the moment 0014's join keyed on
  it: a mnemonic is now what says `i32.add` encodes to 0x6a, so an upstream constructor rename
  silently *moves an opcode* instead of changing a string nobody reads. Corrected in the same
  change rather than in a follow-up, because *a ruling retroactively falsifies the prose
  written before it*, and a field documented as decorative is exactly the field a future
  change treats as safe to rename. `opcodegen.Version` → 2 and `optable.go` regenerated, since
  the header is a claim about the extractor/table *pair*.

- **The vendored suite is pinned by SHA, and the board names the corpus it measured**
  ([#42](https://github.com/scttfrdmn/burroughs/issues/42)). `fetch-spec-tests.sh` cloned
  upstream's tip and `pull --ff-only`'d thereafter, so the corpus floated: it happened to sit
  at `de54fd2` and nothing in the repo said so or would have noticed it moving. Pinned to
  `de54fd27ecf3e68dfd16b6199c548df77b6a2cc1` (2026-07-29, 257 `.wast` files), asserted on
  every path, with a `MinSuiteFiles` floor so a one-file checkout cannot pass as a corpus.
  The board's aggregate line now prints `corpus: suite pin <sha>` beside its counts and
  **verifies the pin against the actual checkout**, because *a count is a claim about a
  corpus* and an unpinned corpus has no identity — two developers on different fetch dates
  could quote incompatible numbers and both be honest, which made *never quote a suite count
  that wasn't run* ambiguous about which corpus a run quoted. The trade is drift for
  staleness, taken deliberately: a stale corpus is visible in a diff, where drift is visible
  only as a number that moved. Bumps are now deliberate PRs, the same posture as
  toolchain currency (0005). Decision 0007's reason for *not* pinning the suite alongside the
  reference is preserved rather than repealed — an input to a report gets pinned, a report
  does not have to be — and what changed is the measurement that "drift is visible" is weaker
  than it sounds.

- **0011's bridge veto is lifted, and the text→binary comparator is ruled** (decision 0011,
  appended; [#67](https://github.com/scttfrdmn/burroughs/issues/67)). The veto's precondition
  was the sequencing correction recorded in 0011's first appendix — the internal form before
  the encoder — and the internal form has now landed, so the bridge proceeds as originally
  ruled: the parser emits binary bytes into the decoder, and the decoder stays the sole module
  authority. The comparator is **wabt as a one-time generator of a committed cross-check
  corpus with a provenance header, never a gate in the verdict path**: self-agreement stays
  inadmissible (our encoder agreeing with our decoder is one witness talking to itself), but a
  *generated* external witness puts no non-Go binary in CI — the same shape as `optable.go`
  (0007) and `keywords.go` (0009), and the **no cgo** gate is untouched. #67 stays a
  pre-registered tripwire in #8's definition of done, closing by falsification in both halves
  rather than by the encoder landing. Consequence for
  [#63](https://github.com/scttfrdmn/burroughs/issues/63): the `plaininstr` scope statement it
  was holding as a decision is now *derived* from what #8's accept grammar requires, so it
  stops being a design call.

- **The rules now govern work *selection*, because the board reached 0 fail and the project
  kept building instruments.** *Bucketed failures are the work plan* presumes buckets; when
  fail hit zero that rule lost its subject and the fallback — deferral citations, controls,
  metadata — is all overhead by nature, so the gradient inverted with nothing saying so.
  The measurement that made the rule: over the trailing six merges the instrument-to-engine
  line ratio went **1:1.8 → 1:5.1** (engine 2007→463, test 3681→2347) while the
  `unsupported` column did not move at all, and `internal/interp` still holds **0 engine
  lines** against 493 test lines. Invisible per-PR because every one of those PRs was
  individually defensible.

  Five new Disciplines, placed *first* in the section because they run upstream of every
  rule about doing selected work well: **the phase's product is the work** (with a per-PR
  `unsupported` delta, a quoted instrument-to-engine ratio, and *two consecutive
  instrument-only PRs is a stop condition*); **control work is a debt against the product**,
  whose only exception is a control catching an **accept-direction** defect the suite scores
  green by construction (§9 G-3); **a zero-fail board is not a green light, it is a lost
  instrument**; **a representation is not a recognizer**; and **decisions serve the thesis
  directionally**. The delta rule points at the existing `unsupportedCeiling` rather than
  building a second mechanism (*one concept, one trigger*, [#82](https://github.com/scttfrdmn/burroughs/pull/82)),
  and the Board section of a PR now carries both figures, since a rule with nowhere to be
  written is a habit.

  Two corrections fall out of the same measurement. **Decision-before-code gains a
  counterweight** — an ADR is not product work either, so *one ADR earns one
  implementation*. And a stale citation: the LEB bucket's 13 blocked members were blocked on
  **#39**, the code-section grammar, not on #7, the interpreter core. The conflation was
  carried across a session boundary in a summary and cost real time before it was checked.

- **0011's bridge has no destination yet, so the internal form comes first**
  ([#67](https://github.com/scttfrdmn/burroughs/issues/67),
  [0011](docs/decisions/0011-wat-parser-return-form.md) appended). Part 1 of that decision —
  the error-only `text.ReadModule` surface — is vindicated: #62/#63/#64 all landed against
  it. Part 2's accounting was wrong. It said reaching `binary.Module` through an encoder buys
  "the binary path's entire conformance record", but `binary.Module` is `{Version, Sections}`
  with payloads aliasing the input image, **28 of the decoder's 29 `decode*` functions return
  bare `error`**, and the 29th returns `(bool, error)` where the bool reports whether the
  section *has* a grammar. Arriving there buys the decoder's *verdict*, not a module — and
  `assert_return` needs something instantiable.

  The one-module-authority ruling **stands and is strengthened** (the authority now has to be
  built, not merely reached); only the order changes. The internal form is grown out of the
  decoder, which has 4162 vectors of conformance record, and the encoder targets it
  afterward — building the encoder first would shape the representation from a parser that
  has never accepted a module, which is the load-bearing-spot manoeuvre 0011 itself declined
  as option B. #67's half 2 asked for "a statement of what the text denotes, from outside
  both the encoder and the decoder" and called it a design question; the internal form *is*
  that statement, so the comparator dissolves into the artifact. Scott's veto on the bridge
  stays open and is no longer the head of the queue.

- **The all-gates-on pass floor was stale by 3380** ([#87](https://github.com/scttfrdmn/burroughs/issues/87)).
  `allOnPassFloor` was **798 against an actual 4178** — set in [#56](https://github.com/scttfrdmn/burroughs/issues/56),
  15 commits back, and left behind by every text-side landing since. It could not have caught a
  regression that erased four fifths of the lane. Raised to the measured value, with #86's +1.
  Found by *reading the printed total next to the constant*, not by any control: nothing asserts
  that a floor is near what it floors, so a floor left behind by a large jump degrades silently
  into decoration — the same defect class as a vacuity floor that passes on an empty set. Whether
  staleness should itself be *checked* is #87, flagged rather than decided here. The default lane's
  `binaryFailCeiling` also moves **1 → 0**, so with both columns at zero any new fail is a
  regression by definition.
- **`reader.skip` exists, because `peek`-then-discard-the-error does not state what it knows**
  ([#86](https://github.com/scttfrdmn/burroughs/issues/86)). The reference has `skip n s`
  (`decode.ml:20`) and uses `peek` + `skip 1` at four sites; transcribing that as
  `_, _ = r.byte()` discards an error that provably cannot fire, and `errcheck` was right to object
  — *a discarded error is a claim about reachability the code does not make*. The named primitive
  makes the claim. Deliberately unchecked and returning nothing: an over-skip is a caller that
  skipped without peeking, and clamping it would convert that bug into a silent misparse of the
  next field.
- **`[Unreleased]`'s 16 group headings consolidated to 3, and the structure is now a gate**
  ([#55](https://github.com/scttfrdmn/burroughs/issues/55)). Keep a Changelog 1.1.0 has one group per
  type per release; this file had reached 5 `### Added`, 5 `### Changed`, and 6 `### Fixed`, because
  each PR appended its own heading rather than merging into the existing one. The consolidation is
  **pure movement** — verified by comparing the multiset *and the sequence* of non-heading content
  lines before and after: 1706 lines, identical, nothing dropped, reworded, or reordered. The
  `[0.0.1]` section and the file's preamble are byte-identical, per the issue's out-of-scope line.

  The gate is `internal/testenv/changelog_test.go`, in Go rather than as a `make check` shell step
  because *a shell pipeline that reports "no matches" is indistinguishable from one that agrees* —
  the vacuity floor and the distinguishable messages are the whole argument against a two-line grep,
  and they apply to the check's own reading of the file. It makes three claims and each is falsified
  separately, because **members of a partition that share an error value all score as each other**
  (grave #34): a duplicated heading fails for repetition, a seventh group name (`### Improved`) fails
  for vocabulary, and a Changed-before-Added swap with no duplication fails for order. A fourth
  injection broke the heading regexp itself and tripped the floor at *0 headings across 2 sections*
  — the degenerate agreement the floor exists for. Scoped to every release section, not just
  `[Unreleased]`: released sections are out of scope for *editing*, not for *reading*.
- **The `annotations.wast:1` residue was attributed to #55 for three PRs, and #55 is a changelog
  issue** ([#83](https://github.com/scttfrdmn/burroughs/issues/83)). Re-pointed at 5 sites in
  `internal/spec/spec_test.go` and 3 here, including in a merged PR body and commit message. The
  attribution was never checkable: `TestFixtureProvenance` machine-checks that a `.wast:N` citation
  resolves, and nothing resolves an issue *number* to its subject — so a bare `#NN` in prose is the
  drifted-citation defect with the machine-checked half removed, and this one was quoted forward
  eight times because quoting is cheaper than checking.
- **`textFailCeiling` 2 → 0, and it was briefly wrong at 1.** Written from the board's *total* of one
  remaining fail, where the ceiling counts the *text* partition and the survivor is in the binary
  one. Reverting #83's fix left `textFail` at exactly 1, so a ceiling of 1 sat green over the defect
  it was being lowered to catch — found by the falsification pass, not by reading. **A ceiling is a
  claim about a partition, so it is read off the partition, not off the total.**

- **#78's lesson ratified into `CLAUDE.md`** (ruling: Scott, #82): *a guard's trigger predicate is
  itself a claim about the space, and an under-matching one fails silently by construction.* The
  falsifiability law does not reach it — you can break a guard's assertion, watch it fail, and still
  have a guard that never fires on most of its population, because an under-matching regexp produces
  **no finding rather than a wrong one**. Measuring the trigger's *coverage against the population it
  claims* is what finds it: coverage is to a trigger what a vacuity check is to a comparison. Two
  corollaries recorded with it — **registration is not verification**, and **one concept, one
  trigger**. Ratified rather than left in the changelog because the class **recurred one PR later**,
  inside the guard repaired for it: a citation row split across two lines is invisible to a
  line-oriented trigger, so the file registers and contributes zero verified rows (#80).
- **The CI-wait recipe's bounded wait now says *which* negative it hit** (ruling: Scott, #82).
  `ci.yml` triggers on `push` to `main` plus `pull_request`, so a topic-branch push creates no run
  until its PR exists — and the poll loop reported that identically to "the run has not appeared
  yet", two conditions with different remedies. The loop's failure branch now asks whether an open
  PR exists and names the remedy. *A bounded wait that cannot distinguish its own failure modes is a
  timer with better manners.* Found by firing for real on #80, where the first reading was "flake in
  the poll"; both branches of the new discriminator were exercised before it was written down.
- `RunGated(decode, readText, isGated)` and `RunWith(decode, readText, isGated,
  have...)` take the text entry point; `Script.Run` now panics on a quote form,
  declaring nothing and supplying nothing.
- The skip inventory keeps its four doors and its invariant — **one env var revokes them
  all** — and now records the fifth door that was written and withdrawn, with why. The
  header had been edited to weaken the invariant to "every license names its revoker",
  which the withdrawal made unnecessary; both the doc and the guard's failure message are
  back as they were, plus a note on what the message deliberately does *not* offer (a skip
  whose condition the flag cannot revoke). Kept rather than deleted because the near-miss
  is the lesson, and a policy that only records the rules nobody tried to bend has lost
  the interesting half of its history.
- `internal/text`'s package doc swept for what the lexer's landing orphaned: it said the
  package "will hold" a lexer, that the keyword table was "read by nothing but this
  package's own tests" (`lexer.go:385` reads it), and that the consumer was "#53's next
  increment" (#53 is closed). The measurement in the linter-silence section was left in
  place with its conclusion corrected rather than deleted — the risk it named is what the
  lexer's arrival retired, and a measurement whose subject moved is a claim, not a
  measurement.
- **Three lifecycle controls re-pointed by the retirement, each narrower than its own
  name**, and the generalization is worth more than the fixes: *a lifecycle guard
  written while its subject has only ever been in one state will encode that state.*
  Guard 2 asserted "every needed capability has a registry entry", which was right
  until a capability was retired and then reported 1236 errors on the success
  condition; the invariant is now **accounted for — tracked debt XOR declared
  component**, which is stronger than the old reading, not looser. Guard 6's vacuity
  floor stood on the registry, so it would have `Fatal`ed on an empty one — *a control
  asserting the absence of its own success* — and now stands on the declaration.
  `TestQuoteFormsAwaitTheirReader`'s first half dissolved, so it was re-aimed at where
  the risk moved (declared-without-supplied) rather than closed: *a tripwire whose
  subject dissolves is re-pointed, never closed.*
- The `unimplemented` ceiling lowered 1236 → **0**, its terminal value under decision
  0004. Not bookkeeping: a ceiling of 1236 against an actual 0 would permit the entire
  population to reappear in the fourth column without a word — the disappearance
  guard 6 exists to prevent, wearing a ceiling's clothes. Falsified at -1 and watched
  fire.
- Guard 2's panic message named a registry a caller should add an entry to, which the
  retirement had just made false advice. It now has a third case naming the
  retirement and pointing at `RunGated`. *A ruling retroactively falsifies the prose
  written before it* — and so does a retirement.

- **Decision 0003 amended**: its LEB taxonomy prescribed the *wrong test order*,
  and the implementation followed its documentation faithfully — so every reviewer
  who checked the code against its claims found agreement. Appended, not edited: the
  body stands as the record of what was believed and of why it survived review. The
  authority for order-of-tests questions is the reference interpreter's `decode.ml`,
  not a derivation from vectors that cannot distinguish the orderings. Also corrects
  the ADR's `\ff\ff\ff\ff\ff\7f` witness, which is listed under the continuation-bit
  bullet while being sourced from a *signed* field.
- **LEB widths are per field, not one width for the whole decoder.** Limits
  minimum and maximum are read at **64 bits**; indices and counts stay 32. The
  suite brackets it from both sides: `binary-leb128.wast:525`'s ten-byte memory
  minimum wants *integer too large* (ten bytes is legal width for a u64, so the
  fault is the unused payload bits) while `:217`'s eleven-byte field wants
  *integer representation too long*. A u32 read scores the first as "too long"
  and gets the string wrong. The consequence is deliberate: a memory32 minimum
  above 2^32 now **decodes** and is the validator's to reject, which is the
  correct layering — reading the field narrowly to catch it in the decoder would
  be borrowing the validator's job and getting the malformed string wrong to do
  it. Pinned by a bidirectional control the suite supplies for free: the same
  five bytes `80 80 80 80 10` are malformed as a data-segment memory index
  (`:565`) and legal as a limits minimum, so one width being wrong fails the two
  halves in opposite directions.
- **The functype form tag is an `s7`, not a byte.** `0x60` *is* −32 read at
  width 7 — the spec's type constructors live in negative s7 space, as `0x5e`
  (array) is −34 — and `binary-leb128.wast:1067` is the vector that settles it:
  `\e0\7f`, annotated by the suite itself as "−0x20 in signed LEB128 encoding",
  must fail as *integer representation too long* rather than *malformed function
  type*. This is the inverse of the limits-flags rule, where the field really is
  a byte and a redundant encoding of a legal value is malformed limits.
- `reader.u64` has a production caller and its `//nolint:unused` is gone, which
  empties the `deadcode` allowlist to zero entries. This is the placeholder
  discipline's intended ending — a deferral retired by a caller, not by a
  suppression outliving its reason.
  ([#19](https://github.com/scttfrdmn/burroughs/issues/19))

- **`Features.ExceptionHandling` and `Features.SIMD` doc comments now say what the gates
  do *not* yet cover.** Writing out an opcode scope for them is what found
  [#48](https://github.com/scttfrdmn/burroughs/issues/48): the table-driven dispatch
  consults `Features` nowhere, so with every gate off the decoder **accepts**
  `throw_ref`, `try_table`, `v128.const`, and `ref.eq` in a function body — the
  accept-and-ignore half of the gate ruling, unnoticed because every prior gate
  discussion was about not over-*rejecting*. The comment I first wrote asserted check
  sites that do not exist: grave #36's fabricated-evidence shape, moved from a format
  string into a comment, where nothing reads it. *Writing down what a flag governs is a
  check on whether it governs it.*
- `decodeBlockType`'s comment gave the wrong reason for its branch order. `either`
  backtracks, so the order affects neither the accept set nor any extent — measured over
  all 256 first bytes in both orders, 427 of 768 rows differ and **every** difference is
  the error message alone. What the order decides is which branch's error survives, and
  that is load-bearing in exactly one place: the gated branch must be last, or the
  alternation overwrites `ErrFeatureDisabled` with `malformed value type` — a gate
  manufacturing malformedness. The control keeps the wrong reason beside the right one.

- **The board's corpus is derived, not enumerated**
  ([#52](https://github.com/scttfrdmn/burroughs/issues/52)). `phase1Files` was eight
  hand-listed filenames — *the enumerated-literal defect living in the oracle's own input
  selector*, so the board's coverage froze at the moment somebody typed the list and a new
  upstream file with byte-string vectors would never be asked. `boardFiles(t)` now parses
  every vendored `.wast` and selects the ones with at least one command the harness can
  score, which finds **14 files** — six the list never held — and puts **19 more vectors**
  on the board, 8 of them red, which is the point.
- **The board is red by 8, deliberately.** **783 pass / 8 fail / 1345 unsupported / 8
  gated** on the default board, **791 / 8 / 0** with every gate on. The 8 reds are all one
  bucket, `(module binary ...) must decode` — accept-direction findings the byte-string
  corpus could not reach, since a hand-listed corpus of malformed vectors samples only the
  reject direction. One is already [#51](https://github.com/scttfrdmn/burroughs/issues/51).
  *A red board that names its buckets outranks a green board that never asked.*
- **Zero-unsupported was a property of the corpus, not a law of the board** (doctrine
  adjustment, recorded on #52). While the corpus was eight byte-string files the unsupported
  column was necessarily zero; running every file makes it 1345, and that column is the
  honest board now — commands the engine cannot answer yet, counted and visible, shrinking
  monotonically as components land. The underlying law is unchanged: nothing hides behind a
  skip ([#29](https://github.com/scttfrdmn/burroughs/issues/29)).

### Fixed

- **`funcref`/`externref` decoded with the wrong nullability, colliding with `(ref func)`/`(ref extern)` and splitting from `(ref null func)`/`(ref null extern)`** ([grave #180](https://github.com/scttfrdmn/burroughs/issues/180)). #179's `decodeRefType` hardcoded `null: false` for the bare Wasm 2.0 abbreviations, framed at review as retaining the wire's *spelled* nullability rather than its semantic meaning — but the reference's own grammar defines `funcref = ref null func`: the abbreviation *is* the nullable spelling, with no bare byte for a non-null func reference at all. So `funcref == (ref func)` read `true` (should be `false` — different spec types) and `funcref == (ref null func)` read `false` (should be `true` — the same spec type, spelled two ways) — backwards on both relations, with second-order effects on `Nullable()` (whose special-case arm existed only to paper over this) and `String()` (`(ref null func)` printed `"unknown"`). Fixed by decoding the abbreviation to its true nullable meaning and moving `FuncRef`/`ExternRef` to `null: true` in lockstep, so every existing `t == FuncRef`-style comparison keeps compiling and returning the same answer — the two constants and the decoder arm were never independently observable, only their mutual agreement was, and that agreement now means *type identity* rather than wire-spelling identity. `Nullable()`'s special case is removed; `Null()` alone now answers both what #179 asked of it and what it was split off to avoid. A sibling gap swept up in the same pass: `abstractHeapNames` never had entries for `func`/`extern`'s kind bytes, so the genuinely non-null `(ref func)`/`(ref extern)` printed `"unknown"` since 0018's implementation (#179) — not introduced by this fix, found alongside it. The review ruling that shaped #179's implementation shared the cause (reasoned about a re-encoding consumer for spelled-bit retention that measurement shows does not exist) and is recorded as such at the grave.

- **An absolute byte budget wearing a ratio's doc comment: grave #138's control could not see the
  property it names** ([grave #166](https://github.com/scttfrdmn/burroughs/issues/166)).
  `TestDecodeCostIsProportionalToCompressedSize` opened with a paragraph explaining, correctly, that
  the assertion is *a ratio against the declared count* rather than an absolute figure, because an
  absolute one "would be a bound on today's allocator behaviour". The code beneath it was
  `budget := uint64(len(img)) * heapPerImageByte` — an absolute figure, per row. **The comment knew
  and the code didn't**, which is the purest instance yet of *the defect stated as the rule*: review
  verifies code against claims, and here the claim was the thing being violated, so every reading
  concurred.

  Two consequences, and the second is the one that matters. The budget was **process-global** —
  `TotalAlloc` around a single `DecodeModule` charges the decoder for whatever else allocated in the
  window — and the 2^16 and 2^20 rows build the *same 28-byte image*, so both carried an identical
  1792-byte ceiling over ~890 bytes of real signal. One stray 5 KB allocation from a false red, and
  on an unrelated PR's run that is exactly what happened: 6208 bytes on the first row, 888 on every
  other. The failure message then blamed allocation proportional to the declared count while its own
  adjacent rows disproved that — the *rows declaring up to 32768x more locals all read 888* — so the
  instrument was wrong about its own cause. And the comparison the doc's argument rested on was
  **never computed**: five rows each checked against their own ceiling is five single-row
  measurements, and one row cannot distinguish "proportional to the image" from "proportional to N
  with a small constant", which is the reading that lets the defect back in.

  The repair asserts **the relation across rows** — allocation stays flat as N scales — comparing the
  widest row to the narrowest over all five rather than each row to a figure derived from itself
  (ruling: Scott, on the grave). A budget with headroom would only relocate the flip threshold while
  still measuring the process's weather. Flat, the rows are **identical at 888 bytes**; with the
  flattening `make([]ValType, 0, total)` reintroduced they read 66424 through 2147484536 and the
  control fails at **32329.9x against a tolerance of 8**, naming both endpoints. Noise isolation is
  minimum-over-eight-repeats (contamination can only push a sample up), a discarded warm-up decode,
  and automatic GC off with an explicit collect per window — that last measured both ways, since it
  holds peak RSS to **2.16 GiB instead of 8.68 GiB** under falsification, and a control that gets
  OOM-killed names no row. All three guards, including the vacuity floor and the zero-allocation
  check, were watched die individually. *Budget by the quantity the purpose names*
  ([#28](https://github.com/scttfrdmn/burroughs/issues/28)) — the purpose is a relation, so the
  budget is one.

- **A rename row's path broke the ratio classifier, and two independently wrong readings agreed
  to within 0.1** ([#117](https://github.com/scttfrdmn/burroughs/issues/117)). `git diff
  --numstat` renders a rename as `internal/{text/internal => gen}/keywordgen/emit.go` — a single
  field containing spaces, braces, and no usable prefix. The first draft split on awk's default
  FS and shredded it into three fields; the cross-checking probe split on tabs correctly and then
  classified the brace form as **engine**, because it does not literally begin `internal/gen/`.
  Both were wrong in the same direction on the same eleven rows.

  What caught it was the *disagreement*, not either reading: 0014 came out 1:2.8 from the script
  against 1:2.7 from the probe, a 0.1 gap between two supposedly equivalent instruments, on the
  one merge in forty that contains rename rows. Had the two mistakes coincided exactly, a wrong
  figure would have shipped with a confirmation attached. `destination()` now resolves the row to
  the path the code lives at, and the three quoted windows are unaffected because none of them
  contains that merge — which is itself the reason the defect was survivable long enough to find.


- **A one-of-two-conditions exemption panicked in the arm that asserts two callers agree**
  ([grave #146](https://github.com/scttfrdmn/burroughs/issues/146)). `encodableOrErr` exempts
  element segments that write an `elemkind` byte rather than a reftype, and asked only
  `isElemKind()` — while the writer's family test is `isElemKind() && allElemIndex()`. A
  `(ref func)` segment holding an element that is not exactly `ref.func x` was therefore exempted
  from the frontier check and then routed to the *expression* family, which calls `w.valType` on a
  type `valTypeByte` refuses: not a wrong image but a **panic**, on three spellings the grammar
  admits. The exemption is now the writer's conjunction verbatim. **A predicate reconstructed from
  one of two conditions is the under-matching trigger defect
  ([#78](https://github.com/scttfrdmn/burroughs/issues/78)) wearing a skip instead of a guard**,
  and it fails the same way: silently, producing no finding, until the population it wrongly
  exempts is reached. Found by enumerating mode × reftype × element shape, which became
  `TestEncodeMatchesTheReferenceOnElemFlags`.

- **A sink installer asked whether a sink was already installed, silently dropping every element**
  ([grave #144](https://github.com/scttfrdmn/burroughs/issues/144)) and **a flat
  `select`/`call_indirect` emitted after its tail, denoting a different program**
  ([grave #145](https://github.com/scttfrdmn/burroughs/issues/145)).

- **The `indirect call type mismatch` trap quoted a type in the wrong notation, and its comment
  cited a vector that says nothing about it**
  ([grave #147](https://github.com/scttfrdmn/burroughs/issues/147)). `funcTypeString` rendered the
  **wat** spelling — `(func (param i32) (result i32))` — while its doc comment claimed that was
  `string_of_deftype`'s and that empty clauses were "dropped". Both halves were false: the
  reference is `"func " ^ string_of_resulttype ts1 ^ " -> " ^ string_of_resulttype ts2`
  (`types.ml:382-383`) and `string_of_resulttype` brackets **unconditionally** (`:361-362`), so the
  empty functype is `func [] -> []`, not `(func)`. All 25 mismatch vectors stop at the sentinel, so
  nothing in the suite reads a character of it — grave
  [#36](https://github.com/scttfrdmn/burroughs/issues/36)'s territory exactly, and the reason the
  fix is to agree with an *external authority* rather than to invent locally. The comment also
  claimed `call_indirect.wast:552` "wants" the tail; that line is a `fib-i64` assert_return, and
  the citation was invented in the direction of claiming oracle cover for the half of the message
  that has none. `TestFuncTypeStringIsTheReferenceSpelling` pins the algorithm, empty case
  included.

- **A 20-byte module allocated 2.64 GiB: `decodeLocals` expanded a declared count before
  anything bounded it** ([grave #138](https://github.com/scttfrdmn/burroughs/issues/138)). The
  local declarations are run-length on the wire (`count, valtype`) and the decoder flattened them
  into one `ValType` per local. The guard was `total >= 1<<32` — the reference's bound, and right
  about the *verdict* — so `0xFFFFFFFE` locals was **admitted** and, at a byte per slot, a 30-byte
  image decoded successfully into **4.00 GiB**. Found by `fuzz-smoke` on CI, where four workers
  each holding gigabytes exhausted a 16 GB runner; the failure surfaced as `fuzzing process hung
  or terminated unexpectedly: exit status 2`.

  **`binary.Func.Locals` now retains the wire form** — `[]LocalGroup`, one entry per run — with
  `TotalLocals()` for the flat count and `EachLocal()` iterating the flat reading without
  materializing it. Decode cost is proportional to the *compressed* size; the sum check stayed
  exactly where it was, because it was never the defect. The flat vector is paid for by the one
  consumer that needs slots, `interp`'s frame builder, and bounded there by `maxFrameLocals`
  (2^24, so 128 MiB of slots) reported as `ErrUnsupported` — an engine limit, since the module is
  well-formed and the spec gives no trap for it.

  **Verdict-preserving, and the board proves it: 26307 / 5349 / 32377 / 1031, unchanged.** That is
  the point rather than a footnote — every one of these modules is spec-legal, so refusing them to
  save the memory would trade a resource bug for an accept-direction one (§9 G-3), which is
  strictly worse and invisible on a board whose vectors only probe this bound from above
  (`binary.wast:159`, `:175`). Measured effect on the target that found it: **3M executions in
  7.4s at 222 MiB peak, up from 2.5M in 59s before the kill** — the old code was spending its
  throughput expanding local counts, 46k → 465k execs/sec.

  Two lessons, and the second is the one worth carrying. First: *a count check that is right about
  the verdict can still be wrong about the resources*, and no vector can see the difference —
  the suite is the oracle for verdicts and silent on cost by construction. Second: **the rule was
  right; only its extent was wrong.** The hazard was *stated at the site* — the old comment
  explained that flattening waits for the sum check so four billion entries are not allocated for
  a module the next line refuses — and that is true of `0xFFFFFFFF` and silent about
  `0xFFFFFFFE`, the neighbour one line later that the check **accepts**. A boundary comment that
  does not state its extent reads as a proof, which is why review confirmed it. Boundary comments
  now state their extent.

  The regression guard is a **property assertion, not a committed crasher**, and that refines the
  standing rule rather than breaking it: the input does not reproduce as a single-input replay (one
  worker at 2.6 GiB returns `ok`) and as a seed it would tax every `make check` 2.66 GiB of peak
  RSS while asserting nothing. Refined form — *a crasher is committed when its replay asserts the
  defect; when it cannot, the guard is a property assertion instead.*
  `TestDecodeCostIsProportionalToCompressedSize` bounds heap against **image** size across 2^16 to
  2^31 declared locals, so the *scaling* is what is measured; a single row cannot tell
  "proportional to the image" from "proportional to N with a small constant".
  `TestFrameLocalsCeilingRefusesRatherThanAllocating` covers the execution half. Both were
  falsified before landing — the first fails on all five rows with the old flattening restored, the
  second on exactly the two rows past the ceiling.

- **A `return` did not truncate the value stack, so a valid module was rejected as unvalidated**
  ([grave #135](https://github.com/scttfrdmn/burroughs/issues/135)). `eval.ml:1069` is
  `take n vs0` — a return truncates to the frame's arity, exactly as a branch to a label does,
  because a function body *is* an implicit labelled block whose arity is the function's result count.
  The arm returned without touching the stack, so `(i32.const 1) (return (i32.const 2))` in a
  `(result i32)` function left two values and `Invoke`'s arity check then reported
  `module reached the interpreter unvalidated` on a module the reference accepts. Three sites shared
  the defect: `opReturn`, `opBr` at the outermost depth, and `opBrIf` in the taken case at that depth.

  **The defect was stated as the rule**, which is why review could not find it: the arm's comment
  argued that "the values below the results belong to no one once this function is done" — true of
  the *frame*, false of the arity check, which counts what is left. Accept-direction, so no
  `assert_invalid` vector could see it; the reject-direction board could not either, because it
  scored the three `comments.wast` modules as fails under a perfectly honest-looking layering-debt
  string. Found by reading the reference while draining the `0f return` bucket — a new error head
  appearing on a module the reference accepts is not a layering debt. Fixed at all three sites, and
  the four new rows in `TestControlFlowSemantics` were each watched to fail under the reverted arm.

- **An `else` popped no label, so a taken then-arm leaked one and the function body ran off its end**
  ([grave #134](https://github.com/scttfrdmn/burroughs/issues/134)). `opElse` is reached only by
  *falling out of* a then-arm that ran to completion, and its `cont` is past the END — so the END
  that would pop the label is jumped over and the pop has to happen at the ELSE. The arm asserted
  the opposite in its comment ("the label stays on the control stack: the END past the else-arm is
  what pops it") and described a mechanism the same line then skipped, leaving one label per taken
  then-arm. The function's own terminating END then saw a non-empty `ctrl`, took itself for a block's
  END, and the body ran past its last instruction: `function body ended without END` on a valid
  module.

  **It survived 20 of 22 rows of the control written for it**, because the encoder refused all 20;
  the two rows that could reach it are exactly the two with a taken then-arm *and* an else-arm
  present. The falsifiability law's own blind spot — a green that survives the bug it names — and it
  was cleared by the artifact the control was waiting on rather than by more control.

- **A control's printed figures and its comment's figures are two witnesses, and only one gets
  re-measured** ([grave #132](https://github.com/scttfrdmn/burroughs/issues/132)).
  `TestRetainedFormOverAcceptedModules` logs its population every run and *also* states it in prose;
  the prose said "74 accepted modules across 253 files, carrying 4 function bodies and 18
  instructions" while the log has printed **27 bodies and 47 instructions** since the instruction
  grammars landed, and the file count was never right at all — that loop walks `suitePaths`, every
  vendored file (**257**), not the board-selected set. Found while sweeping a *different* number, the
  board's own 253 → 254.

  Nobody re-read it because the truth was printed beside it: a figure a control reports live and a
  figure its comment asserts are two claims about one fact, and only the first is re-derived every
  board. The prose is corrected here; the **floors** carrying the same stale values (`instrs ≥ 15`
  against an actual 47 — an unasserted distance) are filed rather than raised, because re-pinning
  four bounds is instrument work and this PR is the product work that walked past them. What the
  issue owns is the sweep, the class being wider than one file.

- **Two gate-allowlist reasons stated a memory count nobody re-derived: three memories documented as
  five and as four** ([grave #129](https://github.com/scttfrdmn/burroughs/issues/129)).
  `memory_trap0.wast:1` was recorded as "five memories at :1" and defines three;
  `memory_fill0.wast:2` as "four" and defines three. Both came from counting the literal `(memory` in
  the source, where `(memory.size $m)`, `(memory.grow …)`, `(memory.fill $m …)` and
  `(data (memory 2) …)` spell those characters **without defining a memory** — three mnemonics and a
  reference into the index space.

  The entries named the right *feature*, so they did their allowlist job and review had no reason to
  look past them; only the supporting number was false. That is #114's class with the identifier
  replaced by a count, and it survived the allowlist's own "verified by reading each module head"
  discipline because the head was read for the feature and the memories were counted off the same
  text the emitter reads. The counts are now the **decoder's** — `len(Module.Memories)` plus the
  memory imports, all gates on — which is the index space `memarg flags bit 6` is a statement about,
  so the reason's number and the reason's claim are one measurement. *Measure with the instrument,
  not a regex.*

- **`i32.const` did not occupy its slot: the four `const` opcodes shared one arm, and 114 spellings
  converted wrongly with the board unmoved**
  ([grave #125](https://github.com/scttfrdmn/burroughs/issues/125)). The decoder stages an s32
  immediate **sign-extended to 64 bits** (`immS32`), while an i32 slot is defined as the low 32 bits
  with the high bits *zero* — `i32.const -1` is `0xFFFFFFFF`, not `0xFFFFFFFFFFFFFFFF`. One
  `case 0x41, 0x42, 0x43, 0x44` pushing `Imm0` unexamined is correct for three of them and wrong for
  the fourth, on every negative constant.

  **The board could not see it, and that is the entry's point.** 114 of the corpus's 6498 distinct
  const spellings were wrong and the pass count moved by nothing: 17923/14330/32764 before the fix
  and after it, byte-identical. Two reasons compounded — the vectors that would have compared a
  negative i32 were already failing on the encoder's instruction frontier (#8), and the ones that were
  not compare against `spec.readIntLit`, a *second* reader that was right. So the defect was
  simultaneously live and invisible, which is exactly the accept-direction class §9 G-3 names and the
  reason two independently-derived literal readers are worth their duplication. What found it was
  `TestHarnessAndEngineLiteralReadersAgree` on its first run. The engine agreeing with itself is not
  evidence.

  **The tell was a comment asserting the property its code lacked.** The arm's own header said the
  decoder stages i32.const sign-extended *and* that pushing `Imm0` unexamined was correct — two claims
  that cannot both hold — so every reviewer who checked the code against its documentation found
  agreement. *The defect stated as the rule*, which is the strongest camouflage a bug can wear.

  The reproducer is `TestI32ConstOccupiesItsSlotZeroExtended`, partitioned by *observation path* out
  of the slot rather than by input, because a fix that truncated at `Invoke` would satisfy a
  boundary-only assertion and leave the stack wrong. Falsifying it corrected the test's own
  documentation: the draft called `i64.extend_i32_u` the sharpest witness, and reintroducing the
  defect showed it green — `extend_i32_u` goes through `popI32`, which truncates, so every path
  through the `popI32`/`pushI32` helpers was protected all along. Three rows fail (direct,
  through-a-local, the hex spelling), the rest mark where the damage stopped.

- **Two accept-direction defects in the code section, both caught by the wabt corpus at one byte and
  invisible to all 4162 vectors** ([#8](https://github.com/scttfrdmn/burroughs/issues/8),
  [#77](https://github.com/scttfrdmn/burroughs/issues/77)). Not graves — the corpus found them inside
  the PR that wrote them, which is the accept-direction control doing the job it was made product work
  for. Both are the shape §9 G-3 names: a well-formed image that decodes clean and denotes a different
  function.

  **A label immediate was emitted as nothing at all.** `br 0` wrote `0x0c` with no operand, so the
  body's terminating `0x0b` was consumed as the immediate — `token#5`, 26 bytes against wabt's 27. The
  regression row is `(module (func br 0(nop)))`, and the trailing `(nop)` is the part with teeth:
  without an instruction after the `br`, a missing immediate eats the `end` and *still* decodes, which
  is exactly how it survived. A second row pins a non-zero depth and `br_if`, so the first is not a
  fixed point.

  **A symbolic local resolved one slot too low whenever a typeuse supplied the params.**
  `(func (type $sig) (local $var i32) (local.get $var))` encoded `$var` as slot 0 where `$sig`'s param
  owns 0 — 77 bytes agreeing with wabt everywhere except `20 00` against `20 01`. The frontier now
  refuses that case in `retainIdx` rather than emitting it, and the refusal is *narrow* deliberately:
  refusing every typeuse cost six encodable modules including a row already in the round-trip table, so
  the predicate is "a symbolic local resolved against a possibly-short space", not "a typeuse". It
  still over-refuses a param-less type, which is pinned as its own row so that #77 landing without
  reaching the predicate is a visible failure rather than a silent one.

- **Three duplicate-name errors named the wrong space, and a substring match hid the one that had
  vectors** ([grave #120](https://github.com/scttfrdmn/burroughs/issues/120)). The reference says
  `duplicate function`, `duplicate data segment`, `duplicate elem segment`; Burroughs said `duplicate
  func`, `duplicate data`, `duplicate elem`. The func case has suite vectors and passed all of them,
  because the harness matches expected strings by **substring** (decision 0003) and `duplicate func`
  is a prefix of `duplicate function $foo` — so the oracle read exactly as far as the expected string
  did and no further, which is the invented-evidence class with the verdict coming out right. The data
  and elem cases have **zero** vectors in either direction. Fixed by writing the category word once
  per space in `newContext` and pinning all nine against parser.mly's `lookup`/`bind` tables, so the
  words are derived from the authority rather than transcribed beside each use.

- **Nine valid modules were rejected, and the comment above the bug asserted the rule it broke**
  ([grave #109](https://github.com/scttfrdmn/burroughs/issues/109)). `constOps`' comment said
  extended-const "arrives with its gate" while no such gate existed and `constLegal`'s predecessor
  returned false for all six opcodes, so `(data (i32.add (i32.const 0) (i32.const 42)))` and its
  siblings in `data.wast`, `elem.wast`, and `global.wast` failed with `constant expression required:
  0x6a/0x6b/0x6c`. **Invisible on both boards by construction**: all 4162 green vectors are
  rejections, so a decoder that wrongly rejects scores full marks (§9 G-3), and the all-on lane
  scores the same. Found by the corpus simply trying to read the images; reproduced from a clean tree
  by reverting `constLegal` to `return false`, which yields exactly those nine. *The defect stated as
  the rule* is the shape — review verifies code against claims, and here the claim was the bug.

  Three collateral repairs of the same shape — greens whose subjects were fiction — each ruled a grave
  in its own right ([#114](https://github.com/scttfrdmn/burroughs/issues/114),
  [#115](https://github.com/scttfrdmn/burroughs/issues/115),
  [#116](https://github.com/scttfrdmn/burroughs/issues/116)) with a comment at each fix site.
  `TestAgreementHoldsUnderEveryFeatureConfiguration` claimed its four booleans were "the same
  derivation without a dependency" while walking **4 of 8** gates, pinning the other four off in all 16
  configurations — it now reflects over `Features` (2^9 = 512), and the lesson is that an enumeration
  wearing a *derivation's* description is worse than a bare enumeration, because the description
  defeats the review that would have caught it. `TestEveryGateOffDeclinesSomething`'s probe struct
  documented a `body`/`constExpr` selector that was **not a field**, which is why no probe could reach
  a const position; the tell was *tense* — a capability the code lacks belongs in the future tense, or
  in an issue, or nowhere. And three comments cited `constWalk`, a function that has never existed in
  the package through three PRs — *a citation to a symbol is as checkable as a `.wast:N`*, and nothing
  was checking these.

  **The new control was itself born stillborn, which is the part worth keeping.**
  `TestEveryCorpusModuleDecodesUnderFullFeatures`' floors measured `len(m.Modules)` — the population —
  and said nothing about whether the loop consumed it, so `range m.Modules[:0]` left it **green**.
  Found by writing the falsification and watching it *not* fire (*a control isn't born until it has
  been watched die*), fixed with a `walked` counter, and re-falsified: the injection now reports
  `walked 0 of 1954`. The sweep to its sibling came back **negative and is recorded as such** — that
  walk's floor is on *declines*, which can only accrue from iterations that happened, so a second
  counter there would be one concept with two triggers.

- **`limits`' nats were never read, so a 2^64 bound was accepted**
  ([grave #112](https://github.com/scttfrdmn/burroughs/issues/112)). Both arms are `nat64`
  (`parser.mly:467-468`), so `(memory 18446744073709551616)` is `i64 constant out of range` *from the
  parser* upstream; the reader advanced the cursor and read nothing, at four call sites — memory and
  table, defining and imported. Two independent silences kept it invisible: no vector writes a 2^64
  limit at all, and an over-acceptance has no `assert_malformed` that can complain about it. Board
  delta zero, before and after.

  **The lesson is indexed by shape:** *a reject-only reader that advances past a literal cannot
  enforce the literal's width.* Where a production's semantic action **is** a conversion
  (`nat8`/`nat32`/`nat64`/`num`/`vec`), skipping the token skips the production's entire content. The
  class-level tell was in the signature — every other NAT-consuming reader in the package names its
  width (`nat32`, `laneidx`, `constImm`'s width-from-the-mnemonic, `memarg`'s two 64-bit checks) and
  `limits` named a structure. The sweep found `limits` was the only one of seven missing it, and the
  negative space is the finding: the readers whose *names* carry a width all had the check.

  Found the way accept-direction defects in this stratum are always found — **something finally
  needed the value.** Stated as a method, since it recurs (#75, #88): *a reader that discards cannot
  be audited by any suite we have*, so the repair for a hand-trusted fact is to give the value a
  consumer. `TestLimitsNatsAreCheckedAtSixtyFourBits` is scoped to all four call sites and watched
  die three ways: reject-only restored (10 rows), `nat32` substituted (10 rows *on the message* plus
  7 accept rows), and fixed at the two inline sites only — which fails exactly the 4 import rows a
  control written against `(memory N)` would never have had.

- **Two facts the code got right and nothing asserted, both turned up by #112's sweep.** Neither was
  a defect; both were claims. The pattern they share with the grave is the point — a comment saying
  *no vector covers this* discharges nothing, because the missing vector is exactly why a control has
  to stand in for it.
  - **`abbreviatedReftypes`' twelve-row pairing was prose until the emitter existed.** A swapped
    expansion (`structref` → `ArrayHT`) parsed identically and no spelling of a test could see it,
    because every caller of `reftype` discarded the value. Now
    `TestEveryAbbreviatedReftypeExpandsAsItsTableClaims` iterates the table: the ten refused element
    types are judged by the heap type their refusal names, and `funcref`/`externref` by the 0x70/0x6F
    `encode.ml` writes — the one channel whose authority is not a string we produced. Falsified three
    ways (refusal pairing, binary byte, table truncated to nine).
  - **The module-field `idx` width was checked in code and asserted nowhere.** `types.go`'s comment
    named the oracle gap — "no vector puts an over-wide index there without an instruction body" —
    and left the property untested for as long as that stayed true.
    `TestModuleFieldIdxIsCheckedAtThirtyTwoBits` covers eleven positions (five `export_desc` arms,
    `start`, `elem declare`, `data`'s and `elem`'s nested indices, `subtype`'s `idx_list`,
    `typeuse`), rejecting at 2^32 and
    accepting at 2^32-1. Substituting `nat64` fails all eleven; the accept half is what makes that
    distinguishable from rejecting long digit strings, and it is the half that would catch someone
    reaching for `nat64` here because `limits` uses it one grave over.

- **A comment asserting extended-const "arrives with its gate" when there is no such gate**
  ([#109](https://github.com/scttfrdmn/burroughs/issues/109)). `constOps`' comment said
  extended-const and WasmGC both "are gated, so they arrive with their gates"; `Features` has
  eight fields and none is extended-const, so `i32.add` in a constexpr is rejected outright with
  `constant expression required` on **nine modules the suite requires accepted**
  (`data.wast:178`, `elem.wast:1057`, `global.wast:3`, and six more). The claim read as
  *declared and tracked* and licensed the omission — the defect stated as the rule, since review
  verifies code against claims. The comment is corrected here and the remedy is flagged for
  Scott, because G-2 does not name extended-const though it is Wasm 3.0 core; either way the
  string is wrong, a declined feature reporting a spec `invalid` string (#5).

  **This is what the cross-check corpus was built to find, and it found it on the first run.**
  Also a second-order lesson: 58 compact-import rejections and these 9 appeared together under
  one flag change and were attributed to one cause. The first bucket vanished when the flags were
  corrected and this one *survived* — `--enable-extended-const` is measurably inert in wabt (0
  modules, 0 bytes), so these are baseline corpus content. Only re-measuring after the fix
  separated the two causes.

- **Three accept-direction defects in 0014's join, none of them findable from the board**
  ([grave #105](https://github.com/scttfrdmn/burroughs/issues/105),
  [grave #106](https://github.com/scttfrdmn/burroughs/issues/106),
  [grave #107](https://github.com/scttfrdmn/burroughs/issues/107),
  [#8](https://github.com/scttfrdmn/burroughs/issues/8)). All three found by *printing what
  the reader returned for named inputs*, not by reading code; all three would have scored
  green on every board, since a wrong row emits a well-formed module that decodes clean.
  Each is a distinct class, and the middle one is the one worth the space:

  **#105 — an under-matching trigger, 411 rows where 436 were measured.** The lexer arm-head
  regexp required `-> TOKEN` on one line, false of the 25 arms that wrap; they were absorbed
  as continuation lines of the preceding keyword. `keywordgen` had already met and solved this
  (its head ends at `->`), and the defect was reintroduced because the shape was **re-derived
  instead of copied**. The floors were green throughout — 411 clears 350 — so exact counts now
  sit beside them, and `reLexArmish` makes an arm-shaped line that fails to parse an error.

  **#106 — the ADR's own premise was measured with the code's blind spot.** The grammar reader
  read `plaininstr`, one of five instruction-building productions, and 0014's "overlap 0,
  gap 0" was measured by a probe *scoped to the same production*. So premise and code agreed
  while both were wrong, and every control-flow instruction — `select`, `block`, `loop`, `if`,
  `call_indirect`, `return_call_indirect`, `try_table` — joined to nothing. **A premise
  measured over the same sample the code reads is not a premise, it is an echo**; and the tell
  was available at the time and not taken, because a gap of *exactly* 0 should prompt the
  question of what the counting instrument cannot see. The repaired gap control therefore uses
  a detector drawn from outside both readers, since asking whether every kind the join resolved
  was resolved is the tautology that hid the seven.

  **#107 — a sound filter widened past the region that made it sound.** `constructorIn`
  identifies a constructor by asking which lowercase identifier the *opcode table* holds, which
  is what avoids parsing OCaml. Fed the whole arm rather than just the action, it matched
  `block` in `| LOOP labeling_opt block END labeling_end_opt` — the **nonterminal**, which is
  also a constructor at 0x02. `loop` would have encoded as **0x02**. Grammar nonterminals share
  the reference's naming convention with its constructors precisely because both name
  instructions, so the filter's soundness was a property of its input region and nothing said
  so.

  A fourth, in the controls rather than the reader: **`Extract`'s partition check was a
  no-op** — `byGrammar` is keyed per kind and `byLexer` per keyword, so `byLexer[kind]` asked
  whether a keyword is spelled `BINARY`, never true. It read as a partition check and could not
  fire on any input. Found by writing its falsification test and watching it *not* fail: *a
  green that survives the bug it names is a control in name only.*

- **The retained sequence dropped every block terminator, so `(func)` decoded to nothing at
  all** ([grave #99](https://github.com/scttfrdmn/burroughs/issues/99),
  [#7](https://github.com/scttfrdmn/burroughs/issues/7)). `expectEnd` read END, judged
  it, and discarded it at all three call sites — while `structural`'s comment claimed the
  opposite in so many words: *"its own terminator is emitted by the recursive
  `block`/`expectEnd` pair below, which is why END appears in the retained sequence at all"*.
  Nothing emitted it. **23 of 27 bound functions decoded to a zero-length body**, and a
  block's extent is not derivable from its header, so the interpreter would have had to
  recompute extents by re-walking — a second opinion about the program's structure.

  The same read found ELSE dropped one instruction over, which is worse: an `if`'s two arms
  have no declared lengths, so without the delimiter they are one undifferentiated run and
  nothing downstream can recover the split. An `if` whose arms cannot be told apart executes
  the wrong one, on valid input.

  `endTerminator` keeps the split that made the merged version a bug — the verdict stays in
  the free function, the emit sits past it — so a rejected terminator cannot leave a
  fabricated END behind. Both directions of the ELSE fact are pinned, since emitting one
  unconditionally is as wrong as dropping it.

  **The defect stated as the rule, which is the strongest camouflage a bug can wear**: every
  reviewer checking the code against the claim finds a `block` call and an `expectEnd` call
  exactly where the sentence says they are. What found it was an assertion over the accept
  population, not a reading.

- **Eight SIMD lane instructions were decoded as different instructions than the module
  contains** ([grave #100](https://github.com/scttfrdmn/burroughs/issues/100),
  [#7](https://github.com/scttfrdmn/burroughs/issues/7)). `v128.load8_lane` and
  its seven siblings are `memop` followed by `laneidx`, and `memop` stages two words of its
  own — so the lane index arrived as a third and `stage`'s two-slot switch discarded it. A
  shuffle operating on the wrong lane, silently, on valid input.

  Found by *printing* each row's staged-word demand rather than by trusting the sentence "no
  arm stages more than two", which was a comment I had written and which was false.
  `stageLaneIdx` packs the index above the memory index — offset u64, memory index u32,
  laneidx u8 is 104 bits of the available 128 — so 0002's two-word form stays sufficient for
  the whole table. `TestInstrImmediateWidthCoversTheTable` counts **bits, not slots**, because
  a slot count would reject this correct reader.

  **The control was vacuous on the defect it named, and only falsification found that.**
  Reverting the fix left it green: the packing assertions called the helper directly, proving
  the helper packs while the defect was that *nothing called it*. Re-pointed at the reader's
  real declared immediates, it fails correctly.

- **`Instr.Op` was a byte, which truncated the 0xfd region's high opcodes into other
  instructions** ([grave #101](https://github.com/scttfrdmn/burroughs/issues/101),
  [#7](https://github.com/scttfrdmn/burroughs/issues/7)). An opcode is one
  byte, which is the reasonable-sounding ground the first version stood on — and the 0xfd
  sub-table reaches **0x113 (275)**. Found by printing the sub-tables' maxima (`opTableFB`
  0x1e, `opTableFC` 0x11, `opTableFD` **0x113**) instead of trusting the word "opcode".
  `TestPrefixedSubOpcodesFitOp` reads the capacity off the field rather than restating it,
  and is scoped to every row of every region rather than to the one that overflowed.

- **Two deferral citations that no longer led to the work they deferred**
  ([#22](https://github.com/scttfrdmn/burroughs/issues/22),
  [#95](https://github.com/scttfrdmn/burroughs/issues/95)). Found by sweeping every `#NN` cited in a
  Go comment against its issue state, after the board reached 0 fail and the next work had to be
  read off deferrals rather than off buckets. `checkCounts` still said the
  `ErrDataCountRequired` half was "tracked in #22 rather than guessed at", which #39's code-section
  grammar had made false — the check is reachable at `binary.go:775` and both vectors
  (`binary.wast:302`, `:325`) pass inside 127/127. And the tag section's missing payload grammar was
  deferred to **#8**, the wat-harness issue, which owns none of it: no EH-gate issue existed at all,
  so a declared deferral was in substance *untracked*. Now #95, with the gate-census row
  (`gatemap.go:211`) as its drift check. Neither is a grave — *unreachability is a grave only when
  it's silent* — but a tracking number that cannot be followed to the work is the declared-and-tracked
  test failing on its second half, and **a citation nobody re-checks is a claim**.
- **`valtype` was a flat switch over seven bytes where the reference reads a three-way alternation**
  ([#88](https://github.com/scttfrdmn/burroughs/issues/88)). `valtype` is `either [numtype; vectype;
  reftype]` (`decode.ml:220-225`) and `reftype` alone has fourteen forms, so the switch reported
  `malformed value type` for ten GC value types and both parameterized `(ref ht)` prefixes —
  **twelve wrongly-rejected constructs**, at every functype parameter, global type, and array or
  struct field in the format. Three functions now, one per branch, and accept at that position goes
  **7 → 17** with every gate on. The invented sentinel is gone with it: there is **no `malformed
  value type` string anywhere in the interpreter**, and because `either` returns the *last* branch's
  error, a byte that is no value type at all is reported as `malformed reference type` — the
  reference's answer, counter-intuitive enough that it is pinned rather than left to be re-derived.
- **`ref.null`'s immediate was read as a `reftype` where the reference reads a `heaptype`, and the
  wrong reader was wrong in both directions** ([#88](https://github.com/scttfrdmn/burroughs/issues/88)).
  The two productions are not nested — each has an arm the other lacks — so one substitution
  under-accepted and over-accepted at once: `heaptype`'s first branch is a **type index**
  (`decode.ml:182`), which `reftype` has no arm for, so `ref.null 0` was rejected `malformed
  reference type: 0x00`; and `reftype`'s `-0x1c`/`-0x1d` prefixes are absent from `heaptype`, so
  `ref.null (ref null extern)` **decoded**. Accept at that position goes **12 → 76**. Only the
  under-accept was in the diagnosis; the over-accept turned up from pointing the probe at the *fix*
  rather than at the defect, which is the argument for doing that. `decodeHeapType` gains its own
  gate checks, because the premise that let it skip them — "reached only from `decodeRefType`,
  which gates first" — died the moment `immHeapType` began calling it from a Wasm 2.0 opcode: *a
  deferral outlives its reason silently*. The type-index gate sits **after** the negativity check,
  since `either` propagates declines without backtracking and a check ahead of the discriminator
  would decline `ref.null extern` as a GC construct.
- **Two segment sentinels named a field the format does not have**
  ([#88](https://github.com/scttfrdmn/burroughs/issues/88)). `ErrMalformedElemFlags` and
  `ErrMalformedDataFlags` said "flags" where the reference says `malformed elements segment kind`
  (`decode.ml:1201`) and `malformed data segment kind` (`:1223`). The *grammar* was already right, so
  this is message-direction only — and it is pinned at the raise sites rather than by the sentinel
  inventory, because `malformed element kind` (`:1157`, the one-byte `elem_kind` nested inside the
  segment) is confusably similar and an existence check upstream stays green with the two swapped.

  **All four members are accept- or message-direction, so the board did not move: 4162 / 0 before
  and after**, 4178 / 0 / 0 all-on. The controls were falsified by re-introducing each defect, and
  the measurement worth keeping is what *else* fired — **four of six had no other witness in the
  package, and five of six left the spec board entirely green**. The two exceptions prove the shape:
  a defect in the reject direction gets noticed by five tests and turns the board red, one in the
  accept direction is alone with whatever control was written for it.
- **The type section decoded `functype` where the reference decodes `rectype`, four levels up**
  ([#86](https://github.com/scttfrdmn/burroughs/issues/86)). `decodeFuncType` was the whole section
  grammar; the reference's is `rectype` → `subtype` → `comptype` → `fieldtype`
  (`decode.ml:243-276`), and `functype` is *one arm of the third level*. Six functions now, one per
  production, so `rec` groups (`0x4e`), both `sub` forms (`0x50`, `0x4f`), `structtype` (`0x5f`) and
  `arraytype` (`0x5e`) are decoded rather than reported as malformed forms — and `mutability`
  (`decode.ml:154-158`) becomes **one function with two call sites**, `fieldtype`'s and
  `globaltype`'s, where the engine had transcribed it at the global position only. That missing
  second call site was grave [#83](https://github.com/scttfrdmn/burroughs/issues/83)'s shape exactly:
  eight `global.wast` vectors scored the path that worked, which is the configuration that makes the
  other one invisible. **Board 4162 pass / 0 fail on the default lane and 4178 / 0 / 0 with every
  gate on — zero fails on both lanes for the first time**; `binary-gc.wast:1` moves `fail → gated`
  (not `fail → pass`: an array type is GC's, so with the gate off the module is honestly declined
  *for the feature*, and the +1 shows in the all-on lane).
- **`ErrMalformedFuncType` was a string the reference never emits, anywhere**
  ([#86](https://github.com/scttfrdmn/burroughs/issues/86)). `comptype`'s fallthrough is `malformed
  definition type` (`decode.ml:259`); the engine said `malformed function type`, which
  `grep -r` finds **zero** times in the whole interpreter. **No suite vector asserts either string**,
  so the board was blind to it by construction — grave
  [#36](https://github.com/scttfrdmn/burroughs/issues/36)'s class one layer out, a fabricated
  *sentinel* rather than a fabricated byte. Replaced, along with a new `malformed storage type`
  (`:234`) for `packtype`'s fallthrough. The control that pins it reads `decode.ml` and is scoped to
  **every sentinel `binary.go` declares**, not to the strings this fix touched — which is how three
  more were found, now [#88](https://github.com/scttfrdmn/burroughs/issues/88).
- **`either` backtracked feature declines, so a gate could manufacture malformedness at an
  alternation** ([#86](https://github.com/scttfrdmn/burroughs/issues/86)). A v128 array field with
  SIMD off reported `malformed storage type: 0x7b`: the alternation rewound past a *configuration*
  answer and let the last branch's *grammar* answer stand, which the
  [#5](https://github.com/scttfrdmn/burroughs/issues/5) ruling forbids in those words. The remedy
  used at `decodeBlockType` — put the gated branch last — is unavailable here, because the reference
  puts `valtype` **first** in `storagetype` and that order decides the neither-case message. So
  `either` propagates `ErrFeatureDisabled` instead of treating it as "these bytes are not this
  production". Accept sets measured **identical** over all 256 first bytes at both alternation sites
  in three lanes; the only rows that change are ones the engine was answering with a spec
  malformed-string where its own configuration was the reason. Found by probing the gate/`either`
  interaction in code this same PR had just added, and **reverting it leaves the entire package
  green** — no vector reaches a v128 storage type, so its control is the only cover.
- **`scanAnnotBody` had an arm the reference's `annot` rule does not have, and lacked one it does**
  ([#83](https://github.com/scttfrdmn/burroughs/issues/83)). `annotations.wast:1` — a **must-succeed**
  module — was rejected with `empty annotation id`, taking the whole file's leading vector with it.
  **Board 4161 → 4162 pass, 2 → 1 fail**; `annotations.wast` 70/71 → **71/71**. The remaining board
  failure is `malformed mutability` in `binary-gc.wast`, a `(module binary ...)` vector, so the *text*
  partition of the fail count is now **0** and `textFailCeiling` asserts it.

  `token` has **three** `"(@"` arms (lexer.mll:821, :825, :829); `annot` has **two** (:850, :855).
  There is no bare-`(@` error arm inside an annotation body, so `(@)` nested in one is not malformed
  — it takes `| "("` and the `@` becomes a `reserved` atom, which is exactly why
  `annotations.wast:16` writes `(@a @ @x (@x) (@x y) (@) (@ x) (@(@(@(@)))))` *inside* a module the
  suite expects to read. We had transcribed the arm from the wrong rule. Every arm we *did* copy was
  right, which is the lesson: **a missing arm is invisible to a diff of the arms you copied**, so two
  rules sharing most of their arms are read arm-by-arm or not at all.

  The sibling defect, found by the same sweep: the nested `"(@"(string)` arm calls `annot_id` in the
  reference (:858) exactly as the token-level arm does (:828), and ours matched the *shape* while
  validating nothing — `(@a (@""))` was accepted. **No vector can score it**: every string-id vector
  in the file (:76–:79) is top-level, so the oracle never asks about the nested position. Fixed by
  factoring `annot_id` into one `annotIDError` both call sites read, the duplication having been what
  let one copy drift.

  New controls in `internal/text/annot_test.go`, each falsified by introducing the defect it names:
  the `"(@"` arm *sets* of both rules compared against `lexer.mll` with a per-rule vacuity floor (a
  tab-anchored arm regexp yields 0 vs 0 and the floor catches it), the top-level-vs-nested verdict
  asserted as a **bidirectional** pair over the same three bytes, `annot_id`'s two messages pinned in
  both positions, and `annotations.wast:1` read end-to-end through `ReadModule` from the vendored
  suite rather than transcribed.

- **Two comments claimed a falsifiability their own probe refuted, in opposite directions**
  ([#80](https://github.com/scttfrdmn/burroughs/issues/80)). The falsification pass over the label
  work broke each of the four facts and read the board, and two of the four sentences written *about*
  those facts were wrong:

  `passFloor`'s new paragraph said `foldedBlock`'s label push was the sharpest case because dropping
  it "costs nothing in the fail bucket and shows up here" — inferred from the two vectors' spelling
  rather than read. Dropping it moves the board **4161 → 4077 and the fail bucket 2 → 86**, all 84
  landing in `(module <wat body>) must read`: an over-rejecting rejector turns must-succeed modules
  into failures, so it is loud in *both* columns. Third time on that floor that reasoning about a
  check was corrected by running it.

  `funcField`'s comment claimed the opposite, that its `enter_func` reset and anonymous push each
  fail in a named way ("a label leaks out of one func into the next … the depth invariant breaks").
  Neither is falsifiable by any wat input: dropping either leaves the board at 4161/2 and
  `./internal/text/` green, because every push site pops under `defer` — so the stack is *already*
  empty when a func body ends — and a func is a module field with no enclosing label scope to inherit
  from or leak into. The lines are kept as **cited agreement with the reference, stated as such**,
  with the probe's numbers at the definition site and the pairing machine-checked in the one
  direction available (`TestLabelStackIsBalancedOnEveryExitPath`, depth 0 on every exit path
  including error returns). A line kept for a reason no test can reach is a declared-and-tracked
  deferral; what makes it honest is the declaration.

  Both are the same offence as `labelPushAnon`'s earlier draft and are recorded the same way: *a
  green that survives the bug it names is a control in name only* has a prose face — **a comment
  naming a defect nobody tried to introduce is a claim, and half the time it is wrong.** The
  generalization the pair supplies is that a *set* of facts asserted together needs the reading done
  per member: this file's header and that floor's paragraph both claimed to be the evidence for four
  facts while being the evidence for one and two respectively, and only the arm-by-arm probe
  separated them.
- **`lane_imms`' bare-laneidx arm was eaten by `memarg`'s greedy memory index**
  ([#76](https://github.com/scttfrdmn/burroughs/issues/76)). `lane_imms` (parser.mly:661-673) was
  implemented as `memarg laneidx`, so `idx_opt` consumed the lone NAT of `v128.load8_lane 0 (…)` as
  a *memory* index and the mandatory laneidx then found a paren. The reference multiplies the
  production into five arms with a comment saying why — *"Need to multiply out options and indices
  to avoid spurious conflicts"* — and the fifth is the one a composition cannot express. **Board
  4147 → 4156 pass, 16 → 7 fail**: nine files 0/1 → 1/1
  (`simd_{load,store}{8,16,32,64}_lane.wast` and `simd_memory-multi.wast`), per-file diffed.

  The forecast said ten and hedged — *"one further `simd_*_lane` file; the exact set is printed by
  the bucket"*. There is no tenth, and printing the set is what settled it. A forecast that names
  its own oracle is falsifiable by consulting it.

  **The nine vectors cannot certify the fix**, which is why the control is one row per arm: eight of
  the nine files write only the bare arm, so a fix that merely stopped reading a memory index would
  take all nine green and break arm 1. `simd_memory-multi.wast` is the one file writing every arm,
  and it hands over a **bidirectional control** — `:12`'s lone `1` is a lane index while `:22`'s
  identical leading `1` is a memory index, so one wrong answer in the lookahead fails the two halves
  in opposite directions. Five defects run, each failing the arms predicted; the store-family
  sharing check was corrected *by* its own falsification, which showed the bare spelling is the one
  that does **not** catch a mis-wired shape table.
- **`elem_list`'s reftype arm was shadowed by the offset-sugar lookahead**
  ([#75](https://github.com/scttfrdmn/burroughs/issues/75)). `elemField` tested
  `at(LParen) && !peek2Keyword(kwItem)` and concluded "an offset", but `elem_list`'s second arm is
  `reftype elemexpr_list` (parser.mly:1155) and a reftype has a parenthesized spelling — `(ref
  func)`, `(ref null func)`, `(ref $t)` — led by neither `item` nor an instruction. **Board 4156 →
  4159 pass, 7 → 4 fail**, and the `(module <wat body>) must read` bucket falls to **1**
  (`annotations.wast:1`, #83's — the number was corrected there; see [Unreleased]).

  **Three vectors, where the issue forecast two.** `array.wast:219` — `(elem $e (ref $bvec) …)`, a
  reftype naming a defined type rather than `func` — was in the bucket all along and unlisted,
  because the bucket key is the expected spec string and that string says nothing about which arm
  broke. Found by printing every failing module's error instead of trusting the list: a partition can
  be *finer* than the issue that named it, not only coarser.

  The control is a **product** — both `elem_list` arms × three offset spellings (none, `(offset …)`,
  bare expr) — because all three vectors write the reftype arm with no offset, so a fix that stopped
  treating any paren as an offset would pass them and lose the sugar arm. The bare-expr column is
  what makes the lookahead a partition rather than a priority: an offset may be any folded
  instruction (:1091-1093), and what separates the cases is that `REF` is its own token (lexer.mll:180)
  while `ref.func`/`ref.null` are others, so `(ref …)` cannot begin an expr at all.

  **Falsification found something the fix did not need: the third lookahead discriminates nothing.**
  Deleting `!peek2Keyword(kwItem)` fails no row, and a `panic()` in its complementary branch never
  fired across the suite — `elemexpr_list` follows a *mandatory* reftype, so `(item …)` can never be
  the first thing after `elem`, and both readings reject `(elem (item …))` with the same message.
  Kept, with the measurement recorded at the site rather than an argument. **Ruled on in #82
  (Scott): kept** — deleting a condition because nothing reaches it today is precisely the move #75's
  own shadowing counsels against, and the measurement stands as the record of what it does *not* do.

  **Both graves are over-rejections**, which is the class a reject-direction corpus is structurally
  blind to. Twelve vectors across the two, and not one would have been visible on the 7-module accept
  oracle that preceded #69.
- **The fixture-provenance guard was checking 118 of 244 citations, and vouching for a file its
  checker read past** ([#78](https://github.com/scttfrdmn/burroughs/issues/78)).
  `TestEveryFixtureFileIsChecked` triggered on `//\s*<file>.wast:\d+` — a citation had to *open* a
  comment — but the wat-fixture style puts the citation in a **row field**, so **17 cited fixture
  rows in `parser_test.go` (9) and `instr_test.go` (8) were unregistered while the guard said
  nothing**. A regexp that under-matches produces no finding rather than a wrong one, which is why
  it sat green: *a guard's trigger predicate is itself a claim about the space, and an
  under-matching one fails silently by construction*. Breaking the assertion could never have found
  it — what did was measuring the trigger's **coverage** against the population it claims. Coverage
  is to a trigger what a vacuity floor is to a comparison.

  Two more defects came out with it. `match_test.go` had been registered with the text checker
  since that checker was written, and **the checker verified nothing in it** — every citation it
  carries is prose or a `derived` declaration, so the row filter skipped all of them; a
  registration made an unchecked file look checked, which is worse than unlisted, and only the new
  `withRows` floor said so (6 files with rows against 7 registered). And I was one sentence away
  from committing "its `derived` premises are checked elsewhere" when a probe showed
  `TestDerivedFixturesStateResolvablePremises` read **three binary files only**: 2 derived
  fixtures, 4 premises. Widened to 8 files, with a grammar-aware `premiseResolves` replacing the
  `\hh`-only `suiteSourceLine`.

  **Scope settled by measurement, not by preference.** A prose citation has no transcription and so
  no drift, and the strongest machine check available for one is "the line falls inside some
  command's extent" — a next-command span index covers **169427 of the suite's 178222 lines
  (95%)**, a whole-file index 177983. A check that passes on nearly any integer is not a check, so
  prose citations stay reviewed by eyes and the guard is scoped to what a machine can verify.

  The trigger is now `citedRow`, **shared** with the text checker rather than spelled twice — two
  regexps for one concept is how a file comes to be registered with a mechanism that reads past it,
  so the `match_test.go` defect was downstream of the duplication. `TestTextFixtureProvenance` also
  gained a per-command **extent** index (a command occupies *lines*, and a citation naming the
  `(module quote …)` line inside an `assert_malformed` is the *more precise* citation, naming the
  vector rather than the assertion wrapping it), verbatim bidirectional containment, and
  shape-based layout detection with a counted-and-ceilinged `computed` category for rows whose
  vector is spliced from Go constants. **41 cited text fixtures checked, up from 24; 9 derived
  fixtures over 15 premises, up from 2 over 4.** Two mechanisms written during the repair proved
  unreachable under a `panic()` probe and were deleted rather than left as untested scaffolding.
- **The wat parser's type-resolution family** (#64, second half): `typeuse` resolution and functype
  equality — `inline_functype_explicit` (parser.mly:237) and `inline_functype` (:222) at all ten of
  their reference call sites, over a deferred phase that runs once the field list is complete.
  **Board 4122 → 4147 pass, 41 → 16 fail**, six files moved and none withdrew (block 12/16→16/16,
  call_indirect 10/14→14/14, func 22/27→27/27, if 21/25→25/25, loop 12/16→16/16,
  return_call_indirect 10/14→14/14), summing to 25, diffed per file. Residue: 13 in `(module <wat
  body>) must read` (#75, #76, #83), 2 `unknown label` (its own PR — `enter_block` and scoped
  labels), 1 the decoder's.

  **Resolution has to be deferred, and one suite vector says so.** `imports.wast:62-64` uses `(type
  $forward)` in two fields *before* defining it, and `module_` applies its field list to two `()`
  arguments (:1389-1392) — three stages, where every *name* binds at stage 0 and the type-using arms
  run at stage 2. A resolver that looked names up where they are used would reject that module:
  accept-direction, so no `assert_malformed` can see it, and it is the one place on the board where
  the design is visible at all (introducing the defect costs imports.wast one pass).

  **The two lookups a typeuse performs are separately timed, and only one is deferred.** `func.wast`
  pins it in both directions on one module — `:442`'s `(func (type 2))` is `assert_invalid`, the
  parser's business being to *accept* it, while `:454`'s `(func (type 2) (param i32))` is
  `assert_malformed` on the same index. What the empty-signature branch defers is `func_type`'s range
  check; `typeuse`'s own name resolution (:471 → :489) runs regardless, so `(func (type $undefined))`
  is malformed with or without an inline signature.

  **`non-function type <n>` is implemented and corpus-unreachable** — zero vectors, measured — because
  omitting one of `func_type`'s three outcomes would report `unknown type` for an index that resolves.
  Pinned by a print check, not by the board.

  Eleven controls, each falsified by introducing the defect it names, and **nine of the eleven defects
  leave the board unchanged**: the index arithmetic, the block sugar's two no-intern cases, the
  equality's five axes, the nesting order, and the error precedence are all invisible to the suite.
  The two that aren't are an over-rejecting resolver and the block arms wired to the create-helper.
  The table is in `internal/text/typetable_test.go`'s header, and that ratio — not the 25 — is what
  the phase's evidence actually rests on.

  Forecast met exactly (41 → 16, pre-registered on #64), and discounted in the same breath: it was
  made after the bucket had been printed and partitioned, so it predicted the size of an already-known
  set. The two forecasts over *unlisted* spaces were both wrong — the nesting order and `externtype`'s
  arms — and both were corrected by printing what the code returns rather than by reasoning about it.
- **Implicit block types were interned outer-before-inner, the reverse of the grammar (#64).**
  `blockSignature` took no tail, so its three callers — `block`, `handlerBlock`, `foldedBlock` — read
  the body *after* the signature's operation was recorded, putting the enclosing construct's implicit
  type in the space ahead of the nested one's. Every arm of the reference's block family reads `let
  ft, es = $2 c in` and only *then* calls its helper (:741, :769, :866, :902), and `$2 c` forces the
  chain whose innermost production is the body. Measured, not argued: index 1 outer and index 2 inner,
  against index 1 inner and index 2 outer now. **Invisible on the board in both configurations** — an
  index shift only becomes a verdict when a numeric typeuse names a shifted index, which no vector
  does. The comment on `orderedTypeUse` asserted the correct order the whole time and was true of that
  function while false of the family it was written for: a nil tail one level up defeated it, which is
  *the defect stated as the rule* with the rule accidentally right. The control is
  `TestNestedBlockTypeInternsBeforeItsEnclosingOne`, and falsifying it exposed a second gap — its
  first version was spelled folded throughout, so re-breaking the *flat* caller left it green. Now one
  nesting per call site.
- **The wat parser's folded/sugar stratum** (#64, first half): `expr`/`expr1` in full — all ten arms
  (parser.mly:813–834) — plus `exprList`, `foldedBlock`, `ifBody`, and the shared handler-clause
  reader. **Board 1953 → 1992 pass, 67 → 28 fail**, and the whole fall is the folded spellings of a
  grammar #63 had already made correct: five files moved (block 3/15→11/15, loop 3/15→11/15, if
  11/24→20/24, call_indirect 0/11→7/11, return_call_indirect 0/11→7/11), summing to 39 with nothing
  withdrawn, diffed per file. `unimplemented: instruction body` is at **zero** — every remaining
  failure is a semantic question, 24 `inline function type` in a six-file × four grid, 2 `unknown
  label`, 1 `unknown type`, 1 the decoder's.

  **Ten productions share one reader.** Five reference families have the same ordered prefix —
  optional `typeuse`, `(param …)*`, `(result …)*` — differing only in tail: `block_param_body`
  (:754/:760, tail `instr_list`), `handler_block_*` (:780/:786, handler clauses), `if_block_*`
  (:879/:885, `if_`), `callexpr_*` (:851/:858, `expr_list`), `callinstr_*_instr_list` (:712/:720,
  `instr_list`). `orderedTypeUse(tail)` is the one reader, and the risk that creates — two places
  knowing that params precede results — is held by `TestFoldedAndFlatSignaturesAgree`, which
  compares 52 flat/folded signature pairs and fails on *disagreement* rather than on a verdict.

  The forecast said 41 vectors and 39 landed. `token.wast:101`/`:117` (`$l0`, `$l$l`) are reached by
  the folded reader and turn on name **resolution**, not grammar; a forecast wrong by two in the
  direction of mistaking a resolution question for a syntactic one, recorded rather than rounded.
- **Every module containing a flat `select` or `call_indirect` was rejected (grave, #64).**
  `instr_list` has **four** arms (parser.mly:546–550), not three: `selectinstr_instr_list` (:549)
  and `callinstr_instr_list` (:550) are arms of the *list* rather than of `instr1`, because they
  absorb the list's tail — a flat `select` is followed by a `(result …)*` chain that bottoms out in
  `instr_list` itself. Nothing read them, so `(func select)`, `(func nop select nop)` and
  `(func call_indirect (type 0))` all failed with `unimplemented: instruction body at "select"`.
  Accept-direction, therefore invisible to the board by construction: no `assert_malformed` vector
  can complain that a legal module was refused. Found by enumerating the reference's arms after the
  folded work rather than by any suite signal — *scope controls to the space*, applied to a
  production I had treated as three arms because three is what I had implemented. The control is
  `TestNoInstructionLeaderIsUnread`'s accept half, which sweeps all 494 instruction-starting
  keywords and reproduces exactly this defect when `flatSelectOrCall` is unwired.
- **The instruction boundary is retired, and what retires it is a sweep rather than an impression
  that the grammar looks finished (#64).** `unimplemented` promises a later stratum will read the
  token; with all four `instr_list` arms and all three `instr1` arms read, that promise is
  undischargeable, and the arm was still making it — `(module (func param))` answered
  `unimplemented: instruction body at "param"`, which is **#70's defect on the unparenthesized
  side**, since #70's derived check only looked past a `(`. 93 keywords reached the arm and
  `startsInstruction` admits none of them. Measured before deleting: of the 494 keywords it does
  admit, **493 are consumed by a reader**, the one exception being `i8x16.shuffle` blaming its own
  offset for `wrong number of lane indices` — a reader claiming the mnemonic, not declining it.
  `bodyBoundary` becomes `expectedInstr`, a plain syntax error in every case; board effect none,
  read rather than forecast.

  The tripwire is **re-pointed a third time, and this time the risk inverts rather than moves.**
  `TestBodyBoundaryIsNamed` guarded against a deferral reported as a syntax error; with no deferral
  left, the live risk is the reverse — a syntax error reported as a deferral, which is the
  flattering direction, since it parks a module the reference rejects in the board's remaining-work
  bucket. Renamed `TestNoInstructionLeaderIsUnread` and re-scoped from a case list to the whole
  keyword table plus the non-keyword token classes, because three successive re-pointings were all
  arguments about which examples belonged in a list. A dissolved subject is re-pointed, never
  closed — and a sign change is a re-pointing.
- **An agreement control is falsified by making its two paths diverge, not by breaking what they
  share** (#64). `TestFoldedAndFlatSignaturesAgree`'s second falsification was meant to be "drop the
  `(param …)*` loop", and dropping it from the *shared* `orderedTypeUse` **passed** — as it had to,
  since mutating one reader moves both spellings together, which is the property the control
  asserts. It was not a silent pass (four other tests failed and the board lost 4 vectors), but as a
  falsification of *this* control it proved nothing, and reading the green as "the control is weak"
  would have been the wrong lesson. Redone as a divergent copy with no param loop: six rows fail,
  folded rejecting where flat accepts. The general shape — an agreement control is blind by design
  to anything that moves both operands, so it must be **paired** with a control that pins one of
  them, here `TestBlockParamHasNoNamedForm`. Recorded because the *first* falsification attempt is
  where the discipline nearly mis-read its own instrument.
- **The instruction boundary now derives what may start an instruction, instead of enumerating one
  arm of it (#70) — and the "zero vectors turn on this" forecast was wrong by 12.** `bodyBoundary`
  admitted a `(` only when the keyword after it was a `try_table` handler clause, and answered
  `unimplemented: instruction body` to every other folded form. That is a scoping decision made
  from the one production the author happened to be reading: the space is `expr1`
  (parser.mly:813–834), whose 10 arms are `plaininstr expr_list` plus nine folded leaders across
  seven tokens. Replaced by `startsInstruction`, the union of `shapeOf`'s domain and
  `expr1NonPlainLeaders`. **Board 1941 → 1953 pass, 79 → 67 fail**, all 12 being `func.wast`
  field-ordering vectors — six type-use permutations (`:559`–`:594`) and six field-after-body
  forms (`:937`–`:957`) — which answer because `func_body` is `instr_list` (:1017) and cannot begin
  with `(param`/`(local`/`(result`/`(type`. Nothing withdrawn, no bucket grown, checked per file.
  Position-dependence comes free: the boundary only runs where an instruction was *required*, so
  `(func (param i32))` still parses.

  The forecast is the lesson. #70 said "board effect: none" on the strength of five spellings tried
  at a Go prompt, and *cheap is a grammar claim* — a board figure is as falsifiable as any other and
  this one was asserted without the board. The class the probes could not reach is **reachable
  keywords in an unreachable position**: every one of the 12 leads with a keyword the parser knows,
  so the interesting inputs are ordinary tokens where no instruction may start, and you do not
  stumble onto those by inventing spellings. Corrected on the issue before landing, measured by
  patching the boundary and reading the board.

  Two controls, each falsified separately because they assert different things:
  `TestExpr1LeadersMatchTheReference` checks the *list* against `parser.mly` in both directions
  (too narrow is an accept-direction defect no `assert_malformed` can catch; too wide regrows #70),
  and `TestStartsInstructionIsTheUnionOfBothArms` checks the *predicate* is wired to it, reflecting
  over the generated keyword table rather than naming mnemonics. Dropping `TRY_TABLE` fails only the
  first; making the predicate ignore a correct list fails only the second.
- **A latent hole in the shared production extractor, found by a vacuity floor rather than by a
  failing assertion.** `reProductionHead` was `^[a-z_][a-z_0-9]* :$`, which misses all six headers
  the reference annotates — `expr`, `expr1`, `func_fields_import`, `func_fields_import_result`,
  `inline_module`, `inline_module1`, each written `name :  /* Sugar */`. So a *lookup* of a commented
  production found nothing and a *bound* on the production before one ran straight through it. The
  one pre-existing caller was unaffected purely by luck: `plaininstr` is followed by an uncommented
  `laneidx :`. `TestPlaininstrShapesMatchTheReference` stays green with the narrow regex restored,
  which is the proof the hole was invisible. What caught it was `TestExpr1LeadersMatchTheReference`'s
  non-empty floor firing on its first run — *a comparison against an empty set succeeds*, and
  without the floor the new control would have extracted zero arms and agreed with everything,
  green and asserting nothing.
- **A grave: the block label readers compared raw lexemes, and `unparam` was the thing that found
  it.** `labeling_opt`'s named arm and `labeling_end_opt` are both `| bindidx` (parser.mly:515/:523),
  which is `| VAR { var $1 $sloc }` — the same `var` helper (:48–51) that `Utf8.decode`s every other
  binding occurrence. Reading `Token.Text` instead skipped the decode and compared the *spelling*,
  which is three defects at once: `block $"\ff" end` was accepted (four arms, one missing call);
  `block $"\ff" end $"\ff"` was accepted, two identical bad spellings comparing equal; and
  `block $a end $"a"` was rejected as a `mismatching label` where the reference calls it a *match*,
  `$a` and `$"a"` being two spellings of one name (lexer.mll:815 vs :816). **Board unchanged at
  1941/79 — all three are invisible to the suite**, two being accept-direction and the third the
  right verdict with the wrong reason. What surfaced it was a lint finding, `labelingOpt - result 1
  (error) is always nil`: the finding was true, and the reason it was true is that the decode which
  would have made it non-nil was missing. *An error constant with no reachable path is a missing
  check wearing a disguise* (grave 0003) — and here the linter reached the disguise one layer before
  the sweep would have, so the honest reading of a dead error return is a question about the check,
  not an invitation to delete the signature. Sweep for siblings: every other `p.c.at(VarTok)` site
  routes through `bindidx`/`decodedVar`; these two were the only raw readers, so the family is
  closed. Fixed with `labelingOpt` returning a decoded name, the decode ordered *before* the
  comparison because `bindidx` reduces at token-read time — so `block $a end $"\ff"` is malformed
  UTF-8, not a mismatch. Three controls, each falsified against its own mechanism:
  `TestLabelsCompareDecodedNames` (both directions in one test, since a spelling comparison is wrong
  both ways at once), `TestLabelDecodePrecedesComparison`, and
  `TestEmptyIdentifierHasNoSpelling` — the last pinning the cross-file premise `labelingOpt`'s `""`
  sentinel rests on, and it needed **two** falsifications because `$""` and a bare `$` are rejected
  at two independent lexer sites (:817, :819); breaking one left the other row green, which is what
  proved the rows were not measuring the same thing.
- **A grave: a block's parameter list is not a `functype`, and the comment asserting otherwise was
  the camouflage.** `blockSignature` delegated to `p.functype()`, so `block (param $x i32) end` was
  accepted — but `block_param_body` (parser.mly:756) has **one** param arm where `functype`
  (:430–:438) has two, the second being the named sugar `LPAR PARAM bindidx valtype RPAR` (:436).
  Six vectors cover it (`block.wast:1475`/`:1479`, `loop.wast:783`/`:787`, `if.wast:1513`/`:1517`)
  and three are this stratum's. *The defect stated as the rule* — the function's own header claimed
  the two prefixes were the same production shape, so a reviewer checking code against its
  documentation found agreement. Caught by #63's definition of done, which requires each reader be
  measured against the reference production defining its extent *on its own*: the discipline found
  what a shared-prefix argument had talked past. Sweep for siblings came back clean —
  `func_fields_import` (:995), `func_fields_body` (:1008) and `tag_fields` (:1047) all legitimately
  carry the named arm, and `blockSignature` was the only site whose reference production lacks it.
- **The boundary was claiming a handler clause in instruction position.** `(module (func
  (catch_all)))` reported `unimplemented: instruction body` — `try_table.wast:366` and `:371` want
  `unexpected token`. The clauses appear in exactly two productions (`handler_block_body` :792–806,
  `try_block_handler_body` :929) and both consume them *before* the `instr_list`, so they are in no
  `expr1` arm and no later stratum will grow a reader for one: the report promised work that cannot
  arrive. **The falsification run for the rejected alternative fix passed, which is what found the
  right one.** Adding the clauses to `atBlockTerminator` instead was expected to fail the control
  and did not — the same class as the three-deep claim below, a structural argument no assertion
  held. Probing produced the single discriminating input, `(func (drop (catch_all)))`: a folded
  operand reaches the boundary through `expr`'s operand loop rather than through `instrList`, which
  a terminator set cannot see. On every other input the two variants agree. The general form —
  `(memory 1)`, `(then …)`, `(else …)` and a flat `block (result …) (param …)` all still claim the
  boundary, with **zero vectors** turning on any of them — is #70, and the by-name clause list is
  declared-and-tracked at its definition site pending that derivation.
- **`TestBlockEndIsRequired` was a green surviving the bug it names, the second time this PR.** All
  five of its rows put a `)` where the `END` should be, and `bodyBoundary` already answers a closing
  paren with `unexpected token` — so swapping `p.unexpected()` for `p.bodyBoundary()` returned the
  identical error on every one and the table passed. A partition asserted from case labels rather
  than checked against what the code returns, exactly as `TestSectionSizeBothSigns` was (#34). Fixed
  by printing both variants and adding the rows that discriminate: `else` after a non-`if` block, and
  a truncated source, where the broken version reads `unimplemented … at "else"` and `… at ""` — the
  engine claiming an unterminated file becomes legal once #64 lands. The paren rows are kept and
  **labelled non-discriminating**, since silence about which rows carry the assertion is how the
  first version passed review.
- **A grave in #63's own lane-immediate readers: the lane count preempted a syntax error.**
  `i8x16.shuffle 0 … 14 -1` reported `wrong number of lane indices` where the reference says
  `unexpected token`, on six vectors. The cause is LR reduction order — `error (at $sloc)` at
  `parser.mly:653` is a *semantic action*, so it cannot run until the production reduces, and a
  lookahead outside the follow set is a syntax error raised in the automaton first. The count is
  genuinely outside; the fix is a follower arm before it.
- **The three-deep claim that fix arrived with was wrong, and the falsification pass is what
  killed it.** The first comment and test asserted a precedence — range, then syntax, then count
  — and hoisting the follower check above the loop changed *nothing*, which a real precedence
  would have made visible. Printed rather than reasoned about: `256 … -1` is a range error and
  `-1 … 256` is a syntax error, same two faults, verdict decided by *position*. Both are raised
  during a left-to-right scan, so between them the leftmost wins and neither kind outranks the
  other; only the count is a true outer layer. The claim survived because every vector in the
  suite has exactly one fault per index list, so **no vector could distinguish a precedence from
  a scan order** — the current-sample blind spot with the whole corpus as the sample. The two
  two-fault rows that separate them are synthetic and say so.
- **A sibling of the same shape in `vecConst`, found by the post-grave sweep and invisible to the
  board.** `v128.const i8x16 0 … 14 $x` reported `wrong number of lane literals`: a VAR is not a
  `num` (`parser.mly:476-478`), so the reference cannot reduce `VECSHAPE list(num)` and never
  reaches `vec`'s length test. No vector covers it — `simd_const.wast` writes wrong lengths and
  out-of-range literals but never an illegal follower after a short list — so the board reads
  green on both readings and the sweep is the only thing that could have found it. The control is
  marked synthetic with the `num` production as its premise.
- Four graves in the lexer, all found by falsifying its own tests from a committed
  baseline rather than by review. **`(; (; half closed ;)` lexed clean** — closedness is
  the nesting depth, and the predecessor read the trailing two bytes; its own doc comment
  claimed a "negative length convention" the code never implemented, which is *the defect
  stated as the rule*. **Block and line comments skipped ill-formed bytes**, where the
  reference's `comment` scanner has `| _ { error "malformed UTF-8 encoding" }` and
  `utf8_no_nl` bounds the line-comment star; found by sweeping the sibling scanners after
  the same permissive-default shape was fixed in `scanAnnotBody`. **`countNewlines`
  counted `'\n'` bytes** rather than `newline` arm matches, so a bare CR advanced no
  lines and `\r\n` counted as one instead of two — it passed every existing test because
  `Line` is read by nothing yet, which is a green asserting a property of code that does
  not run. And **`scanAnnotBody`'s string case had a dead branch**, both sides returning
  the same message and conflating three distinct annot arms.
- A grave in a control, and the most instructive one: `TestTextFixtureProvenance`'s
  containment check searched for *whichever* row literal the cited command contained, and
  every row's leading name field is a substring of its own source — `"current_memory"`
  appears in `(drop (current_memory))`. So the **name** satisfied the check the **source**
  existed to pass, and a probe corrupting only the source went green. *A control that
  picks its own input by whichever candidate passes will always find one that passes.*
  Both fields are now read positionally. Found by perturbing a different field than the
  probe that had already succeeded: **a control falsified in one field is not falsified.**
- `FuzzLexerProgress` caught its own harness defect on its first fuzz run: the EOF branch
  compared `before` against `len(src)`, but `Next`'s skip path consumes bytes and loops
  without returning, so `";;"` reported "EOF with 2 bytes remaining". Fixed to `after`.
- The arm-length bounds check moved from the fuzz harness **into `Next`**, because an arm
  reporting more than it read panicked at the slice *before* the harness could assert
  anything — leaving `slice bounds out of range [:10] with capacity 5` as the only
  witness, naming `Next`'s line and none of the two dozen arms that could have lied. *An
  error from the wrong layer is evidence about where structure was lost*; here the wrong
  layer held the entire diagnosis.
- `make fmt` silently rewrote OCaml quotations in doc comments to typographic quotes.
  The cause is **gofmt's** doc-comment canonicaliser, not gofumpt: it implements the TeX
  convention where a pair of backticks opens and a pair of single quotes closes, so a
  character class in running prose is transformed. Quotations moved into indented code
  blocks — and the explanatory paragraph had to be rewritten to *name* the characters
  rather than show them, because the first draft was canonicalised by the mechanism it
  was describing.
- A drifted citation, found by the new checker on its first run: `(@a \x7f)` cited
  `annotations.wast:26`, which holds `(@a \03)`; the vector is at `:56`. Fixture and
  suite were each internally consistent and their expected strings agreed, so only a
  machine comparing source text could see it.
- Four extractor defects found by printing what the code returned rather than by
  reading it, all invisible to the suite. `i8x16_shuffle` reads `repeat 16 laneidx s`
  and extracted as **one** lane byte instead of sixteen — 15 lost bytes that would
  shift every following instruction in a body. The four structural arms all reported
  the mnemonic `end_`, because "the last `in`" is not "the last statement".
  `0xfb/0x18` reported the OCaml keyword `if`, and that arm needs *two* mnemonics
  (`br_on_cast`/`br_on_cast_fail`) selected by opcode. A multi-line alternation head
  (`decode.ml:601`) was read as an unrecognized arm — which was the extractor working
  as specified, refusing to guess.
- The three prefix escapes (`0xfb`, `0xfc`, `0xfd`) were **absent from the single-byte
  table**, so a walker could not tell "escape to a sub-table" from "no such opcode" —
  the absent-versus-rejected conflation `opInfo.illegal` exists to prevent, in a third
  flavour. Found by #33's agreement test from outside the generator, because the
  generator's own 256-byte partition test *enumerated* `{0xfb, 0xfc, 0xfd}` as a
  literal and so scored the hole as expected. Now recorded as `escape: true` and
  derived on both sides: a hardcoded exception list in a totality check is a hole with
  a comment.
- Two controls in name only, caught by falsification rather than by review. A reader
  check passed with the shuffle fix removed, because deleting the longer pattern lets
  a *shorter* one mask the same text — the check could only see readers surviving
  masking, never one whose territory had been usurped; replaced by an invariant the
  defect actually violates (a matched reader must be *called*, not passed to a
  combinator). And a test asserting the generated table is clean under the repo's
  formatter passed on deliberately mangled input: golangci-lint skips files carrying
  `Code generated ... DO NOT EDIT.`, so the gap it controlled does not exist. Deleted,
  with the measurement recorded at the site — before controlling a gap, check the gap
  exists.
- Two `immBytes` readers were wrong and **no test could reach them** (grave #47).
  `laneidx` read a raw byte; `let laneidx s = u8 s` is `uN 8`, a LEB, so the legal
  two-byte encoding `81 00` consumed one byte instead of two. `laneidx16` read a flat
  `bytes(16)`; `repeat 16 laneidx s` is sixteen LEB reads, 16..32 bytes. Both invisible
  for two compounding reasons: no lane instruction is const-legal, so the extent
  differential never executed either entry, and "a lane index is 0..15, so it is a
  byte" is true about the value and false about the encoding. The general form is
  *a control that only exercises a fact in composition covers the compositions, not the
  fact* — scope controls to the space the **map** spans, not the one its current callers
  reach. Neither is suite-visible: a non-canonical-but-legal LEB is well-formed, which
  is the accept direction 0007 exists to cover.
- Two drifted citations in the same map, found by the citation check on its first run
  (`blocktype` 230→334, `instr_block'` 612→967) — hand-written line numbers, exactly
  the defect `TestFixtureProvenance` was built for, in a new place.
- Decision 0007's stale figures wear a pointer at their point of reading, not just a
  correction three sections away (ruling: Scott, PR #43). *Records are append-corrected;
  stale claims wear a pointer* — the body is preserved per the 0003 precedent, and the
  `counted (not estimated)` heading now forward-references the section that falsifies
  it. The Correction's own single-byte figure was stale too (215, written before the
  escape rows landed in the same PR) and is now 218.
- CI's `conformance` job vendors the reference **before** the board step, not after it.
  The reference-vendoring steps sat below the board on the reasoning that only
  `make opcode-drift` reads decode.ml — then `internal/binary` grew a test that reads it
  too, and the board step failed under `BURROUGHS_NO_SKIP=1` on a corpus the same job
  fetches nine lines later. Reproduced by hiding `third_party/`, not guessed. The
  corollary to the lesson below: a job's corpora are its **preconditions**, satisfied
  before the first step that runs tests rather than next to the step whose name mentions
  them — which package needs which corpus is not a fact a workflow file can track.
- CI's `build` job vendors the reference interpreter too, not only the suite. It runs
  `go test ./...`, which reaches the extractor's tests, which call `RequireSpecRef` —
  and under the workflow-wide `BURROUGHS_NO_SKIP=1` that is a **fail**, not a skip.
  Caught on PR #43's first CI run by the strictness policy doing exactly its job: the
  drift check had been placed in the `conformance` job while the corpus requirement it
  introduced was inherited tree-wide. The general shape, now stated at the site: *a job
  running `go test ./...` inherits every corpus requirement in the tree*, so it must
  vendor all of them rather than the one it was thinking about. Both presence guards
  now run in that job as well, because a truncated fetch passes the Go-level door and
  is a different failure from a missing one.
- Decision 0007's "counted (not estimated)" figures were wrong and are corrected in an
  appended section, body preserved: 201/29/18/256 (504) counted arm *lines*, and
  assumed the SIMD sub-opcode was a byte where the reference runs to `0x113`. The
  reader histogram was a whole-file grep, so it counted occurrences outside the `instr`
  function, and `grep 'idx s'` silently matched the tail of `laneidx s`. Each figure
  had been checked for plausibility rather than against a second method — and the
  extractor, which is that second method, disagreed on its first successful run.
- `binary.wast:112` is settled by asking the authority instead of reasoning about
  it: `decode.ml`'s `sized` runs a section's payload grammar **unbounded** and
  reconciles the declared extent afterwards, which is Burroughs' existing doctrine
  exactly — so the doctrine and the vector never conflicted. `0x0a` is `throw_ref`,
  decoded as a real instruction, and reading continues into the next section until
  EOS yields "unexpected end of section or function". The reference also shows
  `const s` is the *full* instruction grammar with const-ness left to validation,
  which is why that vector currently fails with `ErrNonConstantExpr` — an honest
  fail, not `ErrFeatureDisabled`, so it cannot hide in `gated`.
- `constexpr.go` said `constant expression required` appears 22 times in the suite;
  it appears **24** (global 7, elem 7, data 6, array 2, func_ptrs 2). The
  load-bearing half of the claim was re-checked and holds — 0 occurrences under
  `assert_malformed`, and both cited lines resolve as described.
- Four graves in the new parser, three of them accept-direction — the direction no suite
  vector covers (§9 G-3), and every one found by a sweep written to look there.
  - **`instr_list` has an empty arm** (`parser.mly:546-550`), so `(func)`, `(global i32)`
    and `(table funcref (elem))` are well-formed with nothing in them. The first draft
    routed all of them at the body boundary and **rejected fourteen valid modules**. The
    distinction a boundary must draw is *nothing there* versus *something we cannot read
    yet*, and only the second is `unimplemented`. Its complement landed with it: a closing
    paren at a *mandatory*-body site (`constexpr1` :951, `offset` :1091, the table sugar's
    leading `elemexpr` :1205) is a syntax error, and reporting `unimplemented` there would
    be the wrong-layer error **in the direction that flatters us** — parking a module the
    reference rejects in the work-plan bucket, inflating what the board reads as remaining
    work.
  - **`decodedVar` validated `Token.Text`, the source spelling**, which for `$"\ef"` is
    seven ASCII characters and therefore *always* valid UTF-8 — a production that could not
    fire. The reference's `var` receives the *unescaped* string (`lexer.mll:816-818`), so
    the check belongs on `Value`. Caught by the reject sweep on `id.wast:31`, the single
    vector in the suite that reaches the site, and the reason a one-vector bucket is still
    worth a case.
  - **The test for that site shared its misconception**: it hand-built
    `Token{Kind: VarTok, Text: string(bad)}`, so test and code agreed on the wrong field and
    the assertion passed over dead code. *A hand-built token makes a test's premise about
    the lexer unfalsifiable.* Rewritten to lex real source, which puts the field convention
    where it belongs.
  - **`TestCursorPeekAtEOFIsStable` asserted a premise about another function and got it
    wrong**: `peek()` has no bounds branch, which is correct only while the lexing call
    appends EOF — and `LexAll` stops at EOF and *drops* it. The test panicked with `index
    out of range [3] with length 3` on its first execution. `lexToEOF` was carved out so the
    promise lives in the name of the function keeping it. *A premise about another function
    is checked by a test or it is a wish.*

- **Two correct predicates composed in the wrong order — `uleb` and `sleb`
  tested the continuation bit before the range.** The reference interpreter's
  `uN`/`sN` (`interpreter/binary/decode.ml`) check the unused-bits range
  *before* consulting the continuation bit, and **order of tests is itself a
  claim about the spec**: on the last permitted byte, bytes that are both
  over-wide and continued must be reported *too large*, not *too long*.
  Neither predicate was defective; only their composition. Found by a
  **differential port of the reference's own `uN`/`sN`**, exhaustive over the
  derived disagreement space (k all-continuation prefix bytes × all 256 final
  bytes, k from 0 past the width budget): **112 disagreements at 32 bits, 126 at
  64**, identically for both readers — one structural defect with two tenants.
  Now 0 disagreements over 4096 verdicts at 32 bits and 6656 at 64, with a
  vacuity control asserting the ported oracle actually produces all three
  verdicts over that space. Each half was falsified on its own by reverting it
  and recovering exactly its 112/126.
  ([#36](https://github.com/scttfrdmn/burroughs/issues/36))
- **A taxonomy vector was asserted against the wrong reader.**
  `TestLEBTaxonomy`'s `ff ff ff ff ff 7f` row carried the suite's expectation
  from `binary-leb128.wast:497` — an `i32.const` immediate, a *signed* field —
  and read it with the *unsigned* reader. Both verdicts are correct and they
  differ: `sN(32)` says *too long* (a legal sign extension one byte past the
  budget), `uN(32)` says *too large* (the fifth byte's payload exceeds the
  width). The signed vector moved to `TestSlebIsNotUlebWithACast` and the
  unsigned reading of the same bytes is now pinned where it was, so the pair is
  asserted from both sides. `TestLEBTaxonomy` stayed **green throughout** the
  ordering defect above, because every row it held asked about inputs where the
  two conditions do not overlap — and the overlap region *is* the bug. Grave
  0003's width-parameterization lesson, applied to signedness.
  ([#36](https://github.com/scttfrdmn/burroughs/issues/36))
- **`ErrMalformedFuncType`'s message invented bits the input never had.** The
  byte reconstruction or'd a high bit in for every negative form, reporting a
  `0x5e` array tag as `0xde`. Nothing in the suite can see it — that vector's
  expected string is the bare sentinel, and the harness reads exactly as far as the
  expected string does — and it was found by *printing what the expression returns
  for nine tags* rather than reading its shape. Lesson: **an error message is
  testimony, and fabricated evidence is a lying witness even when the verdict is
  right.** Where the oracle stops short, everything past that point is ours alone to
  keep honest — and per #38 it does *not* always stop at the sentinel: a spec string
  such as `illegal opcode ff` embeds the byte, making the rendering oracle-covered
  for exactly those vectors.
  ([#36](https://github.com/scttfrdmn/burroughs/issues/36))
- **CI's `deadcode` allowlist still filtered `reader.u64`** while the Makefile's
  comment already claimed the allowlist was empty — one truth, two authorities,
  disagreeing. Found by the ruling-falsifies-prose sweep. A gate and its local
  mirror disagreeing is each one's reason to exist, and a suppression outliving its
  subject licenses the next regression silently.
- **`TestSectionSizeBothSigns` was named for both signs and pinned one of them
  twice.** Its first case was labelled "grammar consumed MORE than declared" while
  its own prose said "3 bytes are left over", and the decoder reported `declared 7,
  grammar consumed 4` — the *short* sign. Its second case is face 1, a different
  mechanism. So the grammar-long direction, the only reason the test exists, had no
  assertion at all, and the `t.Log` deferral on its third case hid that a *sign* was
  missing rather than just one vector. Both signs now assert on the error **message**
  (`errors.Is` cannot tell them apart — they are the same error value), and a
  synthetic grammar-long vector covers the direction the suite has no vector for.
  Falsified by swapping the two operands in the message, which now fails three
  assertions instead of none. Lesson: **a test named for a partition must be checked
  against the partition, not against its own case labels** — the coverage cousin of
  *a green that survives the bug it names.* Found while discharging the declared gap
  #25 left in that test. ([#34](https://github.com/scttfrdmn/burroughs/issues/34))
- **`fuzz-smoke` was budgeted in the wrong unit** (#28). The job exists to catch a
  target that stopped building or a corpus that regressed — its purpose names
  *executions* — but its budget was wall clock on a shared runner, making it
  timing-sensitive by construction. It failed twice that way, on PR #27 and again
  on PR #31, both times `context deadline exceeded` with no crasher and after real
  progress: the second at ~70k execs/sec against 130k–670k/sec measured locally, a
  ~7x spread that is a property of the runner and not of the code. Now
  `-fuzztime Nx`, sized from the measured CI floor rather than converted from a dev
  box's rate. Cost, measured on the first green run rather than estimated: the
  `fuzz wast lexer` step went 65s nominal → **3m08s–3m26s**, because the runner's real
  floor is ~17–18k execs/sec, not the ~70k the sizing assumed. Measured across two
  independent green runs, which is the point: 46–47 three-second windows reporting
  `0/sec` against recovery bursts of 605k–**1.25M**/sec — long stalls, not a slow
  steady rate, and a peak that doubles between runs is why no single figure was
  trustworthy. Accepted: a job that takes three minutes and answers a fixed question
  beats one that takes one and sometimes answers none. The stalls get no issue by
  ruling — an issue no work can close is a wish with a label — so the finding lives in
  the budget rationale, where it has consequences.
  `make fuzz` and the nightly 10-minute runs stay wall-clock *because
  their purposes are durations* — the units differ because the purposes do, and both
  sites now say so. Budget by the quantity the purpose names.
- **CI board tests had been passing by not running.** The `build` job never
  vendored the spec suite, and `requireSuite` skips when `testdata/spec` is absent
  — so the pass floor, the closed buckets, the fixture-citation checks, and the
  gated allowlist all skipped on every green CI run in the project's history. No CI
  green had ever asserted a suite count. The `conformance` job vendors the suite and
  **asserts ≥250 `.wast` files are present before trusting any number out of it**:
  a skip is not a verdict, and a job that passes by asking nothing is the
  dishonest-board failure wearing CI's clothes.
- `parseString` returned a nil slice for the empty literal `""`, entangling
  "is a string" with "has bytes" — so a reader checking `str != nil` would
  misread `(module binary "")`, the empty image, which is the *unexpected end*
  boundary and the most-exercised vector in `binary.wast`. Emptiness is a
  length, never a nil. Found by `FuzzWastLexer` on its first run.
- Two hand-typed decoder fixtures had drifted from the suite lines they claimed
  to copy: the UTF-8 BOM vector was truncated from 11 bytes to 8, and an
  `"asm\00"` vector was a mutation of nothing in the suite — reintroducing grave
  #2's own short-preamble-versus-wrong-magic distinction inside the test that
  pins it. All citations now machine-checked, and the coverage widened to every
  preamble vector in `binary.wast:5–45`.
- The s-expression reader could not traverse `annotations.wast`: a bare `;`
  inside a custom annotation form is a delimiter, so the atom loop consumed
  zero bytes and errored on its own delimiter. 256/257 files parsed before,
  257/257 after. Regression vector copied verbatim from `annotations.wast:14`.
- CI used deprecated action versions (Node 20) and requested a Go module
  cache with no `go.sum` to key it on.
- `binary_leb128_64.wast` had been scoring 1/2, and that pass was never earned:
  both its vectors carry i64 memory limits flags, and the decoder was reading the
  limits flags field without interpreting it, so it accepted a memory64 module
  with the memory64 gate off. Honest scoring moves both vectors to `gated` and the
  file to 0/0 — a board line that reads worse and means more. *An unearned pass is
  a regression waiting to be misread*: the fix looks like a regression, which is
  precisely why the third verdict had to exist before the grammar landed.
- Limits flags and section ids are read as **single bytes, not LEBs**. Reading
  either field with `u32` is the helpful mistake: `\81\00` genuinely does encode 1,
  so a LEB read accepts all three of `binary.wast:632`, `:677`, and `:686`, whose
  redundant encoding *is* the malformedness.
- `TestMalformedSectionID` asserted that a tag section must be *accepted* with the
  EH gate off — wrong in the accept-and-ignore direction, and written before the
  gate doctrine was ruled. It now asserts only that the id is not reported as
  malformed; both gate states are covered by
  `TestTagSectionIsWellFormedButGated`.

---

*The entries below land with the instruction and function-body grammars
([#43](https://github.com/scttfrdmn/burroughs/issues/43),
[#39](https://github.com/scttfrdmn/burroughs/issues/39)). `[Unreleased]` now carries
duplicate `Added`/`Fixed` headings from successive PRs appending to it; consolidating them
is a formatting pass of its own, not a drive-by inside a decoder PR.*

- **`decodeConstExpr` defers the const verdict rather than aborting on it.**
  `binary.wast:112` is the vector that forces it: a global initialiser ending `\41\00`
  with no END, followed by the code section's id byte `\0a` — which *is* `throw_ref`.
  An aborting reader reports `constant expression required`; the reference reads on and
  the expression runs off the image, so the answer is `unexpected end of section or
  function`. *An invalid verdict that pre-empts a malformed one is reporting the wrong
  layer's answer.* `ErrNonConstantExpr` is gone; `ErrConstExprRequired` is recorded and
  released only if the grammar completed.
- **Grave [#47](https://github.com/scttfrdmn/burroughs/issues/47) reached a second
  site.** The same raw-byte lane-index read, in `instr.go`'s production `imm` switch
  rather than the test's `immBytes`, survives the whole corpus for the same reason
  (`\fd` appears in no phase-1 vector). *A grave whose lesson was applied to one copy
  of a fact and not the other is half-buried.*
- The changelog's own `binary.wast:112` entry above, which described the vector as
  "currently fail[ing] with `ErrNonConstantExpr`". That sentinel no longer exists and
  the vector passes — the ruling-falsifies-prose sweep applied to this file. ADRs 0006
  and 0007 got the same sweep, by append with bodies preserved.

- **`TestEveryFuzzTargetIsGated`** — the AST-reading sibling of
  `TestEverySkipSiteIsLicensed`, and it was written because it had a live subject.
  `FuzzConstExprProgress` landed with the instruction grammar carrying eleven seeds and a
  fourteen-sentinel allowed-error list, and was budgeted in **neither** the Makefile nor
  either workflow — so it ran only as an ordinary seed-corpus test and its *exploration*
  half had never once executed. Three enumerations of the target set (`Makefile`,
  `ci.yml`, `nightly.yml`) with no control over any: *derive the domain, never enumerate
  it*, broken three times in the same tree. The control now derives the set from
  `func FuzzX(f *testing.F)` declarations, requires each to appear at all three run
  sites, checks both directions, and carries a size floor so an empty walk cannot agree
  with an empty inventory. Newly gated at 1.5M execs, it immediately found **129 new
  interesting inputs** — the measure of what "defined but never budgeted" costs. *A
  fuzzer has two halves that fail independently; a target nothing runs under a budget is
  a file, not equipment.*

- **The gate decline is deferred, not returned on sight** — the same layering
  `binary.wast:112` forced on the const verdict, and it would have been wrong on a green
  board. That vector's unterminated initialiser over-reads into the code section's id byte
  `\0a`, which *is* `throw_ref`; an engine that declined immediately would report a gate
  decline for a module the spec calls malformed **and** park the vector in `gated`, where the
  allowlist would then have to license a decline that is pure artifact. Order, decided in
  0008 since the reference has neither verdict: malformed, then the feature decline, then
  the const verdict.
- `TestEveryMappedOpcodeExistsInTheTable` asked the wrong question in its first draft, and
  the falsification pass is what found it: re-pointing an entry at `0xc6` — present in the
  table with `illegal: true` — left it **green**, because presence was the predicate and
  "exists" is not "is accepted". A gate governing a byte the reference rejects anyway is a
  gate governing nothing, which is the exact defect the test names. Probing an *absent* byte
  fired; probing an illegal one did not, and only comparing the two showed the gap. *A
  control that passes the defect it was written for is a control in name only* — and
  injecting one defect is not enough when the predicate itself is what is wrong.
- **The `0x40` table form is decoded, and declined by feature name when GC is off**
  ([#51](https://github.com/scttfrdmn/burroughs/issues/51)). Function references adds a
  second table encoding — `0x40`, a reserved zero byte, a tabletype, and a const-expr
  initializer, `(table 1 (ref func) (ref.func 0))` in text. `decodeTable` had only the
  plain form, so the `0x40` fell through to `decodeRefType` and came back **`malformed
  reference type: 0x40`**: seven valid `elem.wast` modules rejected with the spec's own
  word. An accept-direction defect, and *a decoder that rejects valid modules is worse
  than one that misses an invalid one* (§9 G-3) — which is why it was taken ahead of
  coverage growth. **Default board 783 pass / 1 fail / 1345 unsupported / 15 gated**
  (from 8 fail / 8 gated); **all gates on 798 / 1 / 0** (from 791), so the seven are
  *earned* in the all-on lane rather than parked. The remaining red is `binary-gc.wast:1`,
  a different mechanism.
- **`decodeRefType` reads the reference's fourteen forms, not two bytes.** It compared
  `0x70`/`0x6F`; the reference reads an `sleb(7)` and ranks fourteen — the Wasm 2.0 two,
  ten GC abstract heap types, and the two parameterized prefixes `-0x1c`/`-0x1d` that take
  a following `heaptype` (new `decodeHeapType`). All twelve GC forms had been answering
  *malformed*; they are defined by Wasm 3.0, so the engine's own configuration is what
  declines them and it must say so. A signed LEB rather than a byte because the width
  decides the error string for an overlong encoding (grave #36).
- `decodeTable` is deliberately **not** an `either`, and the reason is a measurement. The
  reference uses one; copying it would break the decline, since `either` lets the *last*
  branch's error stand and a `\40` with GC off would be re-judged by the plain branch as
  `malformed reference type` — the gate manufacturing malformedness, the exact defect
  filed. The first draft did use one, with the gated branch first, and its comment gave
  two reasons: ordering, and that the forms consume different extents. Probing all 256
  first bytes killed the second — plain accepts 12, the `0x40` form accepts 1, **both
  accept 0** — so the branches are disjoint, there is nothing to backtrack for, and the
  extent claim was invented. *Second-order honesty applied to a comment.*

- **A pre-registered claim, refuted by its own probe** — recorded here because the
  pre-registration is what made it findable, and *honest boards* includes the claims that
  did not survive. [#53](https://github.com/scttfrdmn/burroughs/issues/53) stated this
  work's board movement before authorship as `unsupportedCeiling` **1345 → 1334**, eleven
  vectors. Both halves are wrong: `obsolete-keywords.wast` was **never on the board** (the
  derived corpus is 14 files and its 11 vectors were not among the 1345), and teaching
  `classify` the `(module quote …)` form cannot be scoped to eleven — it widens
  `scorableCommands` and pulls **54 additional files** on: 14 → **68** files, 2144 →
  **28777** commands, unsupported 1345 → **26742** (+25397), with **1236** newly-scorable
  quote vectors across 41 expected strings. Fourth consecutive time in #53's sequencing
  that a step named "cheap" owed one more layer than its name; the countermeasure is the
  one that caught it.
  **And the refutation's own figures needed correcting**, which is the discipline applied to
  its own output: the first reading said 67 files / 1229 vectors / 26741, missing the **7
  bare top-level `(module quote …)` forms** that are not wrapped in an assertion. Re-measured
  with the classifier itself: 68 / 1236 / 26742. A figure quoted to refute a figure is still
  a figure.

## [0.0.1] - 2026-07-30

*Implements contract v0.1. Scaffold state, recorded retroactively at the
genesis commit — no spec-suite test had been run at this point, and no
conformance claim is made for this version.*

### Added
- Host contract v0.1 (`docs/burroughs-contract-v0.1.md`), normative and
  written before any engine code: 1:1 OS threads with no main-thread special
  case, engine-native safepoints, a sequentially-consistent memory model at
  every host boundary, growable continuation stacks, and a netpoller-shaped
  WASI 0.3 event loop.
- `internal/binary`: module preamble check and section-level scan. Payloads
  alias the input buffer — no copying. LEB128 reader is width-parameterized
  (`uleb(bits)`), with `u32` and `u64` on top.
- Decoder error contract: error text tracks the upstream spec suite's
  `assert_malformed` strings verbatim, matched by substring (decision 0003).
- `cmd/burroughs`: `version` and `inspect` subcommands.
- `internal/interp/dispatchbench`: four interpreter strategies measured over
  the same program with correctness controls — the evidence behind decision
  0002. Records closure compilation as a **negative result with a
  reproducer** (no faster than plain in-place, and it allocates: the frame
  escapes to the heap).
- Decision records 0001 (genesis, Apache 2.0), 0002 (interpreter strategy:
  internal-form rewrite, giant-switch dispatch, `uint64` slots beside a
  parallel reference array), 0003 (staged pure-Go `.wast` harness and the
  error contract) — all accepted.
- `make spec-tests` vendors the upstream WebAssembly test suite (257 `.wast`
  files, gitignored — never committed).
- Apache 2.0 license, © 2026 Scott Friedman.

### Fixed
- Malformed-integer taxonomy in the LEB128 decoder conflated the suite's two
  distinct verdicts: a continuation bit set on the last permitted byte is
  *integer representation too long*, while unused high bits set is *integer
  too large*. The rule is width-parameterized — `ff ff ff ff 0f` is a valid
  `u32` and a malformed `i32` constant — so a `u32`-only reader could not
  express it. Grave (#2); regression vectors lifted from
  `binary-leb128.wast`.
- `ErrLEBTooLong` was unreachable for every input: an early return preempted
  the loop's fall-through, verified exhaustively over all 256 fifth-byte
  values. This is why the taxonomy bug went unnoticed — the error that should
  have named the continuation-bit case could never fire. Grave (#3); lesson
  marked at the fix site: *an error constant with no reachable path is a
  missing check wearing a disguise.*
- A truncated module preamble reported a bad-magic error where the spec says
  *unexpected end*; short and full-width-but-wrong preambles are now
  distinguished, pinned by tests drawn from `binary.wast`.
- Section-size overrun reported the wrong spec string. The suite calls this
  *length out of bounds*; *unexpected content after last section* is a
  different condition (duplicate or misordered sections), now named as such
  and tracked as not-yet-enforced.

[Unreleased]: https://github.com/scttfrdmn/burroughs/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/scttfrdmn/burroughs/releases/tag/v0.0.1
