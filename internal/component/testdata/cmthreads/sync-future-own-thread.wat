;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; sync-future-own-thread (#1352): a SYNC future.read or future.write that a Thread of its own instance completes, the
;; wake future-sync.wat's hang rows cannot have. Each export is a sync-typed, sync-lifted `run` that creates a
;; future<u8>, readies a Thread T and then makes its sync copy, which waits (def:copy:"end.wait_for_pending_event()").
;; The sync tail has a candidate, T, so the wait is admitted; T's async copy is the second arriver and completes both
;; ends, so the sync copy returns COMPLETED (0), packed with the byte at the read buffer: 0 | (7 << 8).
;;
;;   - sync-read-own-thread: T writes 7; run's sync read waits for it.
;;   - sync-write-own-thread: T reads; run's sync write of 7 waits for it.
;;   - sync-read-own-thread-control: T does nothing, so no candidate can complete the read and the sync tail traps.
;;   - sync-read-cancelled-by-thread: T cancels run's waiting read, which traps in T, the reader having a sync waiter
;;     (def:cancel_copy:"trap_if(end.has_sync_waiter)").
;;   - sync-read-joined-by-thread: T joins run's waiting reader to a set, which traps in T
;;     (def:canon_waitable_join:"trap_if(w.has_sync_waiter)").
;;
;; Measured on wasmtime 49.0.3 (`-W component-model-threading=y -W
;; component-model-async=y,component-model-more-async-builtins=y --invoke`) and on the pin (refread.py's
;; sync-future-own-thread rows), committed as ../future-sync.reading. Assembled with `wasm-tools parse
;; sync-future-own-thread.wat -o sync-future-own-thread.wasm` (wasm-tools 1.258.0). The memory is 1 1, so heap-backed.
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (type $fut (future u8))
  (core type $bodyt (func (param i32)))
  (core func $fnew (canon future.new $fut))
  (core func $fread (canon future.read $fut (memory $cm) async))
  (core func $fwrite (canon future.write $fut (memory $cm) async))
  (core func $sread (canon future.read $fut (memory $cm)))
  (core func $swrite (canon future.write $fut (memory $cm)))
  (core func $later (canon thread.resume-later))
  (core func $fcr (canon future.cancel-read $fut))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core module $tmod
    (import "" "mem" (memory 1 1))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))
    (import "" "fcr" (func $fcr (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (table (export "t") 5 5 funcref)
    ;; slot 0, T writes 7 into the writable end at mem[20]; slot 1, T reads the readable end at mem[16] into 128;
    ;; slot 2, T does nothing; slot 3, T cancels the read on mem[16]; slot 4, T joins mem[16] to a set.
    (func $tw (param i32)
      (i32.store8 (i32.const 64) (i32.const 7))
      (drop (call $fwrite (i32.load (i32.const 20)) (i32.const 64))))
    (func $tr (param i32)
      (drop (call $fread (i32.load (i32.const 16)) (i32.const 128))))
    (func $tn (param i32))
    (func $tc (param i32)
      (drop (call $fcr (i32.load (i32.const 16)))))
    (func $tj (param i32)
      (call $wsjoin (i32.load (i32.const 16)) (call $wsnew)))
    (elem (i32.const 0) $tw $tr $tn $tc $tj))
  (core instance $ti (instantiate $tmod (with "" (instance (export "mem" (memory $cm))
    (export "fread" (func $fread)) (export "fwrite" (func $fwrite)) (export "fcr" (func $fcr))
    (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))))))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $ti "t")))
  (core module $m
    (import "" "mem" (memory 1 1))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "sread" (func $sread (param i32 i32) (result i32)))
    (import "" "swrite" (func $swrite (param i32 i32) (result i32)))
    (import "" "new" (func $new (param i32 i32) (result i32)))
    (import "" "later" (func $later (param i32)))
    ;; 16: ri  20: wi  64: the write buffer  128: the read buffer
    (func $setup (param $slot i32) (local $f i64)
      (local.set $f (call $fnew))
      (i32.store (i32.const 16) (i32.wrap_i64 (local.get $f)))
      (i32.store (i32.const 20) (i32.wrap_i64 (i64.shr_u (local.get $f) (i64.const 32))))
      (i32.store8 (i32.const 128) (i32.const 0))
      (call $later (call $new (local.get $slot) (i32.const 0))))
    (func $packed (param $r i32) (result i32)
      (i32.or (local.get $r) (i32.shl (i32.load8_u (i32.const 128)) (i32.const 8))))
    (func (export "sync-read-own-thread") (result i32)
      (call $setup (i32.const 0))
      (call $packed (call $sread (i32.load (i32.const 16)) (i32.const 128))))
    (func (export "sync-write-own-thread") (result i32)
      (call $setup (i32.const 1))
      (i32.store8 (i32.const 64) (i32.const 7))
      (call $packed (call $swrite (i32.load (i32.const 20)) (i32.const 64))))
    (func (export "sync-read-own-thread-control") (result i32)
      (call $setup (i32.const 2))
      (call $packed (call $sread (i32.load (i32.const 16)) (i32.const 128))))
    (func (export "sync-read-cancelled-by-thread") (result i32)
      (call $setup (i32.const 3))
      (call $packed (call $sread (i32.load (i32.const 16)) (i32.const 128))))
    (func (export "sync-read-joined-by-thread") (result i32)
      (call $setup (i32.const 4))
      (call $packed (call $sread (i32.load (i32.const 16)) (i32.const 128)))))
  (core instance $mi (instantiate $m
    (with "" (instance (export "mem" (memory $cm)) (export "fnew" (func $fnew)) (export "sread" (func $sread))
                       (export "swrite" (func $swrite)) (export "new" (func $new)) (export "later" (func $later))))))
  (type $rt (func (result u32)))
  (func (export "sync-read-own-thread") (type $rt) (canon lift (core func $mi "sync-read-own-thread")))
  (func (export "sync-write-own-thread") (type $rt) (canon lift (core func $mi "sync-write-own-thread")))
  (func (export "sync-read-own-thread-control") (type $rt) (canon lift (core func $mi "sync-read-own-thread-control")))
  (func (export "sync-read-cancelled-by-thread") (type $rt) (canon lift (core func $mi "sync-read-cancelled-by-thread")))
  (func (export "sync-read-joined-by-thread") (type $rt) (canon lift (core func $mi "sync-read-joined-by-thread"))))
