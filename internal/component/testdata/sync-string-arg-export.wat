;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; sync-string-arg-export: ADR 0103's host-path face. A top-level SYNC lift `first: func(s: string) -> u32`
;; returns its argument's first byte. An embedder calling it passes a string the engine must lower into
;; the component's memory through its realloc, inside the lift's run slot, before the core func runs:
;; `first("abc")` reads 97. Before ADR 0103 a sync lift refused a pending lowering by name, because "the sync
;; path has no task and no entry slot". ADR 0101 had given it the slot, and the adapter lowers inside it.
;; Authored with `wasm-tools parse`; read with `wasmtime run --invoke 'first("abc")'`.
(component
  (core module $m
    (memory (export "m") 1)
    (global $bump (mut i32) (i32.const 1024))
    (func (export "realloc") (param i32 i32 i32 i32) (result i32)
      (local $p i32) (local.set $p (global.get $bump))
      (global.set $bump (i32.add (global.get $bump) (local.get 3))) (local.get $p))
    (func (export "first") (param i32 i32) (result i32) (i32.load8_u (local.get 0))))
  (core instance $i (instantiate $m))
  (alias core export $i "m" (core memory $mem))
  (alias core export $i "realloc" (core func $realloc))
  (alias core export $i "first" (core func $firstf))
  (type $ft (func (param "s" string) (result u32)))
  (func (export "first") (type $ft) (canon lift (core func $firstf) (memory $mem) (realloc $realloc))))
