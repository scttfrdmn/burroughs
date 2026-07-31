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
go test ./internal/interp/dispatchbench -bench . -benchmem -count=6
```

This is a **microbenchmark on a toy loop, not the engine**, and it is kept as
an artifact so 0002's numbers can be re-run and disputed rather than
believed. Results as measured (Apple M4 Pro, darwin/arm64, Go 1.26.5) are
recorded in `docs/decisions/0002-interpreter-strategy.md`.
