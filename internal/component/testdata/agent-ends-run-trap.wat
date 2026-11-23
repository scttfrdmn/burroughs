;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; agent-ends-run-trap (#1357, ADR 0132, row (b)): agent-ends-run-exit.wat with the agent executing `unreachable` in
;; place of the exit, while run is parked in an unbounded `memory.atomic.wait32` that nothing notifies. The trap locks
;; the store (ADR 0105) and ends the run with the trap. Its canonical memory is SHARED, so it is invalid at the pin
;; (ADR 0129), needs BURROUGHS_CM_SHARED_MEMORY=1, and has no external reader. Its memory declares max = min, so it is
;; Go-heap backed and -race sees it. Assembled with
;; `wasm-tools parse agent-ends-run-trap.wat -o agent-ends-run-trap.wasm` (wasm-tools 1.258.0).
;;
;; run spawns agent 1, publishes at 0 that it is about to wait, and waits at 200. The agent spins until run has
;; published (bounded at 2s on the monotonic clock), gives run 100ms to reach its wait, and traps. If run's wait ever
;; returned, run would return ok, and the run would exit 0.
(component
  (import "wasi:clocks/monotonic-clock@0.3.0" (instance $mc
    (export "now" (func (result u64)))))
  (import "burroughs:threads/spawn@0.1.0" (instance $th
    (export "spawn" (func (param "arg" u32)))))
  (core module $mm
    (memory (export "m") 1 1 shared))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (alias export $th "spawn" (func $spawn))
  (alias export $mc "now" (func $now))
  (core func $spawnl (canon lower (func $spawn)))
  (core func $nowl (canon lower (func $now)))
  (core module $gm
    (import "" "mem" (memory 1 1 shared))
    (import "" "spawn" (func $spawn (param i32)))
    (import "" "now" (func $now (result i64)))

    ;; spin until the word at $p is at least $want, or $ns have passed on the monotonic clock
    (func $spinfor (param $p i32) (param $want i32) (param $ns i64) (local $dl i64) (local $i i32)
      (local.set $dl (i64.add (call $now) (local.get $ns)))
      (block $b (loop $l
        (br_if $b (i32.ge_u (i32.atomic.load (local.get $p)) (local.get $want)))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (if (i32.eqz (i32.and (local.get $i) (i32.const 1023)))
          (then (br_if $b (i64.gt_u (call $now) (local.get $dl)))))
        (br $l))))

    ;; spin for $ns on the monotonic clock
    (func $pause (param $ns i64) (local $dl i64)
      (local.set $dl (i64.add (call $now) (local.get $ns)))
      (block $b (loop $l
        (br_if $b (i64.gt_u (call $now) (local.get $dl)))
        (br $l))))

    (func (export "mstart") (param $k i32)
      (call $spinfor (i32.const 0) (i32.const 1) (i64.const 2000000000))
      (call $pause (i64.const 100000000))
      (unreachable))

    (func (export "run") (result i32)
      (call $spawn (i32.const 1))
      (i32.atomic.store (i32.const 0) (i32.const 1))
      ;; nothing notifies 200: only the agent's trap can end this wait
      (drop (memory.atomic.wait32 (i32.const 200) (i32.const 0) (i64.const -1)))
      (i32.const 0)))
  (core instance $gi (instantiate $gm (with "" (instance
    (export "mem" (memory $m))
    (export "spawn" (func $spawnl)) (export "now" (func $nowl))))))
  (type $rt (func async (result (result))))
  ;; the memory option makes the shared memory canonical, which is what admits the door (ADR 0131)
  (func $run (type $rt) (canon lift (core func $gi "run") (memory $m)))
  (instance $r (export "run" (func $run)))
  (export "wasi:cli/run@0.3.0" (instance $r)))
