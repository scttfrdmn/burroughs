;; result-store-heap-cb: result-store-heap.wat's two callers, as async CALLBACK lifts, so that both may be
;; parked at once.
;;
;; result-store-heap.wat's `run` is a SYNC lift that parks in `waitable-set.wait` on an import's subtask,
;; which both oracles trap (wasmtime 49.0.2: *"cannot block a synchronous task before returning"*; the
;; pinned reference: `canon_lift`'s sync tail). This is the admitted shape:
;;   - `run` async-lowers `op` into retptr 0 and returns WAIT on a set holding the subtask, keeping the
;;     set's index in its context slot;
;;   - its callback, on the subtask's RETURNED event, runs result-store-heap's long loop. The loop watches
;;     retptr 0 for a change while this task executes, then returns `(changed << 16) | mem[0]` through
;;     `task.return`.
;;
;; `(memory 1 1)`: heap-backed, so `-race` can see the store and the loop's loads (#979).
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (type $ops_t
    (instance
      (type $op_t (func async (param "x" u32) (result u32)))
      (export "op" (func (type $op_t)))))
  (import "test:async/ops" (instance $ops (type $ops_t)))
  (alias export $ops "op" (func $impf))
  (core func $lowered (canon lower (func $impf) async (memory $cm)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $ret (canon task.return (result u32)))
  (core func $cget (canon context.get i32 0))
  (core func $cset (canon context.set i32 0))
  (core module $runmod
    (import "" "mem" (memory 1 1))
    (import "" "lower" (func $lower (param i32 i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "ret" (func $ret (param i32)))
    (import "" "cget" (func $cget (result i32)))
    (import "" "cset" (func $cset (param i32)))
    ;; run: start `op`, and return EXIT if it RETURNED inline, else WAIT (2) on a set holding its subtask.
    (func (export "run") (result i32) (local $packed i32) (local $si i32)
      (local.set $packed (call $lower (i32.const 7) (i32.const 0)))
      (if (i32.eq (i32.and (local.get $packed) (i32.const 15)) (i32.const 2))
        (then (call $ret (i32.load (i32.const 0))) (return (i32.const 0))))
      (local.set $si (call $wsnew))
      (call $wsjoin (i32.shr_u (local.get $packed) (i32.const 4)) (local.get $si))
      (call $cset (local.get $si))
      (i32.or (i32.shl (local.get $si) (i32.const 4)) (i32.const 2)))
    ;; cb: on SUBTASK (1) with RETURNED (2), the loop, then task.return and EXIT; else WAIT again.
    (func (export "cb") (param $ev i32) (param $p1 i32) (param $p2 i32) (result i32)
      (local $seen i32) (local $changed i32) (local $i i32)
      (if (i32.and (i32.eq (local.get $ev) (i32.const 1)) (i32.eq (local.get $p2) (i32.const 2)))
        (then
          (local.set $seen (i32.load (i32.const 0)))
          (block $done
            (loop $again
              (if (i32.ne (i32.load (i32.const 0)) (local.get $seen))
                (then (local.set $changed (i32.const 1)) (br $done)))
              (local.set $i (i32.add (local.get $i) (i32.const 1)))
              (br_if $again (i32.lt_u (local.get $i) (i32.const 1000000)))))
          (call $ret (i32.or (i32.shl (local.get $changed) (i32.const 16)) (i32.load (i32.const 0))))
          (return (i32.const 0))))
      (i32.or (i32.shl (call $cget) (i32.const 4)) (i32.const 2)))
    (func (export "sibling") (result i32) (i32.const 42)))
  (core instance $runi (instantiate $runmod
    (with "" (instance
      (export "mem" (memory $cm)) (export "lower" (func $lowered))
      (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "ret" (func $ret))
      (export "cget" (func $cget)) (export "cset" (func $cset))))))
  (alias core export $runi "run" (core func $runf))
  (alias core export $runi "cb" (core func $cbf))
  (alias core export $runi "sibling" (core func $sibf))
  (type $rt (func async (result u32)))
  (type $st (func (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf)))
  (func (export "sibling") (type $st) (canon lift (core func $sibf))))
