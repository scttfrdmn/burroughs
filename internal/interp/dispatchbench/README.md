# dispatchbench — the measurement behind decision 0002

Four interpreter strategies over the *same* toy program (sum 1..1000 in a
`br_if` loop), same value-stack discipline, differing only in dispatch and
immediate handling:

| variant | strategy |
|---|---|
| `InPlace` | bytecode is the program; LEB immediates decoded every execution |
| `Rewrite` | pre-decoded into an `[]ins{op, imm}` internal form |
| `Closures` | `[]func(*frame)` closure compilation |
| `InPlaceSideTable` | bytecode is the program; immediates pre-decoded into a sparse `pc → {imm, nextPC}` table |

`Wide*` variants repeat the comparison with multi-byte LEB immediates (large
local indices and constants), because single-byte immediates are in-place's
best case and would flatter it.

`TestAllAgree` / `TestSideAgrees` / `TestWideAgree` are the positive
controls: all four must compute 500500 before any timing means anything. The
judge needs a judge (contract §9, G-4).

```
go test ./internal/interp/dispatchbench -run Test -v     # correctness
make bench                                               # n=10 + benchstat
```

**benchstat or it didn't happen** (decision 0005): a single `-count=1` run is
not a measurement, and no performance claim in this project cites one. `make
bench` runs `-count=10` and pipes through `benchstat`, which reports a median
and a variance band — the band is the part that decides whether two numbers
differ at all.

Re-measured under that rule (Apple M4 Pro, darwin/arm64, Go 1.26.5, n=10):

```
                    │    sec/op     │
InPlace-12                  19.61µ ±  8%
Rewrite-12                  13.30µ ±  6%
Closures-12                 24.25µ ± 10%
InPlaceSideTable-12         22.30µ ± 10%
WideInPlace-12              22.38µ ± 10%
WideRewrite-12              13.32µ ±  8%
WideSideTable-12            22.90µ ±  7%
geomean                     19.20µ
```

This retroactively covers 0002's evidence and the conclusions hold, including
the one that matters most: **Rewrite is immune to immediate width** (13.30µ vs
13.32µ, well inside the bands) while in-place pays 14% for multi-byte LEBs
(19.61µ → 22.38µ). The side table did not recover that cost, which is how it
died on its own terms. Closures remain the slowest *and* allocate.

This is a **microbenchmark on a toy loop, not the engine**, and it is kept as
an artifact so 0002's numbers can be re-run and disputed rather than believed.
Full context in `docs/decisions/0002-interpreter-strategy.md`.
