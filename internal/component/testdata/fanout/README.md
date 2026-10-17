<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# fanout — the parallelism a cross-component fence would give up

One caller and four async callees, each in its own child instance, doing independent work. The chair asked
for it on ADR 0095's amendment (ruling of 2026-10-17), to measure the cost of a fence before building one.
`BenchmarkFanOut` drives it, and `TestFanOutReturnsEveryCalleesResult` is its witness.

- **`go(k, mode, iters)`** async-lowers `run(mode, iters)` on the first `k` child instances. It joins
  every subtask that did not return inline, WAITs until all `k` have resolved, and returns the sum of
  their results.
- **`mode 0`, work in the first segment:** the child counts `iters` down, calls `task.return` and EXITs
  inside its first core call. The reference runs this segment inside the caller's lower.
- **`mode 1`, work after the first suspension:** the child YIELDs first, and counts down in its callback.
  The reference runs this only once the caller has suspended.

Separate instances are what can run in parallel, because the run slot is per instance (ADR 0101).

## Readings

`wasm-tools parse fanout.wat -o fanout.wasm` (wasm-tools 1.258.0). With
`wasmtime run -W component-model-async=y --invoke 'go(<k>, <mode>, 100000)' fanout.wasm`, wasmtime 49.0.3
reads 100000 for k=1 and 400000 for k=4, in both modes. Burroughs reads the same sums
(`TestFanOutReturnsEveryCalleesResult`).
