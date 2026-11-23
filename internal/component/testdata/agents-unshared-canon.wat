;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; agents-unshared-canon (#1336, ADR 0130): agents-shared's peak half, in an instance that is NOT free. Its
;; canonical memory (the one `waitable-set.poll` names) is unshared; the agents rendezvous in a second, shared
;; memory, which is what lets the core instance spawn at all. So ADR 0101 holds exactly: each agent takes the run
;; slot at entry, and run holds it inside `probe`, so the test sees one executing agent and two parked at the slot.
;; `run` returns 0xdead when the probe did not see them at once. Valid at the pin (wasm-tools 1.258.0 `validate
;; --features all`). Assembled with `wasm-tools parse agents-unshared-canon.wat -o agents-unshared-canon.wasm`.
(component
  (import "test:e2/ops" (instance $ops
    (export "spawn" (func (param "k" u32)))
    (export "probe" (func (result u32)))))
  (core module $mm
    (memory (export "sm") 1 1 shared)
    (memory (export "m") 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "sm" (core memory $sm))
  (alias core export $mi "m" (core memory $cm))
  (alias export $ops "spawn" (func $spawn))
  (alias export $ops "probe" (func $probe))
  (core func $spawnl (canon lower (func $spawn)))
  (core func $probel (canon lower (func $probe)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wspoll (canon waitable-set.poll (memory $cm)))
  (core module $gm
    (import "" "sm" (memory 1 1 shared))
    (import "" "m" (memory 1))
    (import "" "spawn" (func $spawn (param i32)))
    (import "" "probe" (func $probe (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wspoll" (func $wspoll (param i32 i32) (result i32)))
    (func (export "mstart") (param $k i32)
      (block $b (loop $l (br_if $b (i32.atomic.load (i32.const 256))) (br $l)))
      (drop (call $wspoll (call $wsnew) (i32.const 16)))
      (drop (i32.atomic.rmw.add (i32.const 264) (i32.const 1)))
      (drop (memory.atomic.notify (i32.const 264) (i32.const -1))))
    (func (export "run") (result i32) (local $n i32)
      (call $spawn (i32.const 1))
      (call $spawn (i32.const 2))
      (if (i32.eqz (call $probe)) (then (return (i32.const 0xdead))))
      (i32.atomic.store (i32.const 256) (i32.const 1))
      (block $b (loop $l
        (local.set $n (i32.atomic.load (i32.const 264)))
        (br_if $b (i32.ge_u (local.get $n) (i32.const 2)))
        (drop (memory.atomic.wait32 (i32.const 264) (local.get $n) (i64.const -1)))
        (br $l)))
      (i32.const 0)))
  (core instance $gi (instantiate $gm (with "" (instance
    (export "sm" (memory $sm)) (export "m" (memory $cm))
    (export "spawn" (func $spawnl)) (export "probe" (func $probel))
    (export "wsnew" (func $wsnew)) (export "wspoll" (func $wspoll))))))
  (type $at (func async (result u32)))
  (func (export "run") (type $at) (canon lift (core func $gi "run"))))
