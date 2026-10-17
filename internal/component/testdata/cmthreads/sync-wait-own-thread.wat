;; sync-wait-own-thread (#1005's ADMITTED case): a sync lift's waitable-set.wait that a Thread of its OWN
;; instance satisfies. The sync `run` creates a future, reads it (BLOCKED), joins the readable end to a set,
;; readies Thread T and waits. T writes 7 into the future, so the read completes and `run` returns the byte.
;;
;; Two oracles, which agree on both arms, 10 of 10 each on wasmtime 49.0.2:
;;   - this arm: wasmtime 7; the REFERENCE READING `value 7` (reference/refread.reading,
;;     `sync_wait_own_thread`). The pin's sync tail has a candidate, T, so the wait is admitted.
;;   - the control (sync-wait-own-thread-control.wat, T does not write): wasmtime "cannot block a synchronous
;;     task before returning"; the reference traps at the sync tail, `trap_if(not candidates)`, traced to
;;     canon_lift:2123.
;; wasmtime reads this intra-instance future copy, unlike trap-while-waiting's first shape, whose copy it
;; refused with "resource not present" when the two ends were driven from two created Threads.
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (type $fut (future u8))
  (core type $bodyt (func (param i32)))
  (core func $fnew (canon future.new $fut))
  (core func $fread (canon future.read $fut (memory $cm) async))
  (core func $fwrite (canon future.write $fut (memory $cm) async))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $cm)))
  (core func $later (canon thread.resume-later))
  (core module $tmod
    (import "" "mem" (memory 1 1))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))
    (table (export "t") 1 1 funcref)
    ;; T: write 7 into the future's writable end (at mem[20])
    (func $t (param i32)
      (i32.store8 (i32.const 80) (i32.const 7))
      (drop (call $fwrite (i32.load (i32.const 20)) (i32.const 80))))
    (elem (i32.const 0) $t))
  (core instance $ti (instantiate $tmod (with "" (instance (export "mem" (memory $cm)) (export "fwrite" (func $fwrite))))))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $ti "t")))
  (core module $m
    (import "" "mem" (memory 1 1))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (import "" "new" (func $new (param i32 i32) (result i32)))
    (import "" "later" (func $later (param i32)))
    (func (export "run") (result i32) (local $f i64) (local $s i32)
      (local.set $f (call $fnew))
      (i32.store (i32.const 16) (i32.wrap_i64 (local.get $f)))
      (i32.store (i32.const 20) (i32.wrap_i64 (i64.shr_u (local.get $f) (i64.const 32))))
      (if (i32.eq (call $fread (i32.load (i32.const 16)) (i32.const 64)) (i32.const -1))
        (then
          (local.set $s (call $wsnew))
          (call $wsjoin (i32.load (i32.const 16)) (local.get $s))
          (call $later (call $new (i32.const 0) (i32.const 0)))
          (drop (call $wswait (local.get $s) (i32.const 72)))))
      (i32.load8_u (i32.const 64))))
  (core instance $mi (instantiate $m
    (with "" (instance (export "mem" (memory $cm)) (export "fnew" (func $fnew)) (export "fread" (func $fread))
                       (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "wswait" (func $wswait))
                       (export "new" (func $new)) (export "later" (func $later))))))
  (alias core export $mi "run" (core func $runf))
  (type $rt (func (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf)))
)
