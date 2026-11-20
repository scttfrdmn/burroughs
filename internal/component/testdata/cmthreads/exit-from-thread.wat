;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; exit-from-thread (#1265, behind gate:cm-threads): a created Thread calls `wasi:cli/exit` while the run is
;; still going, and the exit must end the run. The run's `wasi:cli/run@0.3.0` export is an async callback lift
;; that async-lowers a composed child's `f` and then YIELDs forever, calling no built-in, so only the Thread can
;; end it. `f` creates Thread T, readies it, resolves with task.return and EXITs, so T runs after its own task
;; has returned. T calls `exit-with-code(9)`.
;;
;; wasmtime 49.0.3: exit 9, nothing on stdout or stderr, 10 of 10 (exit-from-thread.reading). Burroughs at
;; 2c4371d hung: T's exit reached the store lock as an orphan trap, and the run's YIELD arm had no lock case, so
;; its callback re-entered forever.
;;
;; The run is an ASYNC-typed `run`, so the export is 0.3's. wasmtime refuses an async lift of a sync-typed
;; function ("the `async` canonical option requires an async function type"), so 0.2's `run` cannot have this
;; shape. `exit-with-code` is 0.3's, and the code 9 is no other path's: a run returning err is 1, and a trap is
;; no exit code at all.
;;
;; Assembled with `wasm-tools parse exit-from-thread.wat -o exit-from-thread.wasm` (wasm-tools 1.258.0), by
;; build.sh.
(component
  (import "wasi:cli/exit@0.3.0" (instance $exit (export "exit-with-code" (func (param "status-code" u8)))))
  (alias export $exit "exit-with-code" (func $exitf))

  ;; The child: its async `f` creates T, readies it, resolves, and EXITs with T still ready.
  (component $child
    (import "exit" (func $exit (param "status-code" u8)))
    (core func $exitc (canon lower (func $exit)))
    (core type $bodyt (func (param i32)))
    (core module $tmod
      (import "" "exit" (func $exit (param i32)))
      (table (export "t") 1 1 funcref)
      ;; T: exit with code 9. `exit-with-code` does not return, as Rust std treats it.
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

  ;; The run: call `f`, then YIELD forever.
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core func $flow (canon lower (func $F) async (memory $cm)))
  (core module $runmod
    (import "" "f" (func $f (result i32)))
    (func (export "run") (result i32)
      (drop (call $f))
      (i32.const 1)) ;; YIELD
    (func (export "cb") (param i32 i32 i32) (result i32)
      (i32.const 1))) ;; YIELD forever: only the child's Thread can end the run
  (core instance $runi (instantiate $runmod (with "" (instance (export "f" (func $flow))))))
  (alias core export $runi "run" (core func $runf))
  (alias core export $runi "cb" (core func $runcbf))
  (type $rt (func async (result (result))))
  (func $run (type $rt) (canon lift (core func $runf) async (callback $runcbf)))
  (instance $r (export "run" (func $run)))
  (export "wasi:cli/run@0.3.0" (instance $r))
)
