;; sync-lower-wait-for (#1233, ADR 0121): wait-for lowered WITHOUT the async option. Burroughs refuses it at
;; instantiate, by name: the host serves wait-for only as an async import, and the pin's blocking sync lower of an
;; async import is not built. Measured on wasmtime 49.0.3 (`--invoke`, flags as wait-cancel.reading's), 10 of 10:
;; `go` (sync-typed) traps "cannot block a synchronous task before returning"; `go-async-typed` blocks and returns 7.
(component
  (import "wasi:clocks/monotonic-clock@0.3.0" (instance $mc
    (export "wait-for" (func async (param "how-long" u64)))))
  (alias export $mc "wait-for" (func $wf))
  (core func $wfl (canon lower (func $wf)))
  (core module $gm
    (import "" "wf" (func $wf (param i64)))
    (func (export "go") (result i32) (call $wf (i64.const 10000000)) (i32.const 7)))
  (core instance $gi (instantiate $gm (with "" (instance (export "wf" (func $wfl))))))
  (type $st (func (result u32)))
  (type $at (func async (result u32)))
  (func (export "go") (type $st) (canon lift (core func $gi "go")))
  (func (export "go-async-typed") (type $at) (canon lift (core func $gi "go"))))
