;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; agents-shared (#1336, ADR 0130): a component whose canonical memory is SHARED, so it is a free instance, whose
;; exports spawn agents through a test import (`test:e2/ops.spawn`, served by the test, since the component's spawn
;; door is E3's) and drive the multi-agent paths. Invalid at the pin (a shared canonical memory, ADR 0129), so it
;; needs BURROUGHS_CM_SHARED_MEMORY=1 and has no external reader. Assembled with `wasm-tools parse agents-shared.wat
;; -o agents-shared.wasm` (wasm-tools 1.258.0).
;;
;;   run          spawns agents 1 and 2, which spin until `probe` (the test) has seen them executing at once, then
;;                each, at once: reads its thread.index, sets its own global and context slot and reads both back
;;                after a rendezvous, churns 32 waitable sets (new, drop), polls an empty set, calls now and
;;                get-random-u64, and waits on its own set for a wait-for(1ms) subtask. Agent 1 then makes a
;;                stream<u8> and a future<u32> and writes "hello-from-1" into the stream; agent 2 reads it and
;;                writes 0xc0ffee into the future, which agent 1 reads. Cross-agent events are taken by polling.
;;                The future's read is ordered first (296). It was ordered so when a read arriving after a
;;                parked write of the same instance's future was not driven, and both parked (#1325 item 1).
;;                Since #1354 the reverse order completes too, and `future-write-then-read` witnesses it; the
;;                order is kept so this fixture's reading is unchanged.
;;                run joins both through memory.atomic.wait32 and reports memory[1024..4096) to the test.
;;   sync-tail    sync-TYPED: spawns agent 3, reads a future (BLOCKED), and waits for it while agent 3 executes;
;;                agent 3 spins, then writes 0x7a11. Returns the value read.
;;   window       spawns agent 4, which calls `hold`, an async import whose impl the test holds open; run's own
;;                wait-for(1ms) store must reach it while agent 4 is still in that host code. Calls `woke` after.
;;   late-string  async-lowers `late`, whose string result the test resolves after the lower returned, off any
;;                agent's goroutine, so its store's realloc is refused (ADR 0130's §3).
;;
;; Layout: 256 release, 264 done, 268..280 the stream's and the future's ends, 284 published, 288 rendezvous,
;; 292 tail-go, 296 the future's read is armed; area(k) = 1024*(k+1): +0 fail bits, +4 thread.index, +8 context, +12 global, +16 event,
;; +32 stream buffer, +64 future buffer, +256 the 32 churned set handles.
(component
  (import "wasi:clocks/monotonic-clock@0.3.0" (instance $mc
    (export "wait-for" (func async (param "how-long" u64)))
    (export "now" (func (result u64)))))
  (import "wasi:random/random@0.3.0" (instance $rand
    (export "get-random-u64" (func (result u64)))))
  (import "test:e2/ops" (instance $ops
    (export "spawn" (func (param "k" u32)))
    (export "probe" (func (result u32)))
    (export "report" (func (param "p" u32) (param "n" u32)))
    (export "await-hold" (func))
    (export "woke" (func))
    (export "hold" (func async))
    (export "late" (func async (result string)))))
  (core module $mm
    (memory (export "m") 1 1 shared)
    (func (export "realloc") (param i32 i32 i32 i32) (result i32) (i32.const 8192)))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (alias export $mc "wait-for" (func $wf))
  (alias export $mc "now" (func $now))
  (alias export $rand "get-random-u64" (func $rnd))
  (alias export $ops "spawn" (func $spawn))
  (alias export $ops "probe" (func $probe))
  (alias export $ops "report" (func $report))
  (alias export $ops "await-hold" (func $awaithold))
  (alias export $ops "woke" (func $woke))
  (alias export $ops "hold" (func $hold))
  (alias export $ops "late" (func $late))
  (type $s (stream u8))
  (type $f (future u32))
  (core func $wfl (canon lower (func $wf) async))
  (core func $nowl (canon lower (func $now)))
  (core func $rndl (canon lower (func $rnd)))
  (core func $spawnl (canon lower (func $spawn)))
  (core func $probel (canon lower (func $probe)))
  (core func $reportl (canon lower (func $report)))
  (core func $awaitl (canon lower (func $awaithold)))
  (core func $wokel (canon lower (func $woke)))
  (core func $holdl (canon lower (func $hold) async))
  (core func $latel (canon lower (func $late) async (memory $m) (realloc (func $mi "realloc"))))
  (core func $wsnew (canon waitable-set.new))
  (core func $wswait (canon waitable-set.wait (memory $m)))
  (core func $wspoll (canon waitable-set.poll (memory $m)))
  (core func $wsdrop (canon waitable-set.drop))
  (core func $wsjoin (canon waitable.join))
  (core func $stdrop (canon subtask.drop))
  (core func $snew (canon stream.new $s))
  (core func $sread (canon stream.read $s async (memory $m)))
  (core func $swrite (canon stream.write $s async (memory $m)))
  (core func $fnew (canon future.new $f))
  (core func $fread (canon future.read $f async (memory $m)))
  (core func $fwrite (canon future.write $f async (memory $m)))
  (core func $cget (canon context.get i32 0))
  (core func $cset (canon context.set i32 0))
  (core func $tidx (canon thread.index))
  (core module $gm
    (import "" "mem" (memory 1 1 shared))
    (import "" "wf" (func $wf (param i64) (result i32)))
    (import "" "now" (func $now (result i64)))
    (import "" "rnd" (func $rnd (result i64)))
    (import "" "spawn" (func $spawn (param i32)))
    (import "" "probe" (func $probe (result i32)))
    (import "" "report" (func $report (param i32 i32)))
    (import "" "awaithold" (func $awaithold))
    (import "" "woke" (func $woke))
    (import "" "hold" (func $hold (result i32)))
    (import "" "late" (func $late (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (import "" "wspoll" (func $wspoll (param i32 i32) (result i32)))
    (import "" "wsdrop" (func $wsdrop (param i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "stdrop" (func $stdrop (param i32)))
    (import "" "snew" (func $snew (result i64)))
    (import "" "sread" (func $sread (param i32 i32 i32) (result i32)))
    (import "" "swrite" (func $swrite (param i32 i32 i32) (result i32)))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))
    (import "" "cget" (func $cget (result i32)))
    (import "" "cset" (func $cset (param i32)))
    (import "" "tidx" (func $tidx (result i32)))
    ;; a defined global, per agent under T-6
    (global $g (mut i32) (i32.const 0))

    (func $fail (param $a i32) (param $bit i32)
      (i32.store (local.get $a) (i32.or (i32.load (local.get $a)) (local.get $bit))))

    ;; Poll set $s until it reports an event, check its code, and return its payload p2.
    (func $pollfor (param $s i32) (param $a i32) (param $want i32) (result i32) (local $code i32)
      (block $b (loop $l
        (local.set $code (call $wspoll (local.get $s) (i32.add (local.get $a) (i32.const 16))))
        (br_if $b (local.get $code))
        (br $l)))
      (if (i32.ne (local.get $code) (local.get $want)) (then (call $fail (local.get $a) (i32.const 64))))
      (i32.load offset=20 (local.get $a)))

    ;; What every agent of `run` does, at the same time as the other.
    (func $common (param $k i32) (param $a i32)
      (local $i i32) (local $h i32) (local $s i32) (local $st i32) (local $t1 i64) (local $t2 i64)
      (i32.store offset=4 (local.get $a) (call $tidx))
      ;; T-6 and context: write this agent's own, meet the other, read both back
      (global.set $g (local.get $k))
      (call $cset (i32.add (i32.mul (local.get $k) (i32.const 100)) (i32.const 7)))
      (drop (i32.atomic.rmw.add (i32.const 288) (i32.const 1)))
      (block $b (loop $l (br_if $b (i32.ge_u (i32.atomic.load (i32.const 288)) (i32.const 2))) (br $l)))
      (i32.store offset=12 (local.get $a) (global.get $g))
      (i32.store offset=8 (local.get $a) (call $cget))
      ;; waitable-set churn, interleaved with the other agent's
      (block $b (loop $l
        (br_if $b (i32.ge_u (local.get $i) (i32.const 32)))
        (local.set $h (call $wsnew))
        (i32.store offset=256 (i32.add (local.get $a) (i32.shl (local.get $i) (i32.const 2))) (local.get $h))
        (call $wsdrop (local.get $h))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $l)))
      ;; an empty set polls NONE
      (local.set $s (call $wsnew))
      (if (i32.ne (call $wspoll (local.get $s) (i32.add (local.get $a) (i32.const 16))) (i32.const 0))
        (then (call $fail (local.get $a) (i32.const 1))))
      (local.set $t1 (call $now))
      (drop (call $rnd))
      ;; wait-for(1ms), async-lowered, joined to this agent's set and waited on
      (local.set $st (call $wf (i64.const 1000000)))
      (if (i32.ne (i32.and (local.get $st) (i32.const 0xf)) (i32.const 2))
        (then
          (call $wsjoin (i32.shr_u (local.get $st) (i32.const 4)) (local.get $s))
          (if (i32.ne (call $wswait (local.get $s) (i32.add (local.get $a) (i32.const 16))) (i32.const 1))
            (then (call $fail (local.get $a) (i32.const 2))))
          (if (i32.ne (i32.load offset=20 (local.get $a)) (i32.const 2))
            (then (call $fail (local.get $a) (i32.const 2))))
          (call $stdrop (i32.shr_u (local.get $st) (i32.const 4)))))
      (call $wsdrop (local.get $s))
      (local.set $t2 (call $now))
      (if (i64.lt_u (local.get $t2) (i64.add (local.get $t1) (i64.const 1000000)))
        (then (call $fail (local.get $a) (i32.const 4)))))

    ;; Agent 1: make the stream and the future, publish their ends, write the stream, read the future.
    (func $producer (param $a i32) (local $p i64) (local $q i64) (local $s i32) (local $r i32)
      (local.set $p (call $snew))
      (local.set $q (call $fnew))
      (i32.store (i32.const 268) (i32.wrap_i64 (local.get $p)))
      (i32.store (i32.const 272) (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
      (i32.store (i32.const 276) (i32.wrap_i64 (local.get $q)))
      (i32.store (i32.const 280) (i32.wrap_i64 (i64.shr_u (local.get $q) (i64.const 32))))
      (i32.atomic.store (i32.const 284) (i32.const 1))
      (i32.store offset=32 (local.get $a) (i32.const 0x6c6c6568)) ;; "hell"
      (i32.store offset=36 (local.get $a) (i32.const 0x72662d6f)) ;; "o-fr"
      (i32.store offset=40 (local.get $a) (i32.const 0x312d6d6f)) ;; "om-1"
      (local.set $s (call $wsnew))
      (local.set $r (call $swrite (i32.load (i32.const 272)) (i32.add (local.get $a) (i32.const 32)) (i32.const 12)))
      (if (i32.eq (local.get $r) (i32.const -1))
        (then
          (call $wsjoin (i32.load (i32.const 272)) (local.get $s))
          (local.set $r (call $pollfor (local.get $s) (local.get $a) (i32.const 3)))))
      (if (i32.ne (local.get $r) (i32.const 0xc0)) (then (call $fail (local.get $a) (i32.const 8))))
      ;; the read goes first, and says so, so the consumer's write waits for this flag. The order is the
      ;; fixture's choice, not a workaround: since #1354 a read arriving after a parked write completes both
      (local.set $r (call $fread (i32.load (i32.const 276)) (i32.add (local.get $a) (i32.const 64))))
      (i32.atomic.store (i32.const 296) (i32.const 1))
      (if (i32.eq (local.get $r) (i32.const -1))
        (then
          (call $wsjoin (i32.load (i32.const 276)) (local.get $s))
          (local.set $r (call $pollfor (local.get $s) (local.get $a) (i32.const 4)))))
      (if (i32.ne (i32.and (local.get $r) (i32.const 0xf)) (i32.const 0)) (then (call $fail (local.get $a) (i32.const 16))))
      (if (i32.ne (i32.load offset=64 (local.get $a)) (i32.const 0xc0ffee)) (then (call $fail (local.get $a) (i32.const 32)))))

    ;; Agent 2: read the stream, write the future.
    (func $consumer (param $a i32) (local $s i32) (local $r i32)
      (block $b (loop $l (br_if $b (i32.atomic.load (i32.const 284))) (br $l)))
      (local.set $s (call $wsnew))
      (local.set $r (call $sread (i32.load (i32.const 268)) (i32.add (local.get $a) (i32.const 32)) (i32.const 12)))
      (if (i32.eq (local.get $r) (i32.const -1))
        (then
          (call $wsjoin (i32.load (i32.const 268)) (local.get $s))
          (local.set $r (call $pollfor (local.get $s) (local.get $a) (i32.const 2)))))
      (if (i32.ne (local.get $r) (i32.const 0xc0)) (then (call $fail (local.get $a) (i32.const 8))))
      (if (i32.ne (i32.load offset=32 (local.get $a)) (i32.const 0x6c6c6568)) (then (call $fail (local.get $a) (i32.const 128))))
      (if (i32.ne (i32.load offset=36 (local.get $a)) (i32.const 0x72662d6f)) (then (call $fail (local.get $a) (i32.const 128))))
      (if (i32.ne (i32.load offset=40 (local.get $a)) (i32.const 0x312d6d6f)) (then (call $fail (local.get $a) (i32.const 128))))
      (i32.store offset=64 (local.get $a) (i32.const 0xc0ffee))
      (block $b (loop $l (br_if $b (i32.atomic.load (i32.const 296))) (br $l)))
      (local.set $r (call $fwrite (i32.load (i32.const 280)) (i32.add (local.get $a) (i32.const 64))))
      (if (i32.eq (local.get $r) (i32.const -1))
        (then
          (call $wsjoin (i32.load (i32.const 280)) (local.get $s))
          (local.set $r (call $pollfor (local.get $s) (local.get $a) (i32.const 5)))))
      (if (i32.ne (i32.and (local.get $r) (i32.const 0xf)) (i32.const 0)) (then (call $fail (local.get $a) (i32.const 16)))))

    ;; Agent 3: wait for sync-tail's go, keep executing a while, then write the future it reads.
    (func $tailagent (param $a i32) (local $i i32)
      (block $b (loop $l (br_if $b (i32.atomic.load (i32.const 292))) (br $l)))
      (block $b (loop $l
        (br_if $b (i32.ge_u (local.get $i) (i32.const 200000)))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $l)))
      (i32.store offset=64 (local.get $a) (i32.const 0x7a11))
      (drop (call $fwrite (i32.load (i32.const 280)) (i32.add (local.get $a) (i32.const 64)))))

    (func (export "mstart") (param $k i32) (local $a i32)
      (local.set $a (i32.mul (i32.add (local.get $k) (i32.const 1)) (i32.const 1024)))
      (if (i32.le_u (local.get $k) (i32.const 2))
        (then
          (block $b (loop $l (br_if $b (i32.atomic.load (i32.const 256))) (br $l)))
          (call $common (local.get $k) (local.get $a))
          (if (i32.eq (local.get $k) (i32.const 1))
            (then (call $producer (local.get $a)))
            (else (call $consumer (local.get $a))))
          (drop (i32.atomic.rmw.add (i32.const 264) (i32.const 1)))
          (drop (memory.atomic.notify (i32.const 264) (i32.const -1)))
          (return)))
      (if (i32.eq (local.get $k) (i32.const 3)) (then (call $tailagent (local.get $a)) (return)))
      (if (i32.eq (local.get $k) (i32.const 4)) (then (i32.store (local.get $a) (call $hold)) (return)))
      unreachable)

    (func (export "run") (result i32) (local $n i32)
      (global.set $g (i32.const 0x77))
      (call $cset (i32.const 0x55))
      (i32.store offset=4 (i32.const 1024) (call $tidx))
      (call $spawn (i32.const 1))
      (call $spawn (i32.const 2))
      ;; the test samples the executing agents while both spin, and says whether it saw them at once
      (if (i32.eqz (call $probe)) (then (return (i32.const 0xdead))))
      (i32.atomic.store (i32.const 256) (i32.const 1))
      (block $b (loop $l
        (local.set $n (i32.atomic.load (i32.const 264)))
        (br_if $b (i32.ge_u (local.get $n) (i32.const 2)))
        (drop (memory.atomic.wait32 (i32.const 264) (local.get $n) (i64.const -1)))
        (br $l)))
      (i32.store offset=12 (i32.const 1024) (global.get $g))
      (i32.store offset=8 (i32.const 1024) (call $cget))
      (call $report (i32.const 1024) (i32.const 3072))
      (i32.const 0))

    (func (export "sync-tail") (result i32) (local $q i64) (local $s i32)
      (local.set $q (call $fnew))
      (i32.store (i32.const 276) (i32.wrap_i64 (local.get $q)))
      (i32.store (i32.const 280) (i32.wrap_i64 (i64.shr_u (local.get $q) (i64.const 32))))
      (call $spawn (i32.const 3))
      (if (i32.ne (call $fread (i32.load (i32.const 276)) (i32.const 1088)) (i32.const -1))
        (then (return (i32.const 0xbad))))
      (local.set $s (call $wsnew))
      (call $wsjoin (i32.load (i32.const 276)) (local.get $s))
      (i32.atomic.store (i32.const 292) (i32.const 1))
      (drop (call $wswait (local.get $s) (i32.const 1040)))
      (i32.load (i32.const 1088)))

    (func (export "window") (result i32) (local $s i32) (local $st i32)
      (call $spawn (i32.const 4))
      (call $awaithold)
      (local.set $s (call $wsnew))
      (local.set $st (call $wf (i64.const 1000000)))
      (if (i32.ne (i32.and (local.get $st) (i32.const 0xf)) (i32.const 2))
        (then
          (call $wsjoin (i32.shr_u (local.get $st) (i32.const 4)) (local.get $s))
          (drop (call $wswait (local.get $s) (i32.const 1040)))))
      (call $woke)
      (i32.const 0))

    (func (export "late-string") (result i32) (local $s i32) (local $st i32)
      (local.set $st (call $late (i32.const 1104)))
      (call $woke)
      (local.set $s (call $wsnew))
      (call $wsjoin (i32.shr_u (local.get $st) (i32.const 4)) (local.get $s))
      (drop (call $wswait (local.get $s) (i32.const 1040)))
      (i32.load (i32.const 1108))))
  (core instance $gi (instantiate $gm (with "" (instance
    (export "mem" (memory $m))
    (export "wf" (func $wfl)) (export "now" (func $nowl)) (export "rnd" (func $rndl))
    (export "spawn" (func $spawnl)) (export "probe" (func $probel)) (export "report" (func $reportl))
    (export "awaithold" (func $awaitl)) (export "woke" (func $wokel)) (export "hold" (func $holdl))
    (export "late" (func $latel))
    (export "wsnew" (func $wsnew)) (export "wswait" (func $wswait)) (export "wspoll" (func $wspoll))
    (export "wsdrop" (func $wsdrop)) (export "wsjoin" (func $wsjoin)) (export "stdrop" (func $stdrop))
    (export "snew" (func $snew)) (export "sread" (func $sread)) (export "swrite" (func $swrite))
    (export "fnew" (func $fnew)) (export "fread" (func $fread)) (export "fwrite" (func $fwrite))
    (export "cget" (func $cget)) (export "cset" (func $cset)) (export "tidx" (func $tidx))))))
  (type $at (func async (result u32)))
  (type $st (func (result u32)))
  (func (export "run") (type $at) (canon lift (core func $gi "run")))
  (func (export "sync-tail") (type $st) (canon lift (core func $gi "sync-tail")))
  (func (export "window") (type $at) (canon lift (core func $gi "window")))
  (func (export "late-string") (type $at) (canon lift (core func $gi "late-string"))))
