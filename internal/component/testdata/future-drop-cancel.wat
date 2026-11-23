;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; future-drop-cancel (#1362): future.drop-writable (0x1b) and future.cancel-read (0x18), the two future built-ins
;; Burroughs did not bind, against one future<u8> per export minted by future.new. The value written is 7.
;;
;; def:drop traps on an end that is copying (def:drop:"trap_if(end.copying_or_cancelling())") and, for a writable
;; future end only, on one that is not DONE (def:drop:"trap_if(isinstance(end, WritableFutureEnd) and end.state !=").
;; A writable future end is DONE once its one value has been taken (a COMPLETED of progress 1) or once its reader has
;; gone (DROPPED), and only when that event has been taken (def:FutureEnd.notify.future_event+).
;; def:cancel_copy traps unless the end is COPYING, and in its sync form on an end joined to a set.
;;
;;   - `$rfirst`: a read parks (BLOCKED); a write completes it inline (COMPLETED), so the writer is DONE and the
;;     reader's event is pending. `$wfirst` is its mirror: a write parks, a read completes it inline, and the
;;     writer's event is pending, so the writer is still COPYING.
;;   - `$pollone h` joins h to a fresh set and polls it: the event code | (payload << 8) | (index << 16), with the
;;     byte at the read buffer in the top 8 bits.
;;   - drop-writable-*: the writer's drop in each state the pin distinguishes; a row that does not trap returns 7 or
;;     a poll after the drop.
;;   - cancel-read-*: the reader's sync cancel in each state; a cancel that returns gives its result. The async form
;;     is future-cancel-read-async.wat's.
;;   - drop-writable-after-reader-dropped: the writer's drop after its reader's.
;;
;; Every export is async-typed and lifted sync. Measured on wasmtime 49.0.3 (`--invoke`) and on the pin (refread.py's
;; future_drop_cancel rows), committed as future-drop-cancel.reading. Assembled with `wasm-tools parse
;; future-drop-cancel.wat -o future-drop-cancel.wasm` (wasm-tools 1.258.0). The memory is 1 1, so heap-backed.
(component
  (core module $mm (memory (export "m") 1 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (type $f (future u8))
  (core func $fnew (canon future.new $f))
  (core func $fread (canon future.read $f async (memory $m)))
  (core func $fwrite (canon future.write $f async (memory $m)))
  (core func $fcr (canon future.cancel-read $f))
  (core func $fdropr (canon future.drop-readable $f))
  (core func $fdropw (canon future.drop-writable $f))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wspoll (canon waitable-set.poll (memory $m)))
  (core module $gm
    (import "" "mem" (memory 1 1))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))
    (import "" "fcr" (func $fcr (param i32) (result i32)))
    (import "" "fdropr" (func $fdropr (param i32)))
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
    ;; A read parks; a write completes it inline. Returns the write's result.
    (func $rfirst (result i32)
      (call $split)
      (drop (call $read))
      (call $write))
    ;; A write parks; a read completes it inline. Returns the read's result.
    (func $wfirst (result i32)
      (call $split)
      (drop (call $write))
      (call $read))
    (func $joinnew (param $h i32) (result i32) (local $ws i32)
      (local.set $ws (call $wsnew))
      (call $wsjoin (local.get $h) (local.get $ws))
      (local.get $ws))
    (func $pollset (param $ws i32) (result i32) (local $code i32)
      (local.set $code (call $wspoll (local.get $ws) (i32.const 256)))
      (i32.or (i32.or (local.get $code) (i32.shl (i32.load (i32.const 260)) (i32.const 8)))
              (i32.or (i32.shl (i32.load (i32.const 256)) (i32.const 16))
                      (i32.shl (i32.load8_u (i32.const 128)) (i32.const 24)))))
    (func $pollone (param $h i32) (result i32) (call $pollset (call $joinnew (local.get $h))))

    ;; --- future.drop-writable ---
    ;; The write completed a parked read, so the writer is DONE; its drop succeeds, and the reader's event, polled
    ;; after the drop, is FUTURE_READ COMPLETED with the byte.
    (func (export "drop-writable-after-write") (result i32)
      (drop (call $rfirst))
      (call $fdropw (global.get $w))
      (call $pollone (global.get $r)))
    ;; The read completed a parked write; the poll takes the writer's FUTURE_WRITE COMPLETED, so the writer is DONE
    ;; and its drop succeeds. Returns that poll.
    (func (export "drop-writable-after-write-event-taken") (result i32) (local $p i32)
      (drop (call $wfirst))
      (local.set $p (call $pollone (global.get $w)))
      (call $fdropw (global.get $w))
      (local.get $p))
    ;; The read completed a parked write, whose event is not taken: the writer is still COPYING, and the drop traps.
    (func (export "drop-writable-event-untaken") (result i32)
      (drop (call $wfirst))
      (call $fdropw (global.get $w))
      (i32.const 7))
    ;; A write parked with no reader: the writer is COPYING, and the drop traps.
    (func (export "drop-writable-write-pending") (result i32)
      (call $split)
      (drop (call $write))
      (call $fdropw (global.get $w))
      (i32.const 7))
    ;; No write ever: the writer is IDLE, not DONE, and the drop traps.
    (func (export "drop-writable-idle") (result i32)
      (call $split)
      (call $fdropw (global.get $w))
      (i32.const 7))
    ;; No write ever, a read parked: the writer is IDLE, not DONE, and the drop traps, so a parked reader is never
    ;; handed a DROPPED by its writer's drop.
    (func (export "drop-writable-idle-reader-parked") (result i32)
      (call $split)
      (drop (call $read))
      (call $fdropw (global.get $w))
      (i32.const 7))

    ;; --- future.cancel-read ---
    ;; A read parked with no writer: the cancel resolves it CANCELLED (2), inline.
    (func (export "cancel-read-parked") (result i32)
      (call $split)
      (drop (call $read))
      (call $fcr (global.get $r)))
    ;; The cancel took the read's event, so a poll of the reader afterwards sees none: 0.
    (func (export "poll-after-cancel-read") (result i32)
      (call $split)
      (drop (call $read))
      (drop (call $fcr (global.get $r)))
      (call $pollone (global.get $r)))
    ;; A cancelled reader is IDLE: a write afterwards parks (BLOCKED), as nothing is reading.
    (func (export "write-after-cancel-read") (result i32)
      (call $split)
      (drop (call $read))
      (drop (call $fcr (global.get $r)))
      (call $write))
    ;; ... and a second read then completes it inline (0), with the byte: 0 | (7 << 8).
    (func (export "read-after-cancel-read") (result i32)
      (call $split)
      (drop (call $read))
      (drop (call $fcr (global.get $r)))
      (drop (call $write))
      (i32.or (call $read) (i32.shl (i32.load8_u (i32.const 128)) (i32.const 8))))
    ;; The write completed the parked read, whose event is not taken: the reader is still COPYING, and the cancel
    ;; takes the COMPLETED (0), with the byte: 0 | (7 << 8).
    (func (export "cancel-read-after-copy") (result i32)
      (drop (call $rfirst))
      (i32.or (call $fcr (global.get $r)) (i32.shl (i32.load8_u (i32.const 128)) (i32.const 8))))
    ;; ... and leaves no second event: 0.
    (func (export "poll-after-cancel-read-after-copy") (result i32)
      (drop (call $rfirst))
      (drop (call $fcr (global.get $r)))
      (call $pollone (global.get $r)))
    ;; No read ever: the reader is IDLE, not COPYING, and the cancel traps.
    (func (export "cancel-read-idle") (result i32)
      (call $split)
      (call $fcr (global.get $r)))
    ;; The poll took the read's COMPLETED, so the reader is DONE, not COPYING, and the cancel traps.
    (func (export "cancel-read-after-event-taken") (result i32)
      (drop (call $rfirst))
      (drop (call $pollone (global.get $r)))
      (call $fcr (global.get $r)))
    ;; The reader is joined to a set: the sync cancel traps (def:cancel_copy's in_waitable_set check).
    (func (export "cancel-read-sync-joined") (result i32)
      (call $split)
      (drop (call $joinnew (global.get $r)))
      (drop (call $read))
      (call $fcr (global.get $r)))
    ;; Its async form, which is unguarded, is future-cancel-read-async.wat's: wasmtime 49.0.3 loads an async
    ;; future.cancel-read only with component-model-more-async-builtins, so it is read under its own invocation.

    ;; --- a reader dropped first ---
    ;; The reader's drop notifies the IDLE writer, so its write completes DROPPED (1) inline, and its drop then
    ;; succeeds. Returns the write's result.
    (func (export "drop-writable-after-reader-dropped") (result i32) (local $wr i32)
      (call $split)
      (call $fdropr (global.get $r))
      (local.set $wr (call $write))
      (call $fdropw (global.get $w))
      (local.get $wr)))
  (core instance $gi (instantiate $gm (with "" (instance (export "mem" (memory $m))
    (export "fnew" (func $fnew)) (export "fread" (func $fread)) (export "fwrite" (func $fwrite))
    (export "fcr" (func $fcr)) (export "fdropr" (func $fdropr))
    (export "fdropw" (func $fdropw)) (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
    (export "wspoll" (func $wspoll))))))
  (type $at (func async (result u32)))
  (func (export "drop-writable-after-write") (type $at) (canon lift (core func $gi "drop-writable-after-write")))
  (func (export "drop-writable-after-write-event-taken") (type $at)
    (canon lift (core func $gi "drop-writable-after-write-event-taken")))
  (func (export "drop-writable-event-untaken") (type $at) (canon lift (core func $gi "drop-writable-event-untaken")))
  (func (export "drop-writable-write-pending") (type $at) (canon lift (core func $gi "drop-writable-write-pending")))
  (func (export "drop-writable-idle") (type $at) (canon lift (core func $gi "drop-writable-idle")))
  (func (export "drop-writable-idle-reader-parked") (type $at)
    (canon lift (core func $gi "drop-writable-idle-reader-parked")))
  (func (export "cancel-read-parked") (type $at) (canon lift (core func $gi "cancel-read-parked")))
  (func (export "poll-after-cancel-read") (type $at) (canon lift (core func $gi "poll-after-cancel-read")))
  (func (export "write-after-cancel-read") (type $at) (canon lift (core func $gi "write-after-cancel-read")))
  (func (export "read-after-cancel-read") (type $at) (canon lift (core func $gi "read-after-cancel-read")))
  (func (export "cancel-read-after-copy") (type $at) (canon lift (core func $gi "cancel-read-after-copy")))
  (func (export "poll-after-cancel-read-after-copy") (type $at)
    (canon lift (core func $gi "poll-after-cancel-read-after-copy")))
  (func (export "cancel-read-idle") (type $at) (canon lift (core func $gi "cancel-read-idle")))
  (func (export "cancel-read-after-event-taken") (type $at) (canon lift (core func $gi "cancel-read-after-event-taken")))
  (func (export "cancel-read-sync-joined") (type $at) (canon lift (core func $gi "cancel-read-sync-joined")))
  (func (export "drop-writable-after-reader-dropped") (type $at)
    (canon lift (core func $gi "drop-writable-after-reader-dropped"))))
