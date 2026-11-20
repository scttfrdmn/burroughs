;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; exit-from-thread-wait (#1265's control, behind gate:cm-threads): exit-from-thread.wat with the run WAITing
;; rather than YIELDing. After calling the child's `f`, the run creates a future nobody writes, reads it, joins
;; the readable end to a set, and WAITs on the set, which only an end to the run can leave. T calls
;; `exit-with-code(9)` after `f` has returned.
;;
;; wasmtime 49.0.3: exit 9, nothing on stdout or stderr, 10 of 10 (exit-from-thread.reading). Burroughs at
;; 2c4371d exited 9 too: the WAIT arm's `awaitEvent` already selects on the store lock (ADR 0105). So the
;; defect is the YIELD arm's, and not an exit that ends only its Thread.
;;
;; Assembled with `wasm-tools parse exit-from-thread-wait.wat -o exit-from-thread-wait.wasm` (wasm-tools
;; 1.258.0), by build.sh.
(component
  (import "wasi:cli/exit@0.3.0" (instance $exit (export "exit-with-code" (func (param "status-code" u8)))))
  (alias export $exit "exit-with-code" (func $exitf))

  ;; The child: exit-from-thread.wat's, unchanged.
  (component $child
    (import "exit" (func $exit (param "status-code" u8)))
    (core func $exitc (canon lower (func $exit)))
    (core type $bodyt (func (param i32)))
    (core module $tmod
      (import "" "exit" (func $exit (param i32)))
      (table (export "t") 1 1 funcref)
      (func $t (param i32)
        (call $exit (i32.const 9))
        unreachable)
      (elem (i32.const 0) $t))
    (core instance $ti (instantiate $tmod (with "" (instance (export "exit" (func $exitc))))))
    (core func $new (canon thread.new-indirect (core type $bodyt) (core table $ti "t")))
    (core func $later (canon thread.resume-later))
    (core func $ret (canon task.return))
    (core module $cm
      (import "" "new" (func $new (param i32 i32) (result i32)))
      (import "" "later" (func $later (param i32)))
      (import "" "ret" (func $ret))
      (func (export "f") (result i32)
        (call $later (call $new (i32.const 0) (i32.const 0)))
        (call $ret)
        (i32.const 0)) ;; EXIT, with T ready
      (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
    (core instance $ci (instantiate $cm
      (with "" (instance (export "new" (func $new)) (export "later" (func $later)) (export "ret" (func $ret))))))
    (alias core export $ci "f" (core func $ff))
    (alias core export $ci "cb" (core func $cbf))
    (type $ft (func async))
    (func (export "f") (type $ft) (canon lift (core func $ff) async (callback $cbf))))
  (instance $c (instantiate $child (with "exit" (func $exitf))))
  (alias export $c "f" (func $F))

  ;; The run: call `f`, then WAIT on a future nobody writes.
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (type $fut (future u8))
  (core func $flow (canon lower (func $F) async (memory $cm)))
  (core func $fnew (canon future.new $fut))
  (core func $fread (canon future.read $fut (memory $cm) async))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core module $runmod
    (import "" "f" (func $f (result i32)))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (func (export "run") (result i32) (local $fut i64) (local $s i32)
      (drop (call $f))
      ;; The writable end (the high word) is held and never written.
      (local.set $fut (call $fnew))
      (drop (call $fread (i32.wrap_i64 (local.get $fut)) (i32.const 64)))
      (local.set $s (call $wsnew))
      (call $wsjoin (i32.wrap_i64 (local.get $fut)) (local.get $s))
      (i32.or (i32.const 2) (i32.shl (local.get $s) (i32.const 4)))) ;; WAIT on the set
    (func (export "cb") (param i32 i32 i32) (result i32)
      unreachable)) ;; the wait never completes: only the child's Thread can end the run
  (core instance $runi (instantiate $runmod
    (with "" (instance (export "f" (func $flow)) (export "fnew" (func $fnew)) (export "fread" (func $fread))
                       (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))))))
  (alias core export $runi "run" (core func $runf))
  (alias core export $runi "cb" (core func $runcbf))
  (type $rt (func async (result (result))))
  (func $run (type $rt) (canon lift (core func $runf) async (callback $runcbf)))
  (instance $r (export "run" (func $run)))
  (export "wasi:cli/run@0.3.0" (instance $r))
)
