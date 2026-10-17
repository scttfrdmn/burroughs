;; fanout: one caller, four async callees doing independent work (the chair's ruling of 2026-10-17 on #1025:
;; "measure the parallelism cost before the build"). Driven by BenchmarkFanOut; the shape is in README.md.
;;
;; The child's run(mode, iters) counts `iters` down, which is the work, and returns iters:
;;   mode 0: the work is in its FIRST segment, then task.return and EXIT;
;;   mode 1: it YIELDs first, and the work is in its callback, past its first suspension.
;; The parent's go(k, mode, iters) async-lowers run on the first k of four child INSTANCES (the run slot is
;; per instance, so separate instances are what can run in parallel), joins every subtask that did not
;; return inline, and WAITs until all k have resolved. It returns the sum of the results.
(component
  (component $child
    (core func $ret (canon task.return (result u32)))
    (core module $cm
      (import "" "ret" (func $ret (param i32)))
      (global $iters (mut i32) (i32.const 0))
      (func $work (param $n i32) (result i32) (local $i i32)
        (local.set $i (local.get $n))
        (block $done
          (loop $l
            (br_if $done (i32.eqz (local.get $i)))
            (local.set $i (i32.sub (local.get $i) (i32.const 1)))
            (br $l)))
        (local.get $n))
      (func (export "run") (param $mode i32) (param $n i32) (result i32)
        (if (i32.eqz (local.get $mode))
          (then (call $ret (call $work (local.get $n))) (return (i32.const 0)))) ;; EXIT, resolved inline
        (global.set $iters (local.get $n))
        (i32.const 1)) ;; YIELD
      (func (export "cb") (param i32 i32 i32) (result i32)
        (call $ret (call $work (global.get $iters)))
        (i32.const 0)))
    (core instance $ci (instantiate $cm (with "" (instance (export "ret" (func $ret))))))
    (alias core export $ci "run" (core func $runf))
    (alias core export $ci "cb" (core func $cbf))
    (type $rt (func async (param "mode" u32) (param "iters" u32) (result u32)))
    (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf))))
  (instance $c0 (instantiate $child))
  (instance $c1 (instantiate $child))
  (instance $c2 (instantiate $child))
  (instance $c3 (instantiate $child))
  (alias export $c0 "run" (func $R0))
  (alias export $c1 "run" (func $R1))
  (alias export $c2 "run" (func $R2))
  (alias export $c3 "run" (func $R3))
  (core module $pmem (memory (export "m") 1 1))
  (core instance $pmemi (instantiate $pmem))
  (alias core export $pmemi "m" (core memory $pm))
  (core func $low0 (canon lower (func $R0) async (memory $pm)))
  (core func $low1 (canon lower (func $R1) async (memory $pm)))
  (core func $low2 (canon lower (func $R2) async (memory $pm)))
  (core func $low3 (canon lower (func $R3) async (memory $pm)))
  (core func $wsnew (canon waitable-set.new))
  (core func $join (canon waitable.join))
  (core func $sdrop (canon subtask.drop))
  (core func $ret (canon task.return (result u32)))
  (core module $gm
    (import "" "m" (memory 1 1))
    (import "" "r0" (func $r0 (param i32 i32 i32) (result i32)))
    (import "" "r1" (func $r1 (param i32 i32 i32) (result i32)))
    (import "" "r2" (func $r2 (param i32 i32 i32) (result i32)))
    (import "" "r3" (func $r3 (param i32 i32 i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "join" (func $join (param i32 i32)))
    (import "" "sdrop" (func $sdrop (param i32)))
    (import "" "ret" (func $ret (param i32)))
    (global $ws (mut i32) (i32.const 0))
    (global $left (mut i32) (i32.const 0))
    ;; Each child's result lands at retptr 16*i; the sum is taken when every child has resolved.
    (func $sum (param $k i32) (result i32) (local $i i32) (local $s i32)
      (block $done
        (loop $l
          (br_if $done (i32.ge_u (local.get $i) (local.get $k)))
          (local.set $s (i32.add (local.get $s) (i32.load (i32.mul (local.get $i) (i32.const 16)))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $l)))
      (local.get $s))
    (global $k (mut i32) (i32.const 0))
    (func $start (param $i i32) (param $mode i32) (param $n i32) (local $p i32)
      (local.set $p
        (if (result i32) (i32.eq (local.get $i) (i32.const 0))
          (then (call $r0 (local.get $mode) (local.get $n) (i32.const 0)))
          (else (if (result i32) (i32.eq (local.get $i) (i32.const 1))
            (then (call $r1 (local.get $mode) (local.get $n) (i32.const 16)))
            (else (if (result i32) (i32.eq (local.get $i) (i32.const 2))
              (then (call $r2 (local.get $mode) (local.get $n) (i32.const 32)))
              (else (call $r3 (local.get $mode) (local.get $n) (i32.const 48)))))))))
      (if (i32.ne (i32.and (local.get $p) (i32.const 15)) (i32.const 2)) ;; not RETURNED: wait for it
        (then
          (call $join (i32.shr_u (local.get $p) (i32.const 4)) (global.get $ws))
          (global.set $left (i32.add (global.get $left) (i32.const 1))))))
    (func (export "go") (param $k i32) (param $mode i32) (param $n i32) (result i32) (local $i i32)
      (global.set $k (local.get $k))
      (global.set $ws (call $wsnew))
      (global.set $left (i32.const 0))
      (block $done
        (loop $l
          (br_if $done (i32.ge_u (local.get $i) (local.get $k)))
          (call $start (local.get $i) (local.get $mode) (local.get $n))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $l)))
      (if (i32.eqz (global.get $left))
        (then (call $ret (call $sum (local.get $k))) (return (i32.const 0))))
      (i32.or (i32.shl (global.get $ws) (i32.const 4)) (i32.const 2))) ;; WAIT
    ;; cb(event, index, state): a SUBTASK event (1) carrying RETURNED (2) is one child done.
    (func (export "cb") (param $ev i32) (param $idx i32) (param $st i32) (result i32)
      (if (i32.and (i32.eq (local.get $ev) (i32.const 1)) (i32.eq (local.get $st) (i32.const 2)))
        (then
          (call $sdrop (local.get $idx))
          (global.set $left (i32.sub (global.get $left) (i32.const 1)))))
      (if (i32.eqz (global.get $left))
        (then (call $ret (call $sum (global.get $k))) (return (i32.const 0))))
      (i32.or (i32.shl (global.get $ws) (i32.const 4)) (i32.const 2))))
  (core instance $gi (instantiate $gm (with "" (instance
    (export "m" (memory $pm))
    (export "r0" (func $low0)) (export "r1" (func $low1)) (export "r2" (func $low2)) (export "r3" (func $low3))
    (export "wsnew" (func $wsnew)) (export "join" (func $join)) (export "sdrop" (func $sdrop))
    (export "ret" (func $ret))))))
  (alias core export $gi "go" (core func $gof))
  (alias core export $gi "cb" (core func $gcb))
  (type $gt (func async (param "k" u32) (param "mode" u32) (param "iters" u32) (result u32)))
  (func (export "go") (type $gt) (canon lift (core func $gof) async (callback $gcb))))
