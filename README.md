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

## Serving HTTP

A WASI 0.3 component that exports `wasi:http/handler` is served as `wasmtime serve` serves one:

```text
$ ./bin/burroughs serve --addr 127.0.0.1:8080 service.wasm
Serving HTTP on http://127.0.0.1:8080/
```

Each request is a call of the component's `handle`, on an instance of its own, and the component may make outgoing
requests. `--dir`, `--scratch`, `--inherit-network` and `--allow-ip-name-lookup` grant what they grant to `run`. From
Go, `Component.Handler()` is an `http.Handler`, served on whatever listener, TLS and timeouts the embedder chooses.

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

## How the thesis is tested

Three things make the experimental apparatus:
- **Burroughs**, the engine.
- **The Phase 4 Go fork**, a modified Go toolchain whose `wasip1` port starts real threads through Burroughs' spawn
  import, and whose `wasip3` port does the same inside a component through Burroughs' component spawn door. Stock
  Go's `wasip1` port is single-threaded. The fork is not published.
- **The Go-runtime torture suite** ([#406](https://github.com/scttfrdmn/burroughs/issues/406)): Go programs built
  with the fork, each of whose observable results must match the same program built with stock Go.

[The v0.12.0 threads witness](https://burroughs.run/docs/threads-witness/) is one reading of the apparatus, with what
it does not show.

## Status

*Written against `v0.15.0`.*

- **The threads phase**, v1 (decision 0099). The MVP core suite is green.
- **On by default:** threads, SIMD, relaxed SIMD, extended constant expressions, tail calls, multi-memory, memory64,
  and the component model. Exception handling and GC stay off.
- **WASI preview 1** runs, with explicit preopens and no default grant. A guest can start its own threads on that
  path.
- **WASI 0.3** is served to a component: `wasi:cli`, `wasi:clocks`, `wasi:random`, and `wasi:sockets` TCP and name
  lookup. The network is denied unless granted with `--inherit-network` or `--allow-ip-name-lookup`. A Go program
  built by the Phase 4 fork for `GOOS=wasip3` runs on it, up to a `net/http` server whose responses match stock Go's
  and wasmtime 49.0.3's byte for byte. By default a component executes guest code on one agent at a time.
- **WASI 0.3 across more than one thread** is behind `gate:cm-shared-memory`, off by default
  (`BURROUGHS_CM_SHARED_MEMORY=1`). With it on, a fork-built Go program at GOMAXPROCS 4 runs inside a component on
  four agents at once, and its output matches stock Go's byte for byte. It is a Burroughs-only extension: the
  Canonical ABI requires an unshared canonical memory, so no other engine accepts such a component. It does not run
  on Windows ([#1165](https://github.com/scttfrdmn/burroughs/issues/1165)).
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
| `3` | the module was refused: malformed, invalid, unlinkable (an import nobody supplied), or declined under `--strict` |
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
