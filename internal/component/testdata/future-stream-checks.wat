;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; future-stream-checks (#1364): four of the five future and stream built-in checks Burroughs made differently from
;; the pin, on one future<u8> or stream<u8> per export minted by future.new or stream.new. The value written is 7.
;; Item 4, the element type, is future-typed-traps.wat's.
;;
;;   - item 1, drop-readable-*: def:drop traps a reader mid-copy (def:drop:"trap_if(end.copying_or_cancelling())"):
;;     a read pending, or a read a write completed whose event is untaken. An IDLE reader, and one whose event was
;;     taken, drop.
;;   - item 2, cancel-write-*: def:cancel_copy traps the SYNC cancel of a writer joined to a set
;;     (def:cancel_copy:"trap_if(end.in_waitable_set() and not async_)"), after its state check
;;     (def:cancel_copy:"trap_if(end.state != End.State.COPYING)"): a joined writer with no write traps as having none.
;;     The async form is future-cancel-write-async.wat's.
;;   - item 3, stream-cancel-*-idle-joined: the same order on a stream end: a joined end with no copy traps at the
;;     state check, which comes first.
;;   - item 5, read-after-read-*: a future.read on a reader that is not IDLE traps (def:copy:"trap_if(end.state !=
;;     End.State.IDLE)"): a read pending, a read completed with its event untaken, and a reader DONE.
;;
;; `$pollone h` joins h to a fresh set and polls it: the event code | (payload << 8) | (index << 16), with the byte at
;; the read buffer in the top 8 bits.
;;
;; Every export is async-typed and lifted sync. Measured on wasmtime 49.0.3 (`--invoke`) and on the pin (refread.py's
;; future-stream-checks rows), committed as future-stream-checks.reading. Assembled with `wasm-tools parse
;; future-stream-checks.wat -o future-stream-checks.wasm` (wasm-tools 1.258.0). The memory is 1 1, so heap-backed.
(component
  (core module $mm (memory (export "m") 1 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (type $f (future u8))
  (type $s (stream u8))
  (core func $fnew (canon future.new $f))
  (core func $fread (canon future.read $f async (memory $m)))
  (core func $fwrite (canon future.write $f async (memory $m)))
  (core func $fcw (canon future.cancel-write $f))
  (core func $fdropr (canon future.drop-readable $f))
  (core func $snew (canon stream.new $s))
  (core func $scr (canon stream.cancel-read $s))
  (core func $scw (canon stream.cancel-write $s))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wspoll (canon waitable-set.poll (memory $m)))
  (core module $gm
    (import "" "mem" (memory 1 1))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))
    (import "" "fcw" (func $fcw (param i32) (result i32)))
    (import "" "fdropr" (func $fdropr (param i32)))
    (import "" "snew" (func $snew (result i64)))
    (import "" "scr" (func $scr (param i32) (result i32)))
    (import "" "scw" (func $scw (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wspoll" (func $wspoll (param i32 i32) (result i32)))
    (global $r (mut i32) (i32.const 0))
    (global $w (mut i32) (i32.const 0))
    ;; 64: the write buffer (7)  128: the read buffer  256: a poll's (index, payload)
    (func $setends (param $s i64)
      (global.set $r (i32.wrap_i64 (local.get $s)))
      (global.set $w (i32.wrap_i64 (i64.shr_u (local.get $s) (i64.const 32)))))
    (func $split
      (call $setends (call $fnew))
      (i32.store8 (i32.const 64) (i32.const 7))
      (i32.store8 (i32.const 128) (i32.const 0)))
    (func $ssplit (call $setends (call $snew)))
    (func $read (result i32) (call $fread (global.get $r) (i32.const 128)))
    (func $write (result i32) (call $fwrite (global.get $w) (i32.const 64)))
    ;; A read parks; a write completes it inline, so the reader's event is pending.
    (func $rfirst
      (call $split)
      (drop (call $read))
      (drop (call $write)))
    (func $joinnew (param $h i32) (result i32) (local $ws i32)
      (local.set $ws (call $wsnew))
      (call $wsjoin (local.get $h) (local.get $ws))
      (local.get $ws))
    (func $pollone (param $h i32) (result i32) (local $code i32)
      (local.set $code (call $wspoll (call $joinnew (local.get $h)) (i32.const 256)))
      (i32.or (i32.or (local.get $code) (i32.shl (i32.load (i32.const 260)) (i32.const 8)))
              (i32.or (i32.shl (i32.load (i32.const 256)) (i32.const 16))
                      (i32.shl (i32.load8_u (i32.const 128)) (i32.const 24)))))

    ;; --- item 1: future.drop-readable ---
    ;; A read pending: the reader is COPYING, and its drop traps.
    (func (export "drop-readable-read-pending") (result i32)
      (call $split)
      (drop (call $read))
      (call $fdropr (global.get $r))
      (i32.const 7))
    ;; A read a write completed, its event untaken: still COPYING, and the drop traps.
    (func (export "drop-readable-event-untaken") (result i32)
      (call $rfirst)
      (call $fdropr (global.get $r))
      (i32.const 7))
    ;; The control: the poll took the read's COMPLETED, so the reader is DONE and drops. Returns the poll.
    (func (export "drop-readable-after-event-taken") (result i32) (local $p i32)
      (call $rfirst)
      (local.set $p (call $pollone (global.get $r)))
      (call $fdropr (global.get $r))
      (local.get $p))
    ;; The control: an IDLE reader drops.
    (func (export "drop-readable-idle") (result i32)
      (call $split)
      (call $fdropr (global.get $r))
      (i32.const 7))

    ;; --- item 2: the sync future.cancel-write ---
    ;; A write parked by a writer joined to a set: the sync cancel traps.
    (func (export "cancel-write-sync-joined") (result i32)
      (call $split)
      (drop (call $joinnew (global.get $w)))
      (drop (call $write))
      (call $fcw (global.get $w)))
    ;; A joined writer with no write: the state check comes first, so it traps as having no write.
    (func (export "cancel-write-idle-joined") (result i32)
      (call $split)
      (drop (call $joinnew (global.get $w)))
      (call $fcw (global.get $w)))

    ;; --- item 3: the sync stream cancels' order ---
    ;; A joined readable stream end with no read: the state check comes first.
    (func (export "stream-cancel-read-idle-joined") (result i32)
      (call $ssplit)
      (drop (call $joinnew (global.get $r)))
      (call $scr (global.get $r)))
    ;; A joined writable stream end with no write: the same.
    (func (export "stream-cancel-write-idle-joined") (result i32)
      (call $ssplit)
      (drop (call $joinnew (global.get $w)))
      (call $scw (global.get $w)))

    ;; --- item 5: future.read on a reader that is not IDLE ---
    ;; A read pending: a second read traps.
    (func (export "read-after-read-pending") (result i32)
      (call $split)
      (drop (call $read))
      (call $read))
    ;; A read a write completed, its event untaken: a second read traps.
    (func (export "read-after-read-event-untaken") (result i32)
      (call $rfirst)
      (call $read))
    ;; The poll took the read's COMPLETED, so the reader is DONE: a second read traps.
    (func (export "read-after-read-done") (result i32)
      (call $rfirst)
      (drop (call $pollone (global.get $r)))
      (call $read)))
  (core instance $gi (instantiate $gm (with "" (instance (export "mem" (memory $m))
    (export "fnew" (func $fnew)) (export "fread" (func $fread)) (export "fwrite" (func $fwrite))
    (export "fcw" (func $fcw)) (export "fdropr" (func $fdropr)) (export "snew" (func $snew))
    (export "scr" (func $scr)) (export "scw" (func $scw)) (export "wsnew" (func $wsnew))
    (export "wsjoin" (func $wsjoin)) (export "wspoll" (func $wspoll))))))
  (type $at (func async (result u32)))
  (func (export "drop-readable-read-pending") (type $at) (canon lift (core func $gi "drop-readable-read-pending")))
  (func (export "drop-readable-event-untaken") (type $at) (canon lift (core func $gi "drop-readable-event-untaken")))
  (func (export "drop-readable-after-event-taken") (type $at)
    (canon lift (core func $gi "drop-readable-after-event-taken")))
  (func (export "drop-readable-idle") (type $at) (canon lift (core func $gi "drop-readable-idle")))
  (func (export "cancel-write-sync-joined") (type $at) (canon lift (core func $gi "cancel-write-sync-joined")))
  (func (export "cancel-write-idle-joined") (type $at) (canon lift (core func $gi "cancel-write-idle-joined")))
  (func (export "stream-cancel-read-idle-joined") (type $at)
    (canon lift (core func $gi "stream-cancel-read-idle-joined")))
  (func (export "stream-cancel-write-idle-joined") (type $at)
    (canon lift (core func $gi "stream-cancel-write-idle-joined")))
  (func (export "read-after-read-pending") (type $at) (canon lift (core func $gi "read-after-read-pending")))
  (func (export "read-after-read-event-untaken") (type $at) (canon lift (core func $gi "read-after-read-event-untaken")))
  (func (export "read-after-read-done") (type $at) (canon lift (core func $gi "read-after-read-done"))))
