;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; future-cancel-write-async (#1364, item 2): future.cancel-write (0x19) in its ASYNC form, the control for
;; future-stream-checks.wat's sync rows. wasmtime 49.0.3 loads an async future.cancel-write only with
;; `component-model-more-async-builtins`, so it is read under its own invocation. One future<u8> per export minted by
;; future.new.
;;
;;   - cancel-write-async-parked: a write parks (BLOCKED); the async cancel returns CANCELLED (2) inline, the future's
;;     reader being this instance's (def:End.cancel notifies inline when `self.other.owner is not None`).
;;   - cancel-write-async-joined: the same with the writer joined to a set, which traps the SYNC form and not this one
;;     (def:cancel_copy:"trap_if(end.in_waitable_set() and not async_)"); the set is then polled, so a second event
;;     would show: the cancel's result | (the poll's code << 8).
;;
;; Every export is async-typed and lifted sync. Measured on wasmtime 49.0.3 (`--invoke`, with the feature) and on the
;; pin (refread.py's future-stream-checks rows), committed as future-stream-checks.reading. Assembled with `wasm-tools
;; parse future-cancel-write-async.wat -o future-cancel-write-async.wasm` (wasm-tools 1.258.0). The memory is 1 1.
(component
  (core module $mm (memory (export "m") 1 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (type $f (future u8))
  (core func $fnew (canon future.new $f))
  (core func $fwrite (canon future.write $f async (memory $m)))
  (core func $fcwa (canon future.cancel-write $f async))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wspoll (canon waitable-set.poll (memory $m)))
  (core module $gm
    (import "" "mem" (memory 1 1))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))
    (import "" "fcwa" (func $fcwa (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wspoll" (func $wspoll (param i32 i32) (result i32)))
    (global $w (mut i32) (i32.const 0))
    (func $split
      (global.set $w (i32.wrap_i64 (i64.shr_u (call $fnew) (i64.const 32))))
      (i32.store8 (i32.const 64) (i32.const 7)))
    (func (export "cancel-write-async-parked") (result i32)
      (call $split)
      (drop (call $fwrite (global.get $w) (i32.const 64)))
      (call $fcwa (global.get $w)))
    (func (export "cancel-write-async-joined") (result i32) (local $ws i32) (local $c i32)
      (call $split)
      (local.set $ws (call $wsnew))
      (call $wsjoin (global.get $w) (local.get $ws))
      (drop (call $fwrite (global.get $w) (i32.const 64)))
      (local.set $c (call $fcwa (global.get $w)))
      (i32.or (local.get $c) (i32.shl (call $wspoll (local.get $ws) (i32.const 256)) (i32.const 8)))))
  (core instance $gi (instantiate $gm (with "" (instance (export "mem" (memory $m))
    (export "fnew" (func $fnew)) (export "fwrite" (func $fwrite)) (export "fcwa" (func $fcwa))
    (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "wspoll" (func $wspoll))))))
  (type $at (func async (result u32)))
  (func (export "cancel-write-async-parked") (type $at) (canon lift (core func $gi "cancel-write-async-parked")))
  (func (export "cancel-write-async-joined") (type $at) (canon lift (core func $gi "cancel-write-async-joined"))))
