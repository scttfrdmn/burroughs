<p align="center"><img src="https://burroughs.run/assets/wordmark.png" alt="Burroughs" width="360"></p>

# Burroughs

**Go → Wasm → Burroughs.** The WebAssembly engine for Go, written in pure Go. · [burroughs.run](https://burroughs.run)

> The B5000 favored ALGOL. Burroughs favors Go.

Burroughs is a language-directed WebAssembly runtime, with no cgo and no dependency outside the standard library. Its
host contract is designed to the specification of the Go runtime: real OS threads, engine-native preemption and
stop-the-world, and a defined memory model at every host boundary. Any spec-conforming guest runs correctly here,
because the upstream test suite is the neutrality guarantee. Where a design tradeoff exists, Go wins.

## Install

```console
$ go install github.com/scttfrdmn/burroughs/cmd/burroughs@latest
$ go get github.com/scttfrdmn/burroughs
```

## Quickstart

From a clone, with the example module at `examples/add/add.wat`:

```console
$ go build -o bin/burroughs ./cmd/burroughs

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

## From Go

```go
wasm, err := os.ReadFile("examples/add/add.wasm")
if err != nil {
	log.Fatal(err)
}
in, err := burroughs.Instantiate(wasm)
if err != nil {
	log.Fatal(err)
}
sum, err := in.Call("add", burroughs.I32(2), burroughs.I32(40))
if err != nil {
	log.Fatal(err)
}
fmt.Println(sum[0].Int32())
```

Both are executed: the transcript by `TestREADMETranscriptIsExecutable`, and the Go block is `Example_quickstart`,
whose output `go test` asserts.

## What's different

- **Designed for Go's runtime.** The [host contract](docs/burroughs-contract-v0.1.md) is written to what Go's runtime
  expects of its host: OS threads, engine-driven safepoints, a boundary memory model, and growable stacks. Not all
  of it is built yet. Threads are, and growable stacks are the v2 rung.
- **Pure Go, and an interpreter.** No cgo, no compiler, and no rung on the published ladder adds one.
- **Every outcome is told apart.** Malformed, invalid, declined, unsupported and gated are separate errors with separate
  exit codes, and a trap is a type a caller can match.
- **Correctness-neutral.** The spec suite is the oracle, and nothing defaults on without its own suite green.

## Status

*Written against `v0.12.0`.*

- **The threads phase**, v1 (decision 0099). The MVP core suite is green.
- **On by default:** threads, SIMD, relaxed SIMD, extended constant expressions, tail calls, multi-memory, memory64,
  and the component model. Exception handling and GC stay off.
- **WASI preview 1** runs, with explicit preopens and no default grant. A guest can start its own threads on that
  path.
- **Platforms:** 64-bit Linux and macOS are built and tested. Windows is measured and unsupported: a non-gating lane
  runs the suite, and each recurring failure is mapped to an open issue in the
  [triage map](.github/windows-triage.tsv).

The known limits are on [burroughs.run](https://burroughs.run/docs/limits-and-status/) and in each release's block of
the [changelog](CHANGELOG.md).

## Exit codes

| code | meaning |
| --- | --- |
| `0` | the module ran |
| `1` | this invocation's own failure — unreadable file, unspellable value, no such export |
| `2` | wrong arguments |
| `3` | the module was refused: malformed, invalid, or declined under `--strict` |
| `4` | the module executed correctly and the program went wrong — a trap, in a core module or a component, or a component call refused because an earlier trap locked the instance |
| `5` | the engine reached something it does not implement in this phase |
| `6` | the module is fine; this build has that proposal's gate off |
| `7` | a component call the **caller** cancelled — the module ran correctly and the invoker stopped it |
| `8` | a call on a component that has been closed — the invoker's own mistake |
| `9` | `Close` reached its bound with a guest still cancelling; the instance was torn down anyway |

`7`, `8` and `9` classify the package's sentinels and are not reachable through the CLI today. The
[CLI reference](https://burroughs.run/docs/cli/) says why, and what each code tells whom.

## More

[Docs](https://burroughs.run/docs/) · [Contract](docs/burroughs-contract-v0.1.md) · [Changelog](CHANGELOG.md) ·
[Blog](https://burroughs.run/blog/) · [Working on Burroughs](https://burroughs.run/docs/working-on-burroughs/)

## License

Apache 2.0 — see `LICENSE` and `NOTICE`. © 2026 Scott Friedman.
