;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; future-typed-traps (#1282 item 1, #1364 item 4): a future end carries its element type, and every future built-in
;; compares it with the type it names (def:copy, def:cancel_copy and def:drop: `trap_if(end.t !=
;; stream_or_future_t.t)`), as stream-typed-traps.wat's mismatch rows do for a stream. One future minted by future.new
;; per export, of `$u8f` unless the export says otherwise; the value written is 7.
;;
;;   - mismatch-*: a future<u8> end through a future<u32> built-in, in each of the six. The cancel rows park their
;;     copy first, and the drop-writable row completes its write first, so each end is in the state its built-in
;;     would otherwise accept: only the type differs.
;;   - same-structure-*: a future<u8> end through a second type index that also names future<u8>. The pin compares
;;     the ValTypes, which are structural, so these do not trap: the read parks (BLOCKED) and the write then completes
;;     it (COMPLETED, 0) with the byte, 0 | (7 << 8).
;;   - mismatch-new32-read8: a future<u32> minted by future.new and read through future<u8>.
;;   - nested-*: a future<future<u8>>, an element Burroughs's codec does not lay out, read through a second type
;;     index naming the same type (it parks, BLOCKED) and through future<future<u32>> (it traps).
;;
;; Every export is async-typed and lifted sync. Measured on wasmtime 49.0.3 (`--invoke`) and on the pin (refread.py's
;; future-typed-traps rows), committed as future-typed-traps.reading. Assembled with `wasm-tools parse
;; future-typed-traps.wat -o future-typed-traps.wasm` (wasm-tools 1.258.0). The memory is 1 1, so heap-backed.
(component
  (core module $mm (memory (export "m") 1 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (type $u8f (future u8))
  (type $u32f (future u32))
  (type $u8g (future u8))
  (core func $fnew (canon future.new $u8f))
  (core func $fread (canon future.read $u8f async (memory $m)))
  (core func $fwrite (canon future.write $u8f async (memory $m)))
  (core func $new32 (canon future.new $u32f))
  (core func $read32 (canon future.read $u32f async (memory $m)))
  (core func $write32 (canon future.write $u32f async (memory $m)))
  (core func $cr32 (canon future.cancel-read $u32f))
  (core func $cw32 (canon future.cancel-write $u32f))
  (core func $dropr32 (canon future.drop-readable $u32f))
  (core func $dropw32 (canon future.drop-writable $u32f))
  (core func $readg (canon future.read $u8g async (memory $m)))
  (core func $writeg (canon future.write $u8g async (memory $m)))
  (type $ffa (future $u8f))
  (type $ffb (future $u8g))
  (type $ffc (future $u32f))
  (core func $newffa (canon future.new $ffa))
  (core func $readffb (canon future.read $ffb async (memory $m)))
  (core func $readffc (canon future.read $ffc async (memory $m)))
  (core module $gm
    (import "" "mem" (memory 1 1))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))
    (import "" "new32" (func $new32 (result i64)))
    (import "" "read32" (func $read32 (param i32 i32) (result i32)))
    (import "" "write32" (func $write32 (param i32 i32) (result i32)))
    (import "" "cr32" (func $cr32 (param i32) (result i32)))
    (import "" "cw32" (func $cw32 (param i32) (result i32)))
    (import "" "dropr32" (func $dropr32 (param i32)))
    (import "" "dropw32" (func $dropw32 (param i32)))
    (import "" "readg" (func $readg (param i32 i32) (result i32)))
    (import "" "writeg" (func $writeg (param i32 i32) (result i32)))
    (import "" "newffa" (func $newffa (result i64)))
    (import "" "readffb" (func $readffb (param i32 i32) (result i32)))
    (import "" "readffc" (func $readffc (param i32 i32) (result i32)))
    (global $r (mut i32) (i32.const 0))
    (global $w (mut i32) (i32.const 0))
    ;; 64: the write buffer (7)  128: the read buffer
    (func $setends (param $s i64)
      (global.set $r (i32.wrap_i64 (local.get $s)))
      (global.set $w (i32.wrap_i64 (i64.shr_u (local.get $s) (i64.const 32))))
      (i32.store (i32.const 64) (i32.const 7))
      (i32.store (i32.const 128) (i32.const 0)))
    (func $split (call $setends (call $fnew)))

    (func (export "mismatch-read") (result i32)
      (call $split)
      (call $read32 (global.get $r) (i32.const 128)))
    (func (export "mismatch-write") (result i32)
      (call $split)
      (call $write32 (global.get $w) (i32.const 64)))
    ;; The read parks through its own type, so the reader is COPYING: only the cancel's type differs.
    (func (export "mismatch-cancel-read") (result i32)
      (call $split)
      (drop (call $fread (global.get $r) (i32.const 128)))
      (call $cr32 (global.get $r)))
    ;; The write parks through its own type, so the writer is COPYING.
    (func (export "mismatch-cancel-write") (result i32)
      (call $split)
      (drop (call $fwrite (global.get $w) (i32.const 64)))
      (call $cw32 (global.get $w)))
    (func (export "mismatch-drop-readable") (result i32)
      (call $split)
      (call $dropr32 (global.get $r))
      (i32.const 7))
    ;; A read parks and the write completes it, so the writer is DONE and would drop through its own type.
    (func (export "mismatch-drop-writable") (result i32)
      (call $split)
      (drop (call $fread (global.get $r) (i32.const 128)))
      (drop (call $fwrite (global.get $w) (i32.const 64)))
      (call $dropw32 (global.get $w))
      (i32.const 7))
    ;; The control: a second type index naming the same future<u8>.
    (func (export "same-structure-read") (result i32)
      (call $split)
      (call $readg (global.get $r) (i32.const 128)))
    (func (export "same-structure-write") (result i32)
      (call $split)
      (drop (call $readg (global.get $r) (i32.const 128)))
      (i32.or (call $writeg (global.get $w) (i32.const 64)) (i32.shl (i32.load8_u (i32.const 128)) (i32.const 8))))
    ;; A future<u32> read through future<u8>.
    (func (export "mismatch-new32-read8") (result i32)
      (call $setends (call $new32))
      (call $fread (global.get $r) (i32.const 128)))
    ;; A future<future<u8>> read through a second index naming the same type, and through future<future<u32>>.
    (func (export "nested-same-structure-read") (result i32)
      (call $setends (call $newffa))
      (call $readffb (global.get $r) (i32.const 128)))
    (func (export "nested-mismatch-read") (result i32)
      (call $setends (call $newffa))
      (call $readffc (global.get $r) (i32.const 128))))
  (core instance $gi (instantiate $gm (with "" (instance (export "mem" (memory $m))
    (export "fnew" (func $fnew)) (export "fread" (func $fread)) (export "fwrite" (func $fwrite))
    (export "new32" (func $new32)) (export "read32" (func $read32)) (export "write32" (func $write32))
    (export "cr32" (func $cr32)) (export "cw32" (func $cw32)) (export "dropr32" (func $dropr32))
    (export "dropw32" (func $dropw32)) (export "readg" (func $readg)) (export "writeg" (func $writeg))
    (export "newffa" (func $newffa)) (export "readffb" (func $readffb)) (export "readffc" (func $readffc))))))
  (type $at (func async (result u32)))
  (func (export "mismatch-read") (type $at) (canon lift (core func $gi "mismatch-read")))
  (func (export "mismatch-write") (type $at) (canon lift (core func $gi "mismatch-write")))
  (func (export "mismatch-cancel-read") (type $at) (canon lift (core func $gi "mismatch-cancel-read")))
  (func (export "mismatch-cancel-write") (type $at) (canon lift (core func $gi "mismatch-cancel-write")))
  (func (export "mismatch-drop-readable") (type $at) (canon lift (core func $gi "mismatch-drop-readable")))
  (func (export "mismatch-drop-writable") (type $at) (canon lift (core func $gi "mismatch-drop-writable")))
  (func (export "same-structure-read") (type $at) (canon lift (core func $gi "same-structure-read")))
  (func (export "same-structure-write") (type $at) (canon lift (core func $gi "same-structure-write")))
  (func (export "mismatch-new32-read8") (type $at) (canon lift (core func $gi "mismatch-new32-read8")))
  (func (export "nested-same-structure-read") (type $at) (canon lift (core func $gi "nested-same-structure-read")))
  (func (export "nested-mismatch-read") (type $at) (canon lift (core func $gi "nested-mismatch-read"))))
