# Burroughs

**The Wasm engine for Go.** · burroughs.run

> The B5000 favored ALGOL. Burroughs favors Go.

Burroughs is a language-directed WebAssembly runtime, written in pure Go,
whose host contract is designed to the specification of the Go runtime:
real OS threads, engine-native preemption and stop-the-world, a defined
memory model at every host boundary, growable continuation stacks, and a
netpoller-shaped WASI 0.3 event loop. Any spec-conforming guest runs
correctly here — the upstream test suite is the neutrality guarantee — but
where a design tradeoff exists, Go wins. Other guests have alternatives.

The name is the lineage: the Burroughs Large Systems (B5000, 1961) were
stack machines built so that one high-level language would win —
language-directed design, sixty-five years before this repo. Wasm is a
stack machine. The gopher gets its burrows back, one letter askew.

## Where this is

**v0, the interpreter phase.** The pipeline that exists today is decoder →
internal form (decision 0002) → validator → interpreter, in pure Go, no cgo,
no dependency outside the standard library. There is no compiler, and v0 does
not have one on its ladder — correctness and spec-tracking agility are this
phase's product.

What that buys today:

- **A module runs.** `burroughs run` reads a `.wasm` file, instantiates it, and
  calls an exported function. A Go host gets the same path as a package —
  `Instantiate`, then `Call` — because the CLI is a *consumer* of the public API
  rather than a second route into the engine (decision 0029), so neither is
  covered without the other.
- **The outcomes are told apart.** Malformed, invalid, declined, unsupported and
  gated are five error classifications, not one `error`, and `run` gives each its
  own exit code: "fix your module", "rebuild with that gate on" and "this engine
  is incomplete" are different instructions to whoever is reading.
- **A validator arriving in slices says so.** Not every construct has a typing
  rule yet. A module using one that does not is neither accepted nor refused — it
  runs, and names the construct that went unchecked (`Instance.Decline`). Pass
  `--strict`, or set `Config.Strict`, to refuse instead of running.

What is not here, stated because a README that implies otherwise is the more
expensive kind of wrong:

- **No host imports and no WASI.** Nothing in the public API supplies an import,
  so a module that imports a function reaches `ErrUnsupported` at the point of
  use.
- **No threads, no stack switching, no component model.** Those are contract
  §§2–5, §7 and §6 — the v1, v2 and v3 rungs. None has started.
- **Proposal gates default off, and a flip is its own stamped event** (contract
  §9). `internal/binary.DefaultFeatures` is the single line that says which are
  on today; every flip is a `CHANGELOG.md` **Added** entry naming its `gate:`.

## Try it

Build the CLI and run the example module — three exports, one per outcome a
caller has to tell apart, short enough to read in full at
`examples/add/add.wat`:

```console
$ go build -o bin/burroughs ./cmd/burroughs

$ ./bin/burroughs version
burroughs v0.0.1 — the machine Burroughs never built

$ ./bin/burroughs inspect examples/add/add.wasm
examples/add/add.wasm: wasm v1, 4 section(s)
  [0] type           12 bytes
  [1] function        4 bytes
  [2] export         19 bytes
  [3] code           46 bytes

$ ./bin/burroughs run examples/add/add.wasm
add
fib
div

$ ./bin/burroughs run examples/add/add.wasm add i32:2 i32:40
i32:42

$ ./bin/burroughs run examples/add/add.wasm fib i32:20
i32:6765

$ ./bin/burroughs run examples/add/add.wasm div i32:7 i32:0  # exits 4
burroughs: trap: integer divide by zero
```

With no function named, `run` lists the module's exported functions. Arguments
and results are typed — `i32:42`, `i64:-1`, `f32:nan:0x200000`, `v128:0x0:0x0`,
`extern:3`, `null:func` — and the spelling `run` prints is the spelling `run`
accepts.

That transcript is not decoration: `TestREADMETranscriptIsExecutable`
(`cmd/burroughs/readme_test.go`) parses this file, runs every `burroughs` line in
it through the CLI's own dispatcher, and compares the output and the exit code to
what is written above. It rots loudly or not at all.

`examples/add/add.wasm` is committed so a fresh clone needs one command, and it
is a **derived** artifact: `go run ./examples/add/gen.go` re-assembles it from
`add.wat` with the engine's own assembler, and a test holds the two equal.

### Exit codes

One code per question a caller can ask, because a single non-zero code cannot
tell a wrong module from an incomplete engine. **The table is the CLI's, not
`run`'s** — `inspect` classifies a refused module the same way, so a script that
inspects before running does not have to translate (decision 0033):

| code | meaning |
| --- | --- |
| `0` | the module ran |
| `1` | this invocation's own failure — unreadable file, unspellable value, no such export |
| `2` | wrong arguments |
| `3` | the module was refused: malformed, invalid, or declined under `--strict` |
| `4` | the module executed correctly and the program went wrong — a trap |
| `5` | the engine reached something it does not implement in this phase |
| `6` | the module is fine; this build has that proposal's gate off |

`inspect` never returns `4` or `5`: it decodes and dumps, so nothing it does can
trap or reach an unimplemented instruction. It exits `0` on a module that fails
*typing*, because it answered its question — the sections — completely. What it
does not do is exit `0` on a module it could not read.

## Use from Go

The import path is `github.com/scttfrdmn/burroughs`, and the whole surface is
`Instantiate`, `Instance.Exports`, `Instance.Call`, and the error classifications
you branch on. What follows is the runnable `Example` from `example_test.go`, body
and comments — its printed output is asserted by `go test`, so it cannot drift
from the engine:

```go
wasm, err := os.ReadFile("examples/add/add.wasm")
if err != nil {
	log.Fatal(err)
}

// Instantiate decodes, validates, and instantiates. The validator runs before
// the interpreter does, which is what this path exists to guarantee.
in, err := burroughs.Instantiate(wasm)
if err != nil {
	log.Fatal(err)
}

// The third outcome: nil means every construct in the module had a typing rule.
// A non-nil decline names what went unchecked, and the module still ran.
if d := in.Decline(); d != nil {
	fmt.Println("declined:", d)
}

fmt.Println("exports:", in.Exports())

sum, err := in.Call("add", burroughs.I32(2), burroughs.I32(40))
if err != nil {
	log.Fatal(err)
}
fmt.Println("add(2, 40) =", sum[0].Int32())

// A trap is the module executing correctly while the program goes wrong, so it
// is a type a caller can match rather than a string to read.
var trap *burroughs.Trap
if _, err := in.Call("div", burroughs.I32(7), burroughs.I32(0)); errors.As(err, &trap) {
	fmt.Println("div(7, 0) trapped:", trap.Reason)
}
```

The five classifications are `ErrMalformed`, `ErrInvalid`, `ErrDeclined`,
`ErrUnsupported` and `ErrGated`; each wraps the engine's own message, so
`errors.Is` gives you the category and the text gives you the rule, the
instruction, or the proposal. `*Trap` carries the spec's trap text verbatim,
which is the string the conformance suite matches.

Every fenced `go` block in this file is verbatim from a file the test build
compiles — `TestREADMEGoBlocksAreRealCode` checks it.

## Conformance

The upstream WebAssembly spec test suite is the oracle, and **this file quotes no
figure from it on purpose**: a count typed into prose is a count that rots, and
the instrument is one command away.

```console
$ make spec-tests                                    # vendor the suite (gitignored)
$ go test ./internal/spec/ -run TestPhase1Files -v   # print the board
$ make conformance                                   # assert it, both lanes
```

The board prints per-file counts, a total, and the failure buckets in size order.
Five verdicts, and the distinction between them is the point (decision 0010):

- **pass** — the engine answered, and the answer was right.
- **fail** — the engine answered, and the answer was wrong. This column is the
  work plan; anything that inflates it destroys its signal.
- **gated** — the engine could answer, but a proposal's gate is off in this
  build. Absence by configuration.
- **unimplemented** — the harness asked and the engine has no component to
  answer with. Absence by construction, and it must be zero before `v0.1.0`.
- **unsupported** — the harness cannot ask: no `Kind` recognizes the command
  form, so there is no question yet.

`make conformance` runs both lanes — the default gate set, and an all-gates-on
lane where the `gated` count must be zero — and refuses to run without the
vendored suite, because a suite that skips passes by asking nothing.

## Reading the repo

- **`docs/burroughs-contract-v0.1.md`** — the normative host contract.
  Start there.
- **`docs/decisions/`** — accepted decision records (ADRs).
- **`CLAUDE.md`** — implementation agent brief: orientation, the phase ladder,
  where work is tracked, and the five behaviours that change what a PR does.
  A pointer page, not the corpus.
- **`docs/laws/`** — the disciplines, by family: each law's specimen, the
  finding that minted it, and the token it was granted on. `CLAUDE.md` links
  every family; read the one whose subject is in play.
- **`CHANGELOG.md`** — what landed, newest first, with gate flips as **Added**
  and graves as **Fixed** beside their issue.

Project state lives in **GitHub issues and milestones**, not in files:
milestones are the phase ladder, and `label:type:grave` is the graveyard of
bugs found and lessons learned.

`make check` is the gate — fmt, build, vet, lint, test, deadcode — and it stays
green on a fresh clone with nothing vendored. `make conformance` is the other
half, and needs the suite.

## License

Apache 2.0 — see `LICENSE` and `NOTICE`. © 2026 Scott Friedman.
