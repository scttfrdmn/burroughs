# 0033 — One exit-code taxonomy for the whole CLI, and `inspect` adopts it

Date: 2026-08-17 · Status: **accepted under a delegated decision** —
[#373 comment](https://github.com/scttfrdmn/burroughs/issues/373#issuecomment-5321006170)

> *"#373's inspect exit code — yours. The only constraint that matters is that a refusal to answer
> never exits 0."* — Scott, relayed and recorded on #373.

**The status field cites a delegation, not a stamp on this document, and the difference is stated
because a status field is a citation to an approval.** What the token grants is authority over *this
question*; it is not an endorsement of the option chosen below, which is the agent's and reviewable
like any other. The constraint it carries is binding and is discharged in the consequences.

## Context

`run` publishes six exit codes (`run.go:147-155`, tabulated in `README.md`), and their whole purpose
is that a single non-zero code cannot tell a wrong module from an incomplete engine: `3` says *fix
your module*, `6` says *rebuild with that gate on*, `5` says *this engine is incomplete*, `1` says
*this invocation's own failure*.

`inspect`, in the same package and behind the same `dispatch`, returned **`1` for everything**:

```go
if err := inspect(stdout, argv[1]); err != nil {
    fmt.Fprintln(stderr, "burroughs:", err)
    return exitError
}
```

So one malformed `.wasm` was exit `3` from `run` and exit `1` from `inspect` — and `1` means "this
invocation's own failure", i.e. the tool blamed *itself* for the user's module. A module using a gated
proposal was `6` from one and `1` from the other, which is grave #301 (a gated module is well-formed)
re-committed one subcommand to the left.

The divergence was **declared** rather than overlooked, in the comment above: the PR that threaded
writers through `dispatch` recorded that which code `inspect` owes a refusal is a question about the
CLI's public contract and that a refactor is not the artifact that answers it. This is that artifact.

Two further facts constrain the answer:

- **`inspect` decodes and must not validate.** A module that fails typing is still a module whose
  sections a reader wants dumped — that is the tool's whole use during a slice campaign. So it cannot
  route through `Config.Instantiate`, which validates and instantiates, and `Instantiate` is where the
  library's classification lives.
- **The codes are a public interface.** At `v0.x` the answer to a compatibility question is "we may
  break it", which is the privileged position 0004 describes — not "it does not matter".

## Options

1. **`inspect` adopts the taxonomy.** It decodes, so it can already distinguish a gated construct from
   a malformed image; `binary.ErrFeatureDisabled` carries that. One taxonomy across the CLI.
2. **The taxonomy is documented as `run`-only**, and `inspect` stays a dump tool whose contract is "it
   worked or it didn't". Requires a scope line in the README, which today tabulates the codes in a way
   that reads as the CLI's table.
3. *(Not in the issue; ruled out by the delegation's constraint.)* `inspect` dumps whatever it could
   decode and exits `0`, treating a refusal as a partial answer.

## Decision

**Option 1.** `inspect` classifies its refusals onto the public sentinels, `dispatch` routes them
through the same `exitCode` that `run`'s errors travel, and the README's table is scoped to the CLI
rather than to one subcommand.

Option 3 is what the constraint forbids, and naming it is not a formality: it is the tempting answer
for a dump tool, since a truncated section list looks like output. A refusal that exits `0` is a
verdict channel reporting success about a question it declined to answer.

Option 2 was the cheaper record — one sentence in the README, no code — and it is rejected because the
scope line would have to explain that two sibling subcommands classify the same file differently,
which is a defensible sentence for a tool with two contracts and this CLI has one. `inspect`'s output
is *about* a module; the exit code is the machine-readable half of that sentence, and a script that
inspects before running should not have to translate.

## What `inspect` can and cannot return

| code | from `inspect` | why |
| --- | --- | --- |
| `0` | yes | the sections were dumped |
| `1` | yes | unreadable file — the one failure here that is not about the module |
| `2` | yes | wrong arguments, from `dispatch` |
| `3` | yes | the decoder refused the image |
| `4` | **no** | nothing is executed, so nothing can trap |
| `5` | **no** | no interpreter is reached |
| `6` | yes | the decoder refused a construct from a gated proposal |

`4` and `5` being unreachable is a **scope statement, not a gap**: they are questions about running,
and `inspect` does not run. The table above is prose; what is executable is the control below.

## Consequences

- **The gate-before-malformed ordering now exists in two places** — `Config.Instantiate` and
  `inspect` — and that is grave #301's shape, declared rather than discovered. The alternatives were
  each worse: a shared classifier would have to be a decode-only entry point in the **public** API,
  minting a compatibility promise for a debugging convenience, and pushing the decision into
  `exitCode` gets the number right by making the message say "malformed module" about a well-formed
  one, which is a correct verdict on fabricated testimony.
- **The tripwire is `TestBothSubcommandsClassifyOneModuleTheSameWay`**, which drives one malformed and
  one gated file through both subcommands via `dispatch` and asserts the codes are **equal and
  absolute**. Equality alone would pass with both copies wrong together; the levels are pinned for the
  reason a delta from one broken instrument can be sound while both levels are wrong.
- The same control asserts the **deliberate disagreement**: an invalid-but-well-formed module is `3`
  from `run` and `0` from `inspect`, because `inspect` answered its question completely. That row is
  what keeps the scope statement executable instead of prose.
- **Grave #383 is fixed in the same PR** and is not incidental: harmonizing the two subcommands would
  otherwise have *spread* a doubled `burroughs: burroughs:` prefix to a subcommand that did not have
  it. One `diagnose` now owns the program name.
- No public API change. `burroughs.ErrGated` and `burroughs.ErrMalformed` were already exported; the
  CLI is a consumer of them in one more place.
- The spec board cannot move: the conformance harness does not invoke the CLI. The `unsupported`
  column's zero here is **structural**, and the figure that has a subject is the two decoder refusal
  classes that now agree across subcommands where none did.
