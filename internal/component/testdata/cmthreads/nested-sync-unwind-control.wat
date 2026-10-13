;; nested-sync-unwind-control: nested-sync-unwind.wat with T's `unreachable` replaced by `nop`, so T calls
;; `release`. A MEASURED DIVERGENCE (#999): the REFERENCE READING is a trap at canon_lift's sync tail,
;; `trap_if(not candidates)`, because `h` waits on an event from another instance and the tail resumes only
;; h's own instance's Threads. wasmtime 49.0.2 agrees: "cannot block a synchronous task before returning",
;; 10 of 10. Burroughs admits the wait and returns g's result, 5.
(component
  (component $gc
    (core module $gmem (memory (export "m") 1 1))
    (core instance $gmemi (instantiate $gmem))
    (alias core export $gmemi "m" (core memory $gm))
    (core func $ret (canon task.return (result u32)))
    (core module $gcm
      (import "" "ret" (func $ret (param i32)))
      (import "" "mem" (memory 1 1))
      (func (export "g") (result i32) (i32.const 1)) ;; YIELD
      (func (export "cb") (param i32 i32 i32) (result i32)
        (if (i32.load (i32.const 0)) (then (call $ret (i32.const 5)) (return (i32.const 0))))
        (i32.const 1))
      (func (export "release") (i32.store (i32.const 0) (i32.const 1))))
    (core instance $gci (instantiate $gcm (with "" (instance (export "ret" (func $ret)) (export "mem" (memory $gm))))))
    (alias core export $gci "g" (core func $gf))
    (alias core export $gci "cb" (core func $gcbf))
    (alias core export $gci "release" (core func $relf))
    (type $gt (func async (result u32)))
    (type $relt (func))
    (func (export "g") (type $gt) (canon lift (core func $gf) async (callback $gcbf)))
    (func (export "release") (type $relt) (canon lift (core func $relf))))
  (instance $gci (instantiate $gc))
  (alias export $gci "g" (func $G))
  (alias export $gci "release" (func $REL))

  (component $child
    (import "g" (func $g async (result u32)))
    (import "release" (func $rel))
    (core module $cmem (memory (export "m") 1 1))
    (core instance $cmemi (instantiate $cmem))
    (alias core export $cmemi "m" (core memory $chm))
    (core type $bodyt (func (param i32)))
    (core func $glow (canon lower (func $g) async (memory $chm)))
    (core func $rellow (canon lower (func $rel)))
    (core func $later (canon thread.resume-later))
    (core func $wsnew (canon waitable-set.new))
    (core func $wsjoin (canon waitable.join))
    (core func $wswait (canon waitable-set.wait (memory $chm)))
    (core module $tmod
      (import "" "rel" (func $rel))
      (table (export "t") 1 1 funcref)
      (func $t (param i32)
        nop
        (call $rel))
      (elem (i32.const 0) $t))
    (core instance $ti (instantiate $tmod (with "" (instance (export "rel" (func $rellow))))))
    (core func $new (canon thread.new-indirect (core type $bodyt) (core table $ti "t")))
    (core module $hmod
      (import "" "mem" (memory 1 1))
      (import "" "g" (func $g (param i32) (result i32)))
      (import "" "new" (func $new (param i32 i32) (result i32)))
      (import "" "later" (func $later (param i32)))
      (import "" "wsnew" (func $wsnew (result i32)))
      (import "" "wsjoin" (func $wsjoin (param i32 i32)))
      (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
      ;; h: start g into retptr 32, join its subtask to a set, ready T, wait; return g's result
      (func (export "h") (result i32) (local $p i32) (local $s i32)
        (local.set $p (call $g (i32.const 32)))
        (local.set $s (call $wsnew))
        (call $wsjoin (i32.shr_u (local.get $p) (i32.const 4)) (local.get $s))
        (call $later (call $new (i32.const 0) (i32.const 0)))
        (drop (call $wswait (local.get $s) (i32.const 72)))
        (i32.load (i32.const 32))))
    (core instance $hi (instantiate $hmod
      (with "" (instance (export "mem" (memory $chm)) (export "g" (func $glow)) (export "new" (func $new))
                         (export "later" (func $later)) (export "wsnew" (func $wsnew))
                         (export "wsjoin" (func $wsjoin)) (export "wswait" (func $wswait))))))
    (alias core export $hi "h" (core func $hf))
    (type $ht (func (result u32)))
    (func (export "h") (type $ht) (canon lift (core func $hf))))
  (instance $c (instantiate $child (with "g" (func $G)) (with "release" (func $REL))))
  (alias export $c "h" (func $H))

  (core func $hlow (canon lower (func $H)))
  (core func $ret (canon task.return (result u32)))
  (core module $runmod
    (import "" "h" (func $h (result i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "run") (result i32)
      (call $ret (call $h))
      (i32.const 0)) ;; EXIT
    (func (export "cb") (param i32 i32 i32) (result i32) (i32.const 0)))
  (core instance $ri (instantiate $runmod (with "" (instance (export "h" (func $hlow)) (export "ret" (func $ret))))))
  (alias core export $ri "run" (core func $runf))
  (alias core export $ri "cb" (core func $cbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf)))
)
