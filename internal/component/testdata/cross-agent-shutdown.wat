;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; cross-agent-shutdown (#1338, contract §6 R-1 as amended on #1334, and §2 T-5.4): a `waitable-set.wait` blocked on
;; an agent, and a `memory.atomic.wait32` blocked on another, both end when the instance is shut down. Nothing can
;; wake either wait: the set is empty and nothing notifies the address, so each is ADR 0128's wait that blocks for
;; ever, and only shutdown ends it. Its canonical memory is SHARED, so it is invalid at the pin (ADR 0129), needs
;; BURROUGHS_CM_SHARED_MEMORY=1, and has no external reader. It imports nothing but the spawn door (ADR 0131) and the
;; monotonic clock, so the root package's Component can load it. Assembled with
;; `wasm-tools parse cross-agent-shutdown.wat -o cross-agent-shutdown.wasm` (wasm-tools 1.258.0).
;;
;;   shutdown           spawns agent 1, which publishes (4) and waits on an empty set, and agent 2, which publishes
;;                      (8) and waits in memory.atomic.wait32 at 200 with no timeout. run spins until both have
;;                      published (bounded at 2s) and returns 1 if both did, leaving both agents blocked.
;;   shutdown-inflight  the same, then run's own agent waits in memory.atomic.wait32 at 204 with no timeout, as a Go M
;;                      idle in notesleep does, so the call is still in flight at Close.
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
  (core func $wsnew (canon waitable-set.new))
  (core func $wswait (canon waitable-set.wait (memory $m)))
  (core module $gm
    (import "" "mem" (memory 1 1 shared))
    (import "" "spawn" (func $spawn (param i32)))
    (import "" "now" (func $now (result i64)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))

    ;; spin until the word at $p is at least $want, or $ns have passed on the monotonic clock; 1 if it was reached
    (func $spinfor (param $p i32) (param $want i32) (param $ns i64) (result i32) (local $dl i64) (local $i i32)
      (local.set $dl (i64.add (call $now) (local.get $ns)))
      (block $b (loop $l
        (br_if $b (i32.ge_u (i32.atomic.load (local.get $p)) (local.get $want)))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (if (i32.eqz (i32.and (local.get $i) (i32.const 1023)))
          (then (if (i64.gt_u (call $now) (local.get $dl)) (then (return (i32.const 0))))))
        (br $l)))
      (i32.const 1))

    (func (export "mstart") (param $k i32) (local $s i32)
      (if (i32.eq (local.get $k) (i32.const 1))
        (then
          (local.set $s (call $wsnew))
          (i32.atomic.store (i32.const 4) (i32.const 1))
          ;; an empty set: nothing can wake this
          (drop (call $wswait (local.get $s) (i32.const 64)))
          ;; reached only if the wait returned, which it must not
          (i32.atomic.store (i32.const 12) (i32.const 1))
          (return)))
      (if (i32.eq (local.get $k) (i32.const 2))
        (then
          (i32.atomic.store (i32.const 8) (i32.const 1))
          ;; nothing notifies 200
          (drop (memory.atomic.wait32 (i32.const 200) (i32.const 0) (i64.const -1)))
          (i32.atomic.store (i32.const 16) (i32.const 1))
          (return)))
      (unreachable))

    (func $start (result i32)
      (call $spawn (i32.const 1))
      (call $spawn (i32.const 2))
      (i32.and (call $spinfor (i32.const 4) (i32.const 1) (i64.const 2000000000))
               (call $spinfor (i32.const 8) (i32.const 1) (i64.const 2000000000))))

    (func (export "shutdown") (result i32)
      (call $start))

    (func (export "shutdown-inflight") (result i32)
      (drop (call $start))
      (drop (memory.atomic.wait32 (i32.const 204) (i32.const 0) (i64.const -1)))
      (i32.const 0)))
  (core instance $gi (instantiate $gm (with "" (instance
    (export "mem" (memory $m))
    (export "spawn" (func $spawnl)) (export "now" (func $nowl))
    (export "wsnew" (func $wsnew)) (export "wswait" (func $wswait))))))
  (type $at (func async (result u32)))
  (func (export "shutdown") (type $at) (canon lift (core func $gi "shutdown")))
  (func (export "shutdown-inflight") (type $at) (canon lift (core func $gi "shutdown-inflight"))))
