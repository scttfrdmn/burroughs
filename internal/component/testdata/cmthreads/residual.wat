;; residual (#977's residual, ADR 0101's Option A item d): another Thread of the chain runs while T1 waits.
;; `run` readies T1 and T2 and YIELDs until T1 is done. T1 async-lowers a nested child's `g` and waits on it.
;; `g` async-lowers the parent's own async `h2` and WAITs on it, then returns h2()+1. `h2` YIELDs until a
;; flag that only T2 sets, then returns 7. So T1's wait can end only if T2 runs while T1 waits, and the
;; scheduling point that runs T2 is h2's YIELD, which a scheduler still waiting on T1's handoff never hears.
;;
;; Oracle: the REFERENCE READING, `value 8` (testdata/cmthreads/reference/refread.reading, `residual`), with
;; its control (T2 doing nothing) reading HANG. wasmtime 49.0.2 hits its "BUG: delivering non-present event"
;; here (wasmtime-bug-repro.wat).
;;
;; A first shape was replaced, and is recorded so it is not re-paid for. In it `g` called the parent's sync
;; `f`, which readied T2 and suspended. wasmtime read 8, but the shape did not discriminate the bare-release
;; neuter: `f`'s own handoff satisfied a scheduler still waiting on T1, so the chain progressed without the
;; detach.
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))

  ;; h2: the parent's async export, YIELDing until mem[48] (set by T2), then returning 7
  (core func $h2ret (canon task.return (result u32)))
  (core module $h2m
    (import "" "mem" (memory 1 1))
    (import "" "ret" (func $ret (param i32)))
    (func (export "h2") (result i32) (i32.const 1)) ;; YIELD
    (func (export "cb") (param i32 i32 i32) (result i32)
      (if (i32.load (i32.const 48)) (then (call $ret (i32.const 7)) (return (i32.const 0))))
      (i32.const 1)))
  (core instance $h2i (instantiate $h2m (with "" (instance (export "mem" (memory $cm)) (export "ret" (func $h2ret))))))
  (alias core export $h2i "h2" (core func $h2f))
  (alias core export $h2i "cb" (core func $h2cbf))
  (type $ht (func async (result u32)))
  (func $H2 (type $ht) (canon lift (core func $h2f) async (callback $h2cbf)))

  (component $child
    (import "h" (func $h async (result u32)))
    (core module $cmem (memory (export "m") 1 1))
    (core instance $cmemi (instantiate $cmem))
    (alias core export $cmemi "m" (core memory $chm))
    (core func $hlow (canon lower (func $h) async (memory $chm)))
    (core func $wsnew (canon waitable-set.new))
    (core func $wsjoin (canon waitable.join))
    (core func $ret (canon task.return (result u32)))
    (core module $gm
      (import "" "mem" (memory 1 1))
      (import "" "h" (func $h (param i32) (result i32)))
      (import "" "wsnew" (func $wsnew (result i32)))
      (import "" "wsjoin" (func $wsjoin (param i32 i32)))
      (import "" "ret" (func $ret (param i32)))
      (func (export "g") (result i32) (local $p i32) (local $s i32)
        (local.set $p (call $h (i32.const 16)))
        (if (i32.eq (i32.and (local.get $p) (i32.const 15)) (i32.const 2))
          (then (call $ret (i32.add (i32.load (i32.const 16)) (i32.const 1))) (return (i32.const 0))))
        (local.set $s (call $wsnew))
        (call $wsjoin (i32.shr_u (local.get $p) (i32.const 4)) (local.get $s))
        (i32.or (i32.const 2) (i32.shl (local.get $s) (i32.const 4)))) ;; WAIT
      (func (export "cb") (param i32 i32 i32) (result i32)
        (call $ret (i32.add (i32.load (i32.const 16)) (i32.const 1)))
        (i32.const 0)))
    (core instance $gi (instantiate $gm
      (with "" (instance (export "mem" (memory $chm)) (export "h" (func $hlow)) (export "wsnew" (func $wsnew))
                         (export "wsjoin" (func $wsjoin)) (export "ret" (func $ret))))))
    (alias core export $gi "g" (core func $gf))
    (alias core export $gi "cb" (core func $gcbf))
    (type $gt (func async (result u32)))
    (func (export "g") (type $gt) (canon lift (core func $gf) async (callback $gcbf))))
  (instance $c (instantiate $child (with "h" (func $H2))))
  (alias export $c "g" (func $G))

  (core type $bodyt (func (param i32)))
  (core func $glow (canon lower (func $G) async (memory $cm)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $cm)))
  (core module $bodymod
    (import "" "mem" (memory 1 1))
    (import "" "g" (func $g (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (table (export "t") 2 2 funcref)
    ;; T1: g() into retptr 24; unless it returned at once, wait on a set holding it; mem[8] = done
    (func $t1 (param i32) (local $p i32) (local $s i32)
      (local.set $p (call $g (i32.const 24)))
      (if (i32.ne (i32.and (local.get $p) (i32.const 15)) (i32.const 2))
        (then
          (local.set $s (call $wsnew))
          (call $wsjoin (i32.shr_u (local.get $p) (i32.const 4)) (local.get $s))
          (drop (call $wswait (local.get $s) (i32.const 32)))))
      (i32.store (i32.const 8) (i32.const 1)))
    ;; T2: set the flag h2 waits for
    (func $t2 (param i32) (i32.store (i32.const 48) (i32.const 1)))
    (elem (i32.const 0) $t1 $t2))
  (core instance $bodyi (instantiate $bodymod
    (with "" (instance (export "mem" (memory $cm)) (export "g" (func $glow)) (export "wsnew" (func $wsnew))
                       (export "wsjoin" (func $wsjoin)) (export "wswait" (func $wswait))))))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $bodyi "t")))
  (core func $later (canon thread.resume-later))
  (core func $taskret (canon task.return (result u32)))
  (core module $runmod
    (import "" "mem" (memory 1 1))
    (import "" "new" (func $new (param i32 i32) (result i32)))
    (import "" "later" (func $later (param i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "callee") (result i32)
      (call $later (call $new (i32.const 0) (i32.const 0))) ;; T1
      (call $later (call $new (i32.const 1) (i32.const 0))) ;; T2
      (i32.const 1)) ;; YIELD
    (func (export "cb") (param i32 i32 i32) (result i32)
      (if (i32.load (i32.const 8))
        (then (call $ret (i32.load (i32.const 24))) (return (i32.const 0))))
      (i32.const 1)))
  (core instance $runi (instantiate $runmod
    (with "" (instance (export "mem" (memory $cm)) (export "new" (func $new))
                       (export "later" (func $later)) (export "ret" (func $taskret))))))
  (alias core export $runi "callee" (core func $calleef))
  (alias core export $runi "cb" (core func $runcbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $calleef) async (callback $runcbf)))
)
