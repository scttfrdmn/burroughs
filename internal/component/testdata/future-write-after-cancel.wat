;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; future-write-after-cancel (#1353): a future.write after a future.cancel-write, on one future<u8> per export minted
;; by future.new. The value written is 7.
;;
;; A cancelled write returns its writable end to IDLE (def:FutureEnd.notify.future_event+'s CANCELLING_COPY arm:
;; `self.state = End.State.IDLE`), so def:copy's `trap_if(end.state != End.State.IDLE)` admits a second write.
;;
;;   - `$wcancel`: a write parks with no reader (BLOCKED), and the sync cancel returns CANCELLED (2).
;;   - write-after-cancel-write: the second write parks too (BLOCKED), as nothing is reading.
;;   - read-after-write-after-cancel-write: a read then completes the second write inline: COMPLETED (0) | (7 << 8).
;;   - poll-after-write-after-cancel-write: the writer's event after that read, polled: FUTURE_WRITE (5) | (COMPLETED
;;     << 8) | (wi 2 << 16) | (the read buffer's byte << 24).
;;   - write-to-parked-read-after-cancel-write: a read parks after the cancel, and the second write completes it
;;     inline: COMPLETED (0).
;;   - drop-writable-after-cancel-write: the cancelled writer is IDLE, not DONE, so its drop traps (def:drop).
;;   - cancel-write-parked: the control, the cancel alone: 2.
;;
;; Every export is async-typed and lifted sync. Measured on wasmtime 49.0.3 (`--invoke`) and on the pin (refread.py's
;; future_write_after_cancel rows), committed as future-write-after-cancel.reading. Assembled with `wasm-tools parse
;; future-write-after-cancel.wat -o future-write-after-cancel.wasm` (wasm-tools 1.258.0). The memory is 1 1, so
;; heap-backed.
(component
  (core module $mm (memory (export "m") 1 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (type $f (future u8))
  (core func $fnew (canon future.new $f))
  (core func $fread (canon future.read $f async (memory $m)))
  (core func $fwrite (canon future.write $f async (memory $m)))
  (core func $fcw (canon future.cancel-write $f))
  (core func $fdropw (canon future.drop-writable $f))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wspoll (canon waitable-set.poll (memory $m)))
  (core module $gm
    (import "" "mem" (memory 1 1))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))
    (import "" "fcw" (func $fcw (param i32) (result i32)))
    (import "" "fdropw" (func $fdropw (param i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wspoll" (func $wspoll (param i32 i32) (result i32)))
    (global $r (mut i32) (i32.const 0))
    (global $w (mut i32) (i32.const 0))
    ;; 64: the write buffer (7)  128: the read buffer  256: a poll's (index, payload)
    (func $split
      (local $s i64)
      (local.set $s (call $fnew))
      (global.set $r (i32.wrap_i64 (local.get $s)))
      (global.set $w (i32.wrap_i64 (i64.shr_u (local.get $s) (i64.const 32))))
      (i32.store8 (i32.const 64) (i32.const 7))
      (i32.store8 (i32.const 128) (i32.const 0)))
    (func $read (result i32) (call $fread (global.get $r) (i32.const 128)))
    (func $write (result i32) (call $fwrite (global.get $w) (i32.const 64)))
    ;; A write parks with no reader, and the sync cancel resolves it CANCELLED. Returns the cancel's result.
    (func $wcancel (result i32)
      (call $split)
      (drop (call $write))
      (call $fcw (global.get $w)))
    (func $pollone (param $h i32) (result i32) (local $ws i32) (local $code i32)
      (local.set $ws (call $wsnew))
      (call $wsjoin (local.get $h) (local.get $ws))
      (local.set $code (call $wspoll (local.get $ws) (i32.const 256)))
      (i32.or (i32.or (local.get $code) (i32.shl (i32.load (i32.const 260)) (i32.const 8)))
              (i32.or (i32.shl (i32.load (i32.const 256)) (i32.const 16))
                      (i32.shl (i32.load8_u (i32.const 128)) (i32.const 24)))))

    ;; The control: the cancel of a parked write returns CANCELLED (2).
    (func (export "cancel-write-parked") (result i32)
      (call $wcancel))
    ;; The cancelled writer is IDLE, so a second write is admitted, and parks: BLOCKED.
    (func (export "write-after-cancel-write") (result i32)
      (drop (call $wcancel))
      (call $write))
    ;; ... and a read then completes it inline (0), with the byte: 0 | (7 << 8).
    (func (export "read-after-write-after-cancel-write") (result i32)
      (drop (call $wcancel))
      (drop (call $write))
      (i32.or (call $read) (i32.shl (i32.load8_u (i32.const 128)) (i32.const 8))))
    ;; ... and the writer's event is then FUTURE_WRITE COMPLETED.
    (func (export "poll-after-write-after-cancel-write") (result i32)
      (drop (call $wcancel))
      (drop (call $write))
      (drop (call $read))
      (call $pollone (global.get $w)))
    ;; A read parks after the cancel; the second write completes it inline: COMPLETED (0).
    (func (export "write-to-parked-read-after-cancel-write") (result i32)
      (drop (call $wcancel))
      (drop (call $read))
      (call $write))
    ;; The cancelled writer is IDLE, not DONE: its drop traps.
    (func (export "drop-writable-after-cancel-write") (result i32)
      (drop (call $wcancel))
      (call $fdropw (global.get $w))
      (i32.const 7)))
  (core instance $gi (instantiate $gm (with "" (instance (export "mem" (memory $m))
    (export "fnew" (func $fnew)) (export "fread" (func $fread)) (export "fwrite" (func $fwrite))
    (export "fcw" (func $fcw)) (export "fdropw" (func $fdropw)) (export "wsnew" (func $wsnew))
    (export "wsjoin" (func $wsjoin)) (export "wspoll" (func $wspoll))))))
  (type $at (func async (result u32)))
  (func (export "cancel-write-parked") (type $at) (canon lift (core func $gi "cancel-write-parked")))
  (func (export "write-after-cancel-write") (type $at) (canon lift (core func $gi "write-after-cancel-write")))
  (func (export "read-after-write-after-cancel-write") (type $at)
    (canon lift (core func $gi "read-after-write-after-cancel-write")))
  (func (export "poll-after-write-after-cancel-write") (type $at)
    (canon lift (core func $gi "poll-after-write-after-cancel-write")))
  (func (export "write-to-parked-read-after-cancel-write") (type $at)
    (canon lift (core func $gi "write-to-parked-read-after-cancel-write")))
  (func (export "drop-writable-after-cancel-write") (type $at)
    (canon lift (core func $gi "drop-writable-after-cancel-write"))))
