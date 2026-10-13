;; yield-admits-entry (ADR 0101's Option A, item e): an async task may ENTER while another YIELDs. The
;; parent's async `run` readies Thread T and YIELDs until T is done. T async-lowers a nested child's `g`
;; and waits on it. `g` async-lowers the parent's OWN async `h`, which `run`'s component passed it, waits
;; for it, and returns h()+1. `h` returns 7 at once.
;;
;; `h`'s entry needs the parent's `entrySem` (the reference's `exclusive_thread`). The reference has cleared
;; `exclusive_thread` by the time `run`'s YIELD arm runs (canon_lift: `inst.exclusive_thread = None` on every
;; callback return), so `h` enters. Burroughs's old YIELD arm re-took `entrySem` while T ran, so `h` could
;; not enter, `g` could not finish, and T waited forever. Expected: 8.
(component
  (core module $hm
    (import "" "ret" (func $ret (param i32)))
    (func (export "h") (result i32) (call $ret (i32.const 7)) (i32.const 0)) ;; EXIT
    (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
  (core func $hret (canon task.return (result u32)))
  (core instance $hi (instantiate $hm (with "" (instance (export "ret" (func $hret))))))
  (alias core export $hi "h" (core func $hf))
  (alias core export $hi "cb" (core func $hcbf))
  (type $ht (func async (result u32)))
  (func $H (type $ht) (canon lift (core func $hf) async (callback $hcbf)))

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
      ;; g: h() into retptr 16; if it returned at once, return h()+1, else WAIT on a set holding it
      (func (export "g") (result i32) (local $p i32) (local $s i32)
        (local.set $p (call $h (i32.const 16)))
        (if (i32.eq (i32.and (local.get $p) (i32.const 15)) (i32.const 2))
          (then (call $ret (i32.add (i32.load (i32.const 16)) (i32.const 1))) (return (i32.const 0))))
        (local.set $s (call $wsnew))
        (call $wsjoin (i32.shr_u (local.get $p) (i32.const 4)) (local.get $s))
        (i32.or (i32.const 2) (i32.shl (local.get $s) (i32.const 4))))
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
  (instance $c (instantiate $child (with "h" (func $H))))
  (alias export $c "g" (func $G))

  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
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
    (table (export "t") 1 1 funcref)
    ;; T: g() into retptr 24; unless it returned at once, wait on a set holding it; mem[8] = done
    (func $t (param i32) (local $p i32) (local $s i32)
      (local.set $p (call $g (i32.const 24)))
      (if (i32.ne (i32.and (local.get $p) (i32.const 15)) (i32.const 2))
        (then
          (local.set $s (call $wsnew))
          (call $wsjoin (i32.shr_u (local.get $p) (i32.const 4)) (local.get $s))
          (drop (call $wswait (local.get $s) (i32.const 32)))))
      (i32.store (i32.const 8) (i32.const 1)))
    (elem (i32.const 0) $t))
  (core instance $bodyi (instantiate $bodymod
    (with "" (instance (export "mem" (memory $cm)) (export "g" (func $glow)) (export "wsnew" (func $wsnew))
                       (export "wsjoin" (func $wsjoin)) (export "wswait" (func $wswait))))))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $bodyi "t")))
  (core func $later (canon thread.resume-later))
  (core func $ret (canon task.return (result u32)))
  (core module $runmod
    (import "" "mem" (memory 1 1))
    (import "" "new" (func $new (param i32 i32) (result i32)))
    (import "" "later" (func $later (param i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "callee") (result i32)
      (call $later (call $new (i32.const 0) (i32.const 0)))
      (i32.const 1)) ;; YIELD
    (func (export "cb") (param i32 i32 i32) (result i32)
      (if (i32.load (i32.const 8))
        (then (call $ret (i32.load (i32.const 24))) (return (i32.const 0))))
      (i32.const 1)))
  (core instance $runi (instantiate $runmod
    (with "" (instance (export "mem" (memory $cm)) (export "new" (func $new))
                       (export "later" (func $later)) (export "ret" (func $ret))))))
  (alias core export $runi "callee" (core func $calleef))
  (alias core export $runi "cb" (core func $runcbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $calleef) async (callback $runcbf)))
)
