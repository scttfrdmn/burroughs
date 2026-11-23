;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; cross-agent-wake (#1338, contract §6 R-1 as amended on #1334): a `waitable-set.wait` blocked on one agent returns
;; when another agent of the same instance makes an event ready on a waitable joined to that set. Its canonical
;; memory is SHARED, so it is invalid at the pin (ADR 0129), needs BURROUGHS_CM_SHARED_MEMORY=1, and has no external
;; reader. Its agents are started through the component spawn door (ADR 0131). Assembled with
;; `wasm-tools parse cross-agent-wake.wat -o cross-agent-wake.wasm` (wasm-tools 1.258.0).
;;
;; Each export is one row. run spawns the row's agents, joins them through memory.atomic.wait32 (bounded at 5s, so a
;; lost wake is a verdict and not a hang), and reports memory[0..4096) to the test. In every wake row the waiter
;; publishes its set and parks in waitable-set.wait, and the actor first asks the test (`await-parked`) to see the
;; waiter's goroutine parked in waitableSetWait's select, so the event is made ready after the park and only a wake
;; can deliver it.
;;
;;   stream              A reads a stream<u8> (BLOCKED), joins the read end to S, waits; B writes 4 bytes into it.
;;   future              A reads a future<u32> (BLOCKED), joins the read end to S, waits; B writes the future.
;;   subtask             A async-lowers `pend`, joins the subtask to S, waits; B calls `resolve`, whose impl lands
;;                       pend's result store on B's goroutine.
;;   join                A waits on an empty S; B makes a future read ready (read, then write) and joins its read end
;;                       to S, so the event was ready before it was a member.
;;   netpoll-read-first  F8's netpollBreak: the poller's S holds an idle "socket" (a stream read nothing writes) and a
;;                       guest future's read end (read BLOCKED); the breaker writes the future. The poller then polls
;;                       S, which must say NONE: the socket is still not ready.
;;   netpoll-write-first the breaker writes first (the write parks), joins its write end to S2, and waits on S2; the
;;                       poller then reads, which drives the copy (#1325): the read completes inline and the
;;                       breaker's wait is woken by the poller's read.
;;   batch               64 future reads joined to one set S, drained by two waiters and one poller at once while a
;;                       producer completes them, after both waiters are parked. Each consumer records the indices it
;;                       took; the one that takes the 64th writes three stop futures, also members of S, so no
;;                       consumer is left waiting.
;;
;; Layout: 0 done, 4 published, 8 S, 12 the handle the actor uses, 16 the waiter's waitable, 20 the actor's op
;; result, 24 the actor's await-parked result, 28 the waiter's wait code, 32/36 its event payload, 40 the waiter's
;; arming op result, 44 run's join verdict, 48 S2, 52 the breaker's wait code, 56/60 its payload, 64 the read
;; buffer, 96 the written value "e4sw", 128/132 the idle stream's ends, 136 its read result, 140 the poll after the
;; break, 144 its payload, 160 the idle read's buffer, 1024 pend's result.
;; batch: 1024+4i r_i and 1536+4i w_i (i < 67; 64..66 are the stops), 2048+4i r_i's read buffer, 2560+4i w_i's value
;; (i+1), 3072+256k consumer k's taken indices, 3840+4k its count, 3856+8k its event record, 3888+4k its fault bits,
;; 3904 a read that did not park, 3908 a write that did not complete, 3912 the taken total, 3916 the producer's
;; await-parked result.
(component
  (import "wasi:clocks/monotonic-clock@0.3.0" (instance $mc
    (export "now" (func (result u64)))))
  (import "burroughs:threads/spawn@0.1.0" (instance $th
    (export "spawn" (func (param "arg" u32)))))
  (import "test:e4/ops" (instance $ops
    (export "await-parked" (func (param "kind" u32) (param "n" u32) (result u32)))
    (export "resolve" (func (param "v" u32)))
    (export "report" (func (param "p" u32) (param "n" u32)))
    (export "pend" (func async (result u32)))))
  (core module $mm
    (memory (export "m") 1 1 shared)
    (data (i32.const 96) "e4sw"))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (alias export $th "spawn" (func $spawn))
  (alias export $mc "now" (func $now))
  (alias export $ops "await-parked" (func $park))
  (alias export $ops "resolve" (func $resolve))
  (alias export $ops "report" (func $report))
  (alias export $ops "pend" (func $pend))
  (type $s (stream u8))
  (type $f (future u32))
  ;; the door first, so with the gate off its own refusal is the one heard
  (core func $spawnl (canon lower (func $spawn)))
  (core func $nowl (canon lower (func $now)))
  (core func $parkl (canon lower (func $park)))
  (core func $resolvel (canon lower (func $resolve)))
  (core func $reportl (canon lower (func $report)))
  (core func $pendl (canon lower (func $pend) async (memory $m)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wswait (canon waitable-set.wait (memory $m)))
  (core func $wspoll (canon waitable-set.poll (memory $m)))
  (core func $wsjoin (canon waitable.join))
  (core func $snew (canon stream.new $s))
  (core func $sread (canon stream.read $s async (memory $m)))
  (core func $swrite (canon stream.write $s async (memory $m)))
  (core func $fnew (canon future.new $f))
  (core func $fread (canon future.read $f async (memory $m)))
  (core func $fwrite (canon future.write $f async (memory $m)))
  (core module $gm
    (import "" "mem" (memory 1 1 shared))
    (import "" "spawn" (func $spawn (param i32)))
    (import "" "now" (func $now (result i64)))
    (import "" "park" (func $park (param i32 i32) (result i32)))
    (import "" "resolve" (func $resolve (param i32)))
    (import "" "report" (func $report (param i32 i32)))
    (import "" "pend" (func $pend (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (import "" "wspoll" (func $wspoll (param i32 i32) (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "snew" (func $snew (result i64)))
    (import "" "sread" (func $sread (param i32 i32 i32) (result i32)))
    (import "" "swrite" (func $swrite (param i32 i32 i32) (result i32)))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))

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

    ;; an agent's last act: count itself done and wake run's join
    (func $finish
      (drop (i32.atomic.rmw.add (i32.const 0) (i32.const 1)))
      (drop (memory.atomic.notify (i32.const 0) (i32.const -1))))

    ;; run's join: wait until $n agents have finished, bounded at 5s; 1 if they did
    (func $join (param $n i32) (result i32) (local $dl i64) (local $d i32)
      (local.set $dl (i64.add (call $now) (i64.const 5000000000)))
      (block $b (loop $l
        (local.set $d (i32.atomic.load (i32.const 0)))
        (if (i32.ge_u (local.get $d) (local.get $n)) (then (return (i32.const 1))))
        (br_if $b (i64.gt_u (call $now) (local.get $dl)))
        (drop (memory.atomic.wait32 (i32.const 0) (local.get $d) (i64.const 50000000)))
        (br $l)))
      (i32.const 0))

    ;; a row's end: join its $n agents, record the verdict at 44, report memory[0..4096), return the verdict
    (func $row (param $n i32) (result i32)
      (i32.store (i32.const 44) (call $join (local.get $n)))
      (call $report (i32.const 0) (i32.const 4096))
      (i32.load (i32.const 44)))

    ;; the waiter's last step: publish, then wait on $s and record the event at 28 (code) and 32/36 (payload)
    (func $armwait (param $s i32)
      (i32.atomic.store (i32.const 4) (i32.const 1))
      (i32.store (i32.const 28) (call $wswait (local.get $s) (i32.const 32))))

    ;; the actor's first step: until the waiter has published (2s), and the test sees $n goroutines parked in
    ;; waitable-set.wait ($kind 0); the probe's answer is recorded at 24
    (func $await (param $kind i32) (param $n i32)
      (drop (call $spinfor (i32.const 4) (i32.const 1) (i64.const 2000000000)))
      (i32.store (i32.const 24) (call $park (local.get $kind) (local.get $n))))

    ;; publish a new set at 8 and return it
    (func $newset (result i32) (local $s i32)
      (local.set $s (call $wsnew))
      (i32.store (i32.const 8) (local.get $s))
      (local.get $s))

    ;; ---- stream: A reads, B writes
    (func $streamA (local $p i64) (local $r i32) (local $s i32)
      (local.set $s (call $newset))
      (local.set $p (call $snew))
      (local.set $r (i32.wrap_i64 (local.get $p)))
      (i32.store (i32.const 16) (local.get $r))
      (i32.store (i32.const 12) (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
      (i32.store (i32.const 40) (call $sread (local.get $r) (i32.const 64) (i32.const 4)))
      (call $wsjoin (local.get $r) (local.get $s))
      (call $armwait (local.get $s))
      (call $finish))
    (func $streamB
      (call $await (i32.const 0) (i32.const 1))
      (i32.store (i32.const 20) (call $swrite (i32.load (i32.const 12)) (i32.const 96) (i32.const 4)))
      (call $finish))

    ;; ---- future: A reads, B writes
    (func $futureA (local $p i64) (local $r i32) (local $s i32)
      (local.set $s (call $newset))
      (local.set $p (call $fnew))
      (local.set $r (i32.wrap_i64 (local.get $p)))
      (i32.store (i32.const 16) (local.get $r))
      (i32.store (i32.const 12) (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
      (i32.store (i32.const 40) (call $fread (local.get $r) (i32.const 64)))
      (call $wsjoin (local.get $r) (local.get $s))
      (call $armwait (local.get $s))
      (call $finish))
    (func $futureB
      (call $await (i32.const 0) (i32.const 1))
      (i32.store (i32.const 20) (call $fwrite (i32.load (i32.const 12)) (i32.const 96)))
      (call $finish))

    ;; ---- subtask: A's async lower, resolved by B's call
    (func $subtaskA (local $st i32) (local $s i32)
      (local.set $s (call $newset))
      (local.set $st (call $pend (i32.const 1024)))
      (i32.store (i32.const 40) (local.get $st))
      (i32.store (i32.const 16) (i32.shr_u (local.get $st) (i32.const 4)))
      (call $wsjoin (i32.shr_u (local.get $st) (i32.const 4)) (local.get $s))
      (call $armwait (local.get $s))
      (call $finish))
    (func $subtaskB
      (call $await (i32.const 0) (i32.const 1))
      (call $resolve (i32.const 0x5eed1338))
      (call $finish))

    ;; ---- join: B joins an already-ready read end to the set A waits on
    (func $joinA
      (call $armwait (call $newset))
      (call $finish))
    (func $joinB (local $p i64) (local $r i32)
      (call $await (i32.const 0) (i32.const 1))
      (local.set $p (call $fnew))
      (local.set $r (i32.wrap_i64 (local.get $p)))
      (i32.store (i32.const 16) (local.get $r))
      (i32.store (i32.const 40) (call $fread (local.get $r) (i32.const 64)))
      (i32.store (i32.const 20) (call $fwrite (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))) (i32.const 96)))
      (call $wsjoin (local.get $r) (i32.load (i32.const 8)))
      (call $finish))

    ;; the poll set's idle member, a "socket": a stream<u8> read end with a read pending that nothing writes
    (func $idle (param $s i32) (local $p i64) (local $r i32)
      (local.set $p (call $snew))
      (local.set $r (i32.wrap_i64 (local.get $p)))
      (i32.store (i32.const 128) (local.get $r))
      (i32.store (i32.const 132) (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
      (i32.store (i32.const 136) (call $sread (local.get $r) (i32.const 160) (i32.const 4)))
      (call $wsjoin (local.get $r) (local.get $s)))

    ;; ---- netpoll-read-first: the poller arms its break read and waits; the breaker is futureB
    (func $np1P (local $p i64) (local $r i32) (local $s i32)
      (local.set $s (call $newset))
      (call $idle (local.get $s))
      (local.set $p (call $fnew))
      (local.set $r (i32.wrap_i64 (local.get $p)))
      (i32.store (i32.const 16) (local.get $r))
      (i32.store (i32.const 12) (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
      (i32.store (i32.const 40) (call $fread (local.get $r) (i32.const 64)))
      (call $wsjoin (local.get $r) (local.get $s))
      (call $armwait (local.get $s))
      ;; the socket is still not ready: the break was the only event
      (i32.store (i32.const 140) (call $wspoll (local.get $s) (i32.const 144)))
      (call $finish))

    ;; ---- netpoll-write-first: the breaker writes first and waits on its own set; the poller's read wakes it
    (func $np2B (local $p i64) (local $w i32) (local $s2 i32)
      (local.set $s2 (call $wsnew))
      (i32.store (i32.const 48) (local.get $s2))
      (local.set $p (call $fnew))
      (i32.store (i32.const 16) (i32.wrap_i64 (local.get $p)))
      (local.set $w (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
      (i32.store (i32.const 12) (local.get $w))
      (i32.store (i32.const 20) (call $fwrite (local.get $w) (i32.const 96)))
      (call $wsjoin (local.get $w) (local.get $s2))
      (i32.atomic.store (i32.const 4) (i32.const 1))
      (i32.store (i32.const 52) (call $wswait (local.get $s2) (i32.const 56)))
      (call $finish))
    (func $np2P (local $s i32) (local $r i32) (local $res i32)
      (local.set $s (call $newset))
      (call $idle (local.get $s))
      (call $await (i32.const 0) (i32.const 1))
      (local.set $r (i32.load (i32.const 16)))
      (local.set $res (call $fread (local.get $r) (i32.const 64)))
      (i32.store (i32.const 40) (local.get $res))
      (if (i32.eq (local.get $res) (i32.const -1))
        (then
          ;; the lost break's shape: the read parked behind a write that is already there, and waits for it
          (call $wsjoin (local.get $r) (local.get $s))
          (i32.store (i32.const 28) (call $wswait (local.get $s) (i32.const 32)))))
      (call $finish))

    ;; ---- batch
    (func $batchSetup (local $s i32) (local $i i32) (local $p i64) (local $r i32) (local $o i32)
      (local.set $s (call $newset))
      (block $b (loop $l
        (br_if $b (i32.ge_u (local.get $i) (i32.const 67)))
        (local.set $o (i32.mul (local.get $i) (i32.const 4)))
        (local.set $p (call $fnew))
        (local.set $r (i32.wrap_i64 (local.get $p)))
        (i32.store offset=1024 (local.get $o) (local.get $r))
        (i32.store offset=1536 (local.get $o) (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
        (i32.store offset=2560 (local.get $o) (i32.add (local.get $i) (i32.const 1)))
        (if (i32.ne (call $fread (local.get $r) (i32.add (i32.const 2048) (local.get $o))) (i32.const -1))
          (then (i32.store (i32.const 3904) (i32.const 1))))
        (call $wsjoin (local.get $r) (local.get $s))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $l))))

    (func $bad (param $k i32) (param $bit i32) (local $a i32)
      (local.set $a (i32.add (i32.const 3888) (i32.mul (local.get $k) (i32.const 4))))
      (i32.store (local.get $a) (i32.or (i32.load (local.get $a)) (local.get $bit))))

    ;; take one batch event for consumer $k, whose code is $code and whose payload is at its record; 1 if it is done
    (func $consume (param $k i32) (param $code i32) (result i32)
      (local $ev i32) (local $p1 i32) (local $n i32) (local $j i32)
      (local.set $ev (i32.add (i32.const 3856) (i32.mul (local.get $k) (i32.const 8))))
      (local.set $p1 (i32.load (local.get $ev)))
      (if (i32.ne (local.get $code) (i32.const 4)) (then (call $bad (local.get $k) (i32.const 1))))
      (if (i32.load offset=4 (local.get $ev)) (then (call $bad (local.get $k) (i32.const 2))))
      ;; a stop ends this consumer
      (if (i32.or (i32.or (i32.eq (local.get $p1) (i32.load (i32.const 1280)))
                          (i32.eq (local.get $p1) (i32.load (i32.const 1284))))
                  (i32.eq (local.get $p1) (i32.load (i32.const 1288))))
        (then (return (i32.const 1))))
      (local.set $n (i32.load (i32.add (i32.const 3840) (i32.mul (local.get $k) (i32.const 4)))))
      (if (i32.lt_u (local.get $n) (i32.const 64))
        (then
          (i32.store (i32.add (i32.add (i32.const 3072) (i32.mul (local.get $k) (i32.const 256)))
            (i32.mul (local.get $n) (i32.const 4))) (local.get $p1))
          (i32.store (i32.add (i32.const 3840) (i32.mul (local.get $k) (i32.const 4)))
            (i32.add (local.get $n) (i32.const 1))))
        (else (call $bad (local.get $k) (i32.const 4))))
      (if (i32.eq (i32.atomic.rmw.add (i32.const 3912) (i32.const 1)) (i32.const 63))
        (then
          ;; the 64th: write the three stops, so no consumer still waiting is left waiting
          (block $b (loop $l
            (br_if $b (i32.ge_u (local.get $j) (i32.const 3)))
            (drop (call $fwrite (i32.load offset=1792 (i32.mul (local.get $j) (i32.const 4))) (i32.const 2560)))
            (local.set $j (i32.add (local.get $j) (i32.const 1)))
            (br $l)))
          (return (i32.const 1))))
      (i32.const 0))

    (func $batchW (param $k i32) (local $s i32)
      (local.set $s (i32.load (i32.const 8)))
      (block $b (loop $l
        (br_if $b (call $consume (local.get $k)
          (call $wswait (local.get $s) (i32.add (i32.const 3856) (i32.mul (local.get $k) (i32.const 8))))))
        (br $l)))
      (call $finish))
    (func $batchP (local $s i32) (local $code i32)
      (local.set $s (i32.load (i32.const 8)))
      (block $b (loop $l
        (br_if $b (i32.ge_u (i32.atomic.load (i32.const 3912)) (i32.const 64)))
        (local.set $code (call $wspoll (local.get $s) (i32.const 3872)))
        (if (local.get $code) (then (br_if $b (call $consume (i32.const 2) (local.get $code)))))
        (br $l)))
      (call $finish))
    (func $batchC (local $i i32)
      (i32.store (i32.const 3916) (call $park (i32.const 0) (i32.const 2)))
      (block $b (loop $l
        (br_if $b (i32.ge_u (local.get $i) (i32.const 64)))
        (if (call $fwrite (i32.load offset=1536 (i32.mul (local.get $i) (i32.const 4)))
                          (i32.add (i32.const 2560) (i32.mul (local.get $i) (i32.const 4))))
          (then (i32.store (i32.const 3908) (i32.const 1))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $l)))
      (call $finish))

    (func (export "mstart") (param $k i32)
      (if (i32.eq (local.get $k) (i32.const 1)) (then (call $streamA) (return)))
      (if (i32.eq (local.get $k) (i32.const 2)) (then (call $streamB) (return)))
      (if (i32.eq (local.get $k) (i32.const 3)) (then (call $futureA) (return)))
      (if (i32.eq (local.get $k) (i32.const 4)) (then (call $futureB) (return)))
      (if (i32.eq (local.get $k) (i32.const 5)) (then (call $subtaskA) (return)))
      (if (i32.eq (local.get $k) (i32.const 6)) (then (call $subtaskB) (return)))
      (if (i32.eq (local.get $k) (i32.const 7)) (then (call $joinA) (return)))
      (if (i32.eq (local.get $k) (i32.const 8)) (then (call $joinB) (return)))
      (if (i32.eq (local.get $k) (i32.const 9)) (then (call $np1P) (return)))
      (if (i32.eq (local.get $k) (i32.const 11)) (then (call $np2P) (return)))
      (if (i32.eq (local.get $k) (i32.const 12)) (then (call $np2B) (return)))
      (if (i32.eq (local.get $k) (i32.const 13)) (then (call $batchW (i32.const 0)) (return)))
      (if (i32.eq (local.get $k) (i32.const 14)) (then (call $batchW (i32.const 1)) (return)))
      (if (i32.eq (local.get $k) (i32.const 15)) (then (call $batchP) (return)))
      (if (i32.eq (local.get $k) (i32.const 16)) (then (call $batchC) (return)))
      (unreachable))

    (func (export "stream") (result i32)
      (call $spawn (i32.const 1)) (call $spawn (i32.const 2)) (call $row (i32.const 2)))
    (func (export "future") (result i32)
      (call $spawn (i32.const 3)) (call $spawn (i32.const 4)) (call $row (i32.const 2)))
    (func (export "subtask") (result i32)
      (call $spawn (i32.const 5)) (call $spawn (i32.const 6)) (call $row (i32.const 2)))
    (func (export "join") (result i32)
      (call $spawn (i32.const 7)) (call $spawn (i32.const 8)) (call $row (i32.const 2)))
    (func (export "netpoll-read-first") (result i32)
      (call $spawn (i32.const 9)) (call $spawn (i32.const 4)) (call $row (i32.const 2)))
    (func (export "netpoll-write-first") (result i32)
      (call $spawn (i32.const 11)) (call $spawn (i32.const 12)) (call $row (i32.const 2)))
    (func (export "batch") (result i32)
      (call $batchSetup)
      (call $spawn (i32.const 13)) (call $spawn (i32.const 14)) (call $spawn (i32.const 15)) (call $spawn (i32.const 16))
      (call $row (i32.const 4))))
  (core instance $gi (instantiate $gm (with "" (instance
    (export "mem" (memory $m))
    (export "spawn" (func $spawnl)) (export "now" (func $nowl)) (export "park" (func $parkl))
    (export "resolve" (func $resolvel)) (export "report" (func $reportl)) (export "pend" (func $pendl))
    (export "wsnew" (func $wsnew)) (export "wswait" (func $wswait)) (export "wspoll" (func $wspoll))
    (export "wsjoin" (func $wsjoin))
    (export "snew" (func $snew)) (export "sread" (func $sread)) (export "swrite" (func $swrite))
    (export "fnew" (func $fnew)) (export "fread" (func $fread)) (export "fwrite" (func $fwrite))))))
  (type $at (func async (result u32)))
  (func (export "stream") (type $at) (canon lift (core func $gi "stream")))
  (func (export "future") (type $at) (canon lift (core func $gi "future")))
  (func (export "subtask") (type $at) (canon lift (core func $gi "subtask")))
  (func (export "join") (type $at) (canon lift (core func $gi "join")))
  (func (export "netpoll-read-first") (type $at) (canon lift (core func $gi "netpoll-read-first")))
  (func (export "netpoll-write-first") (type $at) (canon lift (core func $gi "netpoll-write-first")))
  (func (export "batch") (type $at) (canon lift (core func $gi "batch"))))
