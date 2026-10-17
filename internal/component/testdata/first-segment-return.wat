;; first-segment-return: #1020's class, mechanism (c), on the default path with no host and no Threads.
;; The child's async callback lift calls task.return and EXITs inside its FIRST core call, so it resolves
;; before its first suspension. The reference runs that segment inside the parent's lower (canon_lift's
;; thread.resume), so the lower returns RETURNED (2). go returns the lower's state.
;; Readings: first-segment-return.reading.
(component
  (component $child
    (core func $ret (canon task.return (result u32)))
    (core module $cm
      (import "" "ret" (func $ret (param i32)))
      (func (export "run") (param i32) (result i32) (call $ret (i32.const 7)) (i32.const 0)) ;; EXIT, resolved
      (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
    (core instance $ci (instantiate $cm (with "" (instance (export "ret" (func $ret))))))
    (alias core export $ci "run" (core func $runf))
    (alias core export $ci "cb" (core func $cbf))
    (type $rt (func async (param "id" u32) (result u32)))
    (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf))))
  (instance $c (instantiate $child))
  (alias export $c "run" (func $RUN))
  (core module $pmem (memory (export "m") 1 1))
  (core instance $pmemi (instantiate $pmem))
  (alias core export $pmemi "m" (core memory $pm))
  (core func $runlow (canon lower (func $RUN) async (memory $pm)))
  (core func $ret (canon task.return (result u32)))
  (core module $gm
    (import "" "run" (func $run (param i32 i32) (result i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "go") (result i32)
      (call $ret (i32.and (call $run (i32.const 1) (i32.const 0)) (i32.const 15)))
      (i32.const 0))
    (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
  (core instance $gi (instantiate $gm (with "" (instance (export "run" (func $runlow)) (export "ret" (func $ret))))))
  (alias core export $gi "go" (core func $gof))
  (alias core export $gi "cb" (core func $gcb))
  (type $gt (func async (result u32))) (func (export "go") (type $gt) (canon lift (core func $gof) async (callback $gcb))))
