;; add.wat — the module the README's end-to-end example runs.
;;
;; Provenance: synthetic. Authored here, for this repo, because the point of the
;; example is a module small enough to read in full beside its own output. It
;; cites no `.wast` line and claims none: the upstream suite is the engine's
;; oracle, not a source of illustrations.
;;
;; `add.wasm` beside this file is **derived** from this text by the engine's own
;; assembler (internal/text.EncodeModule) — run `go run ./examples/add/gen.go` to
;; regenerate it, and TestExampleWasmIsDerivedFromItsWat holds the two equal.
;;
;; Three exports, one per outcome a caller has to be able to tell apart:
;;
;;   add  — a result. Two operands and an i32.add.
;;   fib  — a computation the interpreter has to actually interpret: a call, a
;;          branch, and recursion, so a right answer is not a folded constant.
;;   div  — a trap. i32.div_s by zero is the spec's "integer divide by zero",
;;          which is the module executing correctly while the program goes wrong.
(module
  (func $add (export "add") (param i32 i32) (result i32)
    local.get 0
    local.get 1
    i32.add)

  (func $fib (export "fib") (param i32) (result i32)
    local.get 0
    i32.const 2
    i32.lt_s
    if (result i32)
      local.get 0
    else
      local.get 0
      i32.const 1
      i32.sub
      call $fib
      local.get 0
      i32.const 2
      i32.sub
      call $fib
      i32.add
    end)

  (func $div (export "div") (param i32 i32) (result i32)
    local.get 0
    local.get 1
    i32.div_s))
