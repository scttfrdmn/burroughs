# 0009 — The wat keyword table's authority, one grammar over from 0007

Date: 2026-08-01 · Status: **accepted** (Scott, 2026-07-31 — recon stamped on #53, both
outcomes, and the extraction ratified into the mnemonics PR)

## Decision

**The wat keyword table is machine-derived from the reference interpreter's text lexer,
`interpreter/text/lexer.mll`, at the same pinned revision as the opcode table.
Hand-transcribed is not on the menu.**

This is 0007's principle applied to a second grammar rather than a new principle, and
the mechanism is 0007's option **B** unchanged: extract from the source text in pure Go,
commit the output, drift-check it against the pinned reference in CI. All four of B's
conditions carry over and are discharged below.

What is new is the *scope* question — whether this needed its own ADR at all, given that
0007 already settled the principle. It does, for one reason that is not ceremony: 0007's
argument was about a table whose facts are **byte→shape**, and every step of its
reasoning was checked against `decode.ml`. Whether the same argument holds for
`lexer.mll` is an empirical question about a different file, and the answer turned out
to have a wrinkle 0007's file does not have (the two `unknown operator` producers,
below). An ADR that inherits an argument still has to check that the argument's premises
hold in the new place.

## Question

#53 needs to know, for each of 589 mnemonics, that it is a token — and for
`get_local`, `i32.wrap/i64`, and every other spelling wat has ever retired, that it is
not. The bucket is 555 vectors expecting `unknown operator`, plus 11 that name the
mnemonic inside the expected string.

The suite cannot check the accept half of any of the 589. Every vector bearing on the
table is `assert_malformed`, so a keyword the table wrongly **omits** produces
`unknown operator <mnemonic>` on a module the spec calls well-formed — and no vector
asserts that a valid mnemonic lexes, because valid mnemonics live inside modules asserted
valid *as a whole*. Contract §9 G-3's exact shape, and the second instance of it in this
project.

The asymmetry is worth stating numerically because it is starker here than in 0007. A
table containing **nothing at all** passes all 566 `unknown operator` vectors: they are
reject-direction assertions, and an empty table rejects everything. The oracle is
generous about the half that cannot go wrong and silent about the half that can.

## Measured facts

Measured 2026-08-01 against the vendored `WebAssembly/spec` at
`bdd7164bfe18cf0bd5c3d90ef8cc3b8919fb9c0a` — the same revision and the same fetch as
`optable.go`. One `make spec-ref`, one pin, two grammars.

### The extraction is *easier* than `decode.ml`'s, and that was checked before it was claimed

`lexer.mll` is 36686 bytes. The keyword table is one `match` block: the `| keyword as s`
arm of `rule token` (lexer.mll:143–144), running to its fallthrough at :809.

| property | `decode.ml` (0007) | `lexer.mll` (here) |
|---|---|---|
| arm heads | `\| 0xc5 \| ... \| 0xcb`, wrappable across lines | one string literal, never wrapped |
| alternations in a head | yes (cost an afternoon; grave #45) | **none** |
| computed arms | `if opcode = ...` chooses a constructor | none |
| regions | 4 (single-byte + 3 prefixes) | **1** |
| multi-line RHS | 24 arms | 14 arms |
| extracted | 542 arms | **589 keywords, 173 kinds** |

The head grammar is a string literal, so there is nothing to disambiguate. This is the
finding that made the recon come back *extractable* and let 8a stand alone rather than
folding into #8 — the conditional Scott stamped, met with room to spare.

### The `keyword` production gates the block, and it is the reason `/` matters

    let symbol   = ['+''-''*''/''\\''^''~''=''<''>''!''?''@''#''$''%''&''|'':''`''.''\'']   (:66)
    let idchar   = letter | digit | '_' | symbol                                            (:109)
    let keyword  = ['a'-'z'] (letter | digit | '_' | '.' | ':')+                            (:111)
    let reserved = (idchar | string)+ | ',' | ';' | '[' | ']' | '{' | '}'                    (:113)

`keyword` has no `/`. `reserved` has one, via `symbol`. So the extracted set is not
merely "the strings in the block" — it is the strings the `keyword` rule can *deliver*,
and an arm head outside that charset would be dead code in the reference and an
unreachable row here. `checkShape` asserts the agreement, and it is the check with no
counterpart in `opcodegen`, because `decode.ml`'s arm heads are integer literals with no
production gating them.

### There are two producers of `unknown operator`, and the split is 8 + 3

The wrinkle 0007's file does not have, found by probing before authorship:

    | keyword as s { match s with ... | _ -> unknown lexbuf }   (:809)
    | reserved     { unknown lexbuf }                            (:839)

| path | count | mnemonics |
|---|---|---|
| keyword fallthrough, :809 | **8** | `current_memory`, `grow_memory`, `get_local`, `set_local`, `tee_local`, `anyfunc`, `get_global`, `set_global` |
| `reserved`, :839 | **3** | `i32.wrap/i64`, `i32.trunc_s:sat/f32`, `f32x4.convert_s/i32x4` |

**The 3 are right only because ocamllex is longest-match.** For `i32.wrap/i64`,
`keyword` matches 8 characters and `reserved` matches 12; ocamllex takes the longest
match (first rule only on a tie), so the lexeme handed to `unknown` is the whole
mnemonic. A port that lexed keyword-first-wins, or stopped at the first non-keyword
character, would report `unknown operator i32.wrap` — and the expected string for those
three *contains the mnemonic* (#38's refinement), so the suite would convict. Those 3 are
the best vectors in #53: message rendering with real oracle teeth, pinning a
lexer-architecture property rather than a table lookup.

This is why the table alone does not deliver the 11, and it is recorded here because a
reader of the table will otherwise assume it does.

### The eleven obsolete mnemonics are absent, and absence is the whole reject contract

Nine of the eleven are keyword-shaped, so their absence is not a tautology — it is the
block's extent being right. A scraper that wandered into `and annot start` (:844) or into
the parser's token declarations would pick up shapes the keyword block does not have. The
other two carry `/` and could never appear.

Which makes the failure mode unusually quiet: `get_local` is rejected by *not being in
the map*, so an accidental addition has no site where anything looks wrong. Hence a
control on the committed artifact, not only on the extraction — see condition 4 below.

### Hand-transcription's local error rate has not improved

Unchanged from 0007 and quoted because it is the argument's floor rather than its
flourish: **seven wrong citations in twelve hand-written items** (#37), by a careful
author, caught within the hour. 589 facts hand-carried with no machine check is 0007's
defect farm with a bigger delivery.

## Options

Not re-litigated. 0007 evaluated A (build the reference and derive from behaviour), B
(extract from source text, commit, drift-check), and C (hand-write plus a CI differential
lane), and chose B. Every argument transfers, and two transfer *more strongly*:

- **A's cost is unchanged and its circularity is worse.** Still no OCaml toolchain
  anywhere (`ocaml ocamlfind ocamlbuild dune opam ocamlc` → all absent). And deriving the
  keyword set by *executing* the reference means synthesizing a module per mnemonic, which
  needs the mnemonic's arity and immediate shape — knowledge the wat parser does not have
  yet. Circular at the margin in 0007; circular at the centre here.
- **B's weakness is smaller.** B reads source text and text can be misread; the measured
  table above says there is materially less to misread in this file than in `decode.ml`.

## Discharging B's four conditions

Recorded here rather than restated in #53, per 0007's precedent.

**1 — Born falsified, including the vacuity control.** Every assertion had its defect
introduced and its failure watched. The vacuity case is `Floor = 400` against 589
measured: an extractor that recognizes *nothing* produces zero arms and zero
unrecognized lines, and a drift check comparing empty to empty agrees perfectly.
`TestVacuityIsCaughtByTheNamedMechanism` covers four mutations and — per grave #34 —
discriminates on the message text rather than on `errors.Is`, since `ErrVacuous` is
reported by both the locate failure and the floor. Each case also guards that the
mutation *applied*, because a mutation that silently did not apply is a falsification
test passing for the wrong reason.

One floor, not `opcodegen`'s per-region map: this grammar has one region. The per-region
shape there exists because a SIMD refactor could empty `0xfd` while leaving the
single-byte table intact, and there is no analogous partition here to under-count
independently.

**2 — Nothing is hand-written, so there is no irregular remainder to cite.** `decode.ml`
had four structural arms that no extraction could reach; the keyword block has none. The
hand-authored facts in this PR are instead the *premises* — the two-producer split, the
`/` charset difference, the eleven mnemonics — and each is a test that reads the fact out
of the authority's text rather than restating it. `TestSlashSplitsTheTwoUnknownOperatorPaths`
is the pattern: its first draft looped over the extracted table asserting no keyword holds
a symbol, which `Extract` rejects *before returning*, so the loop could never see one — a
green surviving the bug it names. The falsifiable question is not "is the table clean" but
"do the two charsets still disagree about `/`".

**3 — The committed table carries a generation header.** Authority, revision, extractor
version, keyword count, kind count. The revision is read from
`scripts/fetch-spec-ref.sh` by `internal/gen`, never passed as a flag: a SHA typed at a
second site is a citation that can drift from the pin it describes. The fallthrough's
line number in the header's prose is likewise *recorded by the extractor*, not typed —
generated prose that cites the authority is subject to the same drift as any other
citation, with a code generator's alibi.

**4 — CI re-runs the extraction and asserts equality**, as `make keyword-drift`, wired
into the `conformance` job beside `make opcode-drift`. Its own target and its own CI step
rather than a widening of the opcode one: two authorities, two verdicts, and a log that
says which table drifted.

**And one addition, because condition 4 has a gap this table widens.** `keyword-drift`
needs `third_party/spec`, so it cannot live in `make check` — which means on a fresh
clone, `DO NOT EDIT` is a *request*. A hand edit adding `get_local` to `keywords.go`
builds, lints, and passes `make check`, and the row whose absence three oracle-covered
vectors score against is exactly the row that could be added.

The linter is no help, and the measurement is worth recording because the first reading of
it was wrong. `.golangci.yml`'s `exclusions.generated: lax` *does* suppress `unused` on any
file carrying the `Code generated ... DO NOT EDIT.` marker — but that is not why the linter
is quiet here: the integrity tests read the map, so `unused` is correct to say nothing.
Checked in all four combinations of marker-present and tests-present; only marker-stripped
*and* tests-absent reports `var keywords is unused`. Which relocates the risk rather than
removing it: deleting keywords_test.go would take this package from "table with a consumer"
to "table with none", with the exclusion ensuring nothing objects.

So `internal/text/keywords_test.go` asserts the **committed file's** properties with no
corpus at all: a size floor, the eleven absences, the `keyword` shape, and a
content spot-check with its kinds. A drift check and an integrity check are different
questions, and only one of them can be asked without a fetch. The distinction generalizes
back to `optable.go`, which today has only the drift check — noted, not fixed here, since
this ADR's subject is the keyword table.

## Consequences

- `internal/text/keywords.go` is committed and generated: 589 keywords, 173 kinds, at
  `bdd7164`. A fresh clone builds with no fetch.
- `make keywords` regenerates; `make keyword-drift` asserts agreement and **refuses to
  run** without both corpora rather than skipping. Two guards, because `keywordgen`'s
  tests read the reference *and* one suite vector, and a guard naming only the first would
  send a reader to `make spec-ref` for an absence `make spec-tests` fixes.
- **`internal/gen` exists, and it is 0006's condition being discharged rather than a new
  utility.** 0006 declined to share the pin reader and the formatter on the grounds that
  the second consumer did not exist — legitimate, because building it early means shaping
  it from its only consumer's requirements. The second consumer has now arrived, so the
  seam is cut where the fact actually is: one reader of `scripts/fetch-spec-ref.sh`, one
  formatter, both generators. What is deliberately *not* in there is anything either
  generator's grammar knows — a shared `parseArm` would be the wrong seam, two grammars
  pretending to be one.
- **`internal/testenv` grew a fourth licensed door and a per-file floor map.**
  `RequireSuiteFile` is the door for a citation check against *one* named vector, which is
  a different question from `RequireSuite`'s count over 257 files: a full corpus missing
  the cited file passes the count and then fails with a bare read error. And `refFloors`
  keys the size floor by path constant rather than taking it as a parameter — a floor
  passed at the call site is a fact about a file typed somewhere other than where the file
  is named, and the failure mode is a caller passing 0 and defeating the control it is
  calling. An unregistered path is a hard failure, never a default floor.
- **The table ships one increment ahead of its consumer, declared and tracked.** Nothing
  in the engine reads `keywords` yet; the lexer that will is #53's next increment. That is
  Scott's sequencing — the table exists before the code that consumes it, so the
  555-vector bucket earns against an authority rather than against a hand-written set —
  and it is legitimate only because the deferral is *named at the definition site*
  (`internal/text/doc.go`) with a tracking issue and a control. Unreachability is a grave
  only when it is silent (#6).
- **The 11 mnemonics need more than this table**, per the two-producer finding: maximal
  munch across `keyword` and `reserved`, both `unknown operator` sites, and `'$'(id)` so
  identifiers are not mis-lexed (needed by 6 of the 11). That is why 8a's extraction
  landed *with* the mnemonics rather than after them.
- **Not settled here:** the `(module quote …)` classification's cost, which the board
  measurement falsified as scoped. See the postscript.

## Postscript: the pre-registered claim, and how it failed

#53 stated the movement this work would deliver before it was authored — `1345 → 1334`,
eleven vectors — so that the PR could be checked against it. It is wrong in both
directions, and the pre-registration is what made that findable.

**`obsolete-keywords.wast` was never on the board.** The board is 14 files, derived from
capability (#52), and this file's 11 vectors were not among the 1345 unsupported. There is
no `1345 → 1334` to deliver, because the subtrahend was not in the minuend.

**Teaching `classify` the `(module quote …)` form is not scopeable to 11 vectors.** It
widens `scorableCommands`, which pulls **53 additional files** onto the derived board:

| | before | after |
|---|---|---|
| files | 14 | **67** |
| commands | 2144 | **28769** |
| unsupported | 1345 | **26741** (+25396) |

with **1229** newly-scorable quote vectors across 41 distinct expected strings — 555
`unknown operator`, 186 `malformed UTF-8 encoding`, 152 `unexpected token`, 92
`alignment`, 55 `constant out of range`, 32 `illegal character`, and a long tail. The 11
mnemonics are 11 of those 1229.

**The pattern, which is the reusable part.** This is the fourth consecutive time in #53's
sequencing that a step named "cheap" owed one more layer than its name — the grep-scoped
bucket table, the UTF-8 reuse claim, the fallthrough-only mnemonics, and now the board
delta. Scott's ruling on the third made it a datum: *cheap is a grammar claim like any
other, and every time it's been measured it owed one more layer than its name.* The
countermeasure is the one that caught this: state the number before authoring, in a form
a probe can refute.

The scoping consequence — what the mnemonics actually cost once the board admits 1229
vectors — is Scott's call, flagged in the PR rather than decided here.
