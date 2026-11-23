;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; future-cancel-read-async (#1362): future.cancel-read (0x18) in its ASYNC form, which future-drop-cancel.wat's
;; sync rows cannot carry: wasmtime 49.0.3 loads an async future.cancel-read only with
;; `component-model-more-async-builtins`. One future<u8> per export minted by future.new.
;;
;;   - cancel-read-async-parked: a read parks (BLOCKED); the async cancel returns CANCELLED (2) inline, the future's
;;     writer being this instance's (def:End.cancel notifies inline when `self.other.owner is not None`).
;;   - cancel-read-async-joined: the same with the reader joined to a set, which traps the SYNC form and not this
;;     one (def:cancel_copy:"trap_if(end.in_waitable_set() and not async_)"); the set is then polled, so a second
;;     event would show: the cancel's result | (the poll's code << 8).
;;
;; Every export is async-typed and lifted sync. Measured on wasmtime 49.0.3 (`--invoke`, with the feature) and on the
;; pin (refread.py's future_drop_cancel rows), committed as future-drop-cancel.reading. Assembled with `wasm-tools
;; parse future-cancel-read-async.wat -o future-cancel-read-async.wasm` (wasm-tools 1.258.0). The memory is 1 1.
(component
  (core module $mm (memory (export "m") 1 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (type $f (future u8))
  (core func $fnew (canon future.new $f))
  (core func $fread (canon future.read $f async (memory $m)))
  (core func $fcra (canon future.cancel-read $f async))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wspoll (canon waitable-set.poll (memory $m)))
  (core module $gm
    (import "" "mem" (memory 1 1))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fcra" (func $fcra (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wspoll" (func $wspoll (param i32 i32) (result i32)))
    (global $r (mut i32) (i32.const 0))
    (func $split
      (global.set $r (i32.wrap_i64 (call $fnew))))
    (func (export "cancel-read-async-parked") (result i32)
      (call $split)
      (drop (call $fread (global.get $r) (i32.const 128)))
      (call $fcra (global.get $r)))
    (func (export "cancel-read-async-joined") (result i32) (local $ws i32) (local $c i32)
      (call $split)
      (local.set $ws (call $wsnew))
      (call $wsjoin (global.get $r) (local.get $ws))
      (drop (call $fread (global.get $r) (i32.const 128)))
      (local.set $c (call $fcra (global.get $r)))
      (i32.or (local.get $c) (i32.shl (call $wspoll (local.get $ws) (i32.const 256)) (i32.const 8)))))
  (core instance $gi (instantiate $gm (with "" (instance (export "mem" (memory $m))
    (export "fnew" (func $fnew)) (export "fread" (func $fread)) (export "fcra" (func $fcra))
    (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "wspoll" (func $wspoll))))))
  (type $at (func async (result u32)))
  (func (export "cancel-read-async-parked") (type $at) (canon lift (core func $gi "cancel-read-async-parked")))
  (func (export "cancel-read-async-joined") (type $at) (canon lift (core func $gi "cancel-read-async-joined"))))
