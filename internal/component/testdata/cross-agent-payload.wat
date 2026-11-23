;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; cross-agent-payload (#1339, discharging #1227; contract §6 R-3, and R-4's append stamped on #1334): an event one
;; agent takes from a waitable set observes B-MM-1 for the payload another agent wrote. Agent B fills memory with
;; plain stores and then completes a stream or future write; agent A, which armed the read, takes the event and reads
;; what B wrote with plain loads, checking every byte or word. Its canonical memory is SHARED, so it is invalid at the
;; pin (ADR 0129), needs BURROUGHS_CM_SHARED_MEMORY=1, and has no external reader. Its agents are started through the
;; component spawn door (ADR 0131). It imports nothing but the spawn door and the monotonic clock, so the root
;; package's Component can load it, and its places are read through the `word` export. Assembled with
;; `wasm-tools parse cross-agent-payload.wat -o cross-agent-payload.wasm` (wasm-tools 1.258.0).
;;
;; Each row export runs $iters iterations between one A and one B, then joins them through memory.atomic.wait32
;; (bounded at 60s, so a lost event is a verdict and not a hang) and returns the join verdict. In every iteration A
;; arms its read first and only then publishes the iteration at 4 (an atomic store, so the only edge it buys runs
;; from A to B); B spins on 4, writes, and its write is the copy's second arriver, so the copy runs on B's goroutine
;; and A learns of it only from the event.
;;
;;   stream       one stream<u8>, its read end joined to S once. A: stream.read 256 bytes at 2048 (BLOCKED), wait.
;;                B: 256 plain i32.store8 of the iteration's pattern at 1024, then stream.write of them, which
;;                copies them into A's buffer. A checks all 256 bytes after the event.
;;   future       a fresh future<u32> per iteration. A: future.read at 2048 (BLOCKED), join to S, wait. B: 256
;;                plain i32.store words and a 1024-byte memory.fill at an offset that moves with the iteration,
;;                then future.write of that offset. A reads the offset from the future, then all 256 words and
;;                1024 bytes at it with plain loads.
;;   future-poll  the future row, A taking the event by waitable-set.poll in a loop rather than by wait.
;;
;; The two store kinds are both plain to the guest and differ in the engine, which is why both are here: an aligned
;; i32.store or i32.store8 is an atomic access in Go (ADR 0054, ADR 0111), and memory.fill is a plain Go write
;; (ADR 0064), the one the race detector can see race against A's loads if nothing orders them.
;;
;; Layout: 0 done, 4 A's published iteration, 8 S, 12 the write end B uses, 16 A's read end, 20 run's join verdict,
;; 24 A's last completed iteration, 28 B's last completed iteration, 32 A's polls that found nothing (future-poll),
;; 64 A's fault count then its first fault (68 iteration, 72 kind, 76 index, 80 got, 84 want), 96 B's fault count
;; then its first fault (100 iteration, 104 kind, 108 got, 112 want), 128 A's event record (p1, p2), 1024 B's write
;; buffer, 2048 A's read buffer, 4096+2048*(i&7) iteration i's future region: 256 words, then 1024 filled bytes.
;;
;; A's fault kinds: 1 the read did not return BLOCKED, 2 the event code, 3 the event index, 4 the event payload, 5 the
;; future's offset, 6 a region word, 7 a filled byte, 8 a stream byte, 9 a poll found nothing for 5s. B's: 1 A never
;; published the iteration (5s), 2 the write's result.
;;
;; Patterns, each different from the iteration before it and never zero: stream byte j of iteration i is i*2+j*6+1
;; (mod 256); region word j is i<<16|j; a filled byte is i*2+1 (mod 256).
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
  (type $s (stream u8))
  (type $f (future u32))
  ;; the door first, so with the gate off its own refusal is the one heard
  (core func $spawnl (canon lower (func $spawn)))
  (core func $nowl (canon lower (func $now)))
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
  (core func $fdropr (canon future.drop-readable $f))
  (core module $gm
    (import "" "mem" (memory 1 1 shared))
    (import "" "spawn" (func $spawn (param i32)))
    (import "" "now" (func $now (result i64)))
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
    (import "" "fdropr" (func $fdropr (param i32)))

    ;; how many iterations each row runs
    (global $iters i32 (i32.const 1000))

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

    ;; run's join: wait until $n agents have finished, bounded at 60s; 1 if they did
    (func $join (param $n i32) (result i32) (local $dl i64) (local $d i32)
      (local.set $dl (i64.add (call $now) (i64.const 60000000000)))
      (block $b (loop $l
        (local.set $d (i32.atomic.load (i32.const 0)))
        (if (i32.ge_u (local.get $d) (local.get $n)) (then (return (i32.const 1))))
        (br_if $b (i64.gt_u (call $now) (local.get $dl)))
        (drop (memory.atomic.wait32 (i32.const 0) (local.get $d) (i64.const 50000000)))
        (br $l)))
      (i32.const 0))

    ;; A's fault: count it at 64, and record the first one's iteration, kind, index, got and want
    (func $faultA (param $kind i32) (param $i i32) (param $j i32) (param $got i32) (param $want i32)
      (if (i32.eqz (i32.load (i32.const 64)))
        (then
          (i32.store (i32.const 68) (local.get $i))
          (i32.store (i32.const 72) (local.get $kind))
          (i32.store (i32.const 76) (local.get $j))
          (i32.store (i32.const 80) (local.get $got))
          (i32.store (i32.const 84) (local.get $want))))
      (i32.store (i32.const 64) (i32.add (i32.load (i32.const 64)) (i32.const 1))))

    ;; B's fault, at 96 the same way
    (func $faultB (param $kind i32) (param $i i32) (param $got i32) (param $want i32)
      (if (i32.eqz (i32.load (i32.const 96)))
        (then
          (i32.store (i32.const 100) (local.get $i))
          (i32.store (i32.const 104) (local.get $kind))
          (i32.store (i32.const 108) (local.get $got))
          (i32.store (i32.const 112) (local.get $want))))
      (i32.store (i32.const 96) (i32.add (i32.load (i32.const 96)) (i32.const 1))))

    ;; A's check of the event it took into 128: its code, its index and its payload; 1 if all three are as wanted
    (func $event (param $i i32) (param $code i32) (param $wcode i32) (param $idx i32) (param $wp2 i32) (result i32)
      (if (i32.ne (local.get $code) (local.get $wcode))
        (then (call $faultA (i32.const 2) (local.get $i) (i32.const 0) (local.get $code) (local.get $wcode))
              (return (i32.const 0))))
      (if (i32.ne (i32.load (i32.const 128)) (local.get $idx))
        (then (call $faultA (i32.const 3) (local.get $i) (i32.const 0) (i32.load (i32.const 128)) (local.get $idx))
              (return (i32.const 0))))
      (if (i32.ne (i32.load (i32.const 132)) (local.get $wp2))
        (then (call $faultA (i32.const 4) (local.get $i) (i32.const 0) (i32.load (i32.const 132)) (local.get $wp2))
              (return (i32.const 0))))
      (i32.const 1))

    ;; B's wait for A to publish iteration $i; 1 if it did within 5s
    (func $awaitA (param $i i32) (result i32)
      (if (call $spinfor (i32.const 4) (local.get $i) (i64.const 5000000000)) (then (return (i32.const 1))))
      (call $faultB (i32.const 1) (local.get $i) (i32.atomic.load (i32.const 4)) (local.get $i))
      (i32.const 0))

    ;; ---- stream: A reads 256 bytes per iteration from one stream, B writes them
    (func $streamA (local $p i64) (local $r i32) (local $s i32) (local $i i32) (local $code i32) (local $j i32)
      (local $got i32) (local $want i32)
      (local.set $s (call $wsnew))
      (i32.store (i32.const 8) (local.get $s))
      (local.set $p (call $snew))
      (local.set $r (i32.wrap_i64 (local.get $p)))
      (i32.store (i32.const 16) (local.get $r))
      (i32.store (i32.const 12) (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
      (call $wsjoin (local.get $r) (local.get $s))
      (local.set $i (i32.const 1))
      (block $done (loop $it
        (br_if $done (i32.gt_u (local.get $i) (global.get $iters)))
        (local.set $code (call $sread (local.get $r) (i32.const 2048) (i32.const 256)))
        (if (i32.ne (local.get $code) (i32.const -1))
          (then (call $faultA (i32.const 1) (local.get $i) (i32.const 0) (local.get $code) (i32.const -1))
                (br $done)))
        (i32.atomic.store (i32.const 4) (local.get $i))
        ;; STREAM_READ, COMPLETED with a progress of 256
        (br_if $done (i32.eqz (call $event (local.get $i) (call $wswait (local.get $s) (i32.const 128))
          (i32.const 2) (local.get $r) (i32.const 4096))))
        (local.set $j (i32.const 0))
        (block $b (loop $l
          (br_if $b (i32.ge_u (local.get $j) (i32.const 256)))
          (local.set $got (i32.load8_u offset=2048 (local.get $j)))
          (local.set $want (i32.and (i32.add (i32.add (i32.mul (local.get $i) (i32.const 2))
            (i32.mul (local.get $j) (i32.const 6))) (i32.const 1)) (i32.const 255)))
          (if (i32.ne (local.get $got) (local.get $want))
            (then (call $faultA (i32.const 8) (local.get $i) (local.get $j) (local.get $got) (local.get $want))))
          (local.set $j (i32.add (local.get $j) (i32.const 1)))
          (br $l)))
        (i32.store (i32.const 24) (local.get $i))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $it)))
      (call $finish))
    (func $streamB (local $i i32) (local $j i32) (local $res i32)
      (local.set $i (i32.const 1))
      (block $done (loop $it
        (br_if $done (i32.gt_u (local.get $i) (global.get $iters)))
        (br_if $done (i32.eqz (call $awaitA (local.get $i))))
        (local.set $j (i32.const 0))
        (block $b (loop $l
          (br_if $b (i32.ge_u (local.get $j) (i32.const 256)))
          (i32.store8 offset=1024 (local.get $j) (i32.add (i32.add (i32.mul (local.get $i) (i32.const 2))
            (i32.mul (local.get $j) (i32.const 6))) (i32.const 1)))
          (local.set $j (i32.add (local.get $j) (i32.const 1)))
          (br $l)))
        (local.set $res (call $swrite (i32.load (i32.const 12)) (i32.const 1024) (i32.const 256)))
        (if (i32.ne (local.get $res) (i32.const 4096))
          (then (call $faultB (i32.const 2) (local.get $i) (local.get $res) (i32.const 4096)) (br $done)))
        (i32.store (i32.const 28) (local.get $i))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $it)))
      (call $finish))

    ;; ---- future: A reads a fresh future<u32> per iteration, carrying the offset of the region B filled
    (func $futureA (param $poll i32) (local $s i32) (local $p i64) (local $r i32) (local $i i32) (local $code i32)
      (local $off i32) (local $j i32) (local $got i32) (local $want i32) (local $dl i64) (local $n i32)
      (local.set $s (call $wsnew))
      (i32.store (i32.const 8) (local.get $s))
      (local.set $i (i32.const 1))
      (block $done (loop $it
        (br_if $done (i32.gt_u (local.get $i) (global.get $iters)))
        (local.set $p (call $fnew))
        (local.set $r (i32.wrap_i64 (local.get $p)))
        (i32.store (i32.const 16) (local.get $r))
        (i32.store (i32.const 12) (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
        (local.set $code (call $fread (local.get $r) (i32.const 2048)))
        (if (i32.ne (local.get $code) (i32.const -1))
          (then (call $faultA (i32.const 1) (local.get $i) (i32.const 0) (local.get $code) (i32.const -1))
                (br $done)))
        (call $wsjoin (local.get $r) (local.get $s))
        (i32.atomic.store (i32.const 4) (local.get $i))
        (if (local.get $poll)
          (then
            (local.set $dl (i64.add (call $now) (i64.const 5000000000)))
            (local.set $n (i32.const 0))
            (block $b (loop $l
              (local.set $code (call $wspoll (local.get $s) (i32.const 128)))
              (br_if $b (local.get $code))
              (local.set $n (i32.add (local.get $n) (i32.const 1)))
              (if (i32.eqz (i32.and (local.get $n) (i32.const 1023)))
                (then (if (i64.gt_u (call $now) (local.get $dl))
                  (then (call $faultA (i32.const 9) (local.get $i) (i32.const 0) (local.get $n) (i32.const 0))
                        (br $done)))))
              (br $l)))
            (i32.store (i32.const 32) (i32.add (i32.load (i32.const 32)) (local.get $n))))
          (else (local.set $code (call $wswait (local.get $s) (i32.const 128)))))
        ;; FUTURE_READ, COMPLETED
        (br_if $done (i32.eqz (call $event (local.get $i) (local.get $code) (i32.const 4) (local.get $r) (i32.const 0))))
        (local.set $off (i32.load (i32.const 2048)))
        (local.set $want (i32.add (i32.const 4096) (i32.mul (i32.and (local.get $i) (i32.const 7)) (i32.const 2048))))
        (if (i32.ne (local.get $off) (local.get $want))
          (then (call $faultA (i32.const 5) (local.get $i) (i32.const 0) (local.get $off) (local.get $want)))
          (else
            (local.set $j (i32.const 0))
            (block $b (loop $l
              (br_if $b (i32.ge_u (local.get $j) (i32.const 256)))
              (local.set $got (i32.load (i32.add (local.get $off) (i32.shl (local.get $j) (i32.const 2)))))
              (local.set $want (i32.or (i32.shl (local.get $i) (i32.const 16)) (local.get $j)))
              (if (i32.ne (local.get $got) (local.get $want))
                (then (call $faultA (i32.const 6) (local.get $i) (local.get $j) (local.get $got) (local.get $want))))
              (local.set $j (i32.add (local.get $j) (i32.const 1)))
              (br $l)))
            (local.set $want (i32.and (i32.add (i32.mul (local.get $i) (i32.const 2)) (i32.const 1)) (i32.const 255)))
            (local.set $j (i32.const 0))
            (block $b (loop $l
              (br_if $b (i32.ge_u (local.get $j) (i32.const 1024)))
              (local.set $got (i32.load8_u offset=1024 (i32.add (local.get $off) (local.get $j))))
              (if (i32.ne (local.get $got) (local.get $want))
                (then (call $faultA (i32.const 7) (local.get $i) (local.get $j) (local.get $got) (local.get $want))))
              (local.set $j (i32.add (local.get $j) (i32.const 1)))
              (br $l)))))
        (call $fdropr (local.get $r))
        (i32.store (i32.const 24) (local.get $i))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $it)))
      (call $finish))
    (func $futureB (local $i i32) (local $j i32) (local $off i32) (local $res i32)
      (local.set $i (i32.const 1))
      (block $done (loop $it
        (br_if $done (i32.gt_u (local.get $i) (global.get $iters)))
        (br_if $done (i32.eqz (call $awaitA (local.get $i))))
        (local.set $off (i32.add (i32.const 4096) (i32.mul (i32.and (local.get $i) (i32.const 7)) (i32.const 2048))))
        (local.set $j (i32.const 0))
        (block $b (loop $l
          (br_if $b (i32.ge_u (local.get $j) (i32.const 256)))
          (i32.store (i32.add (local.get $off) (i32.shl (local.get $j) (i32.const 2)))
            (i32.or (i32.shl (local.get $i) (i32.const 16)) (local.get $j)))
          (local.set $j (i32.add (local.get $j) (i32.const 1)))
          (br $l)))
        (memory.fill (i32.add (local.get $off) (i32.const 1024))
          (i32.add (i32.mul (local.get $i) (i32.const 2)) (i32.const 1)) (i32.const 1024))
        (i32.store (i32.const 1024) (local.get $off))
        (local.set $res (call $fwrite (i32.load (i32.const 12)) (i32.const 1024)))
        (if (local.get $res)
          (then (call $faultB (i32.const 2) (local.get $i) (local.get $res) (i32.const 0)) (br $done)))
        (i32.store (i32.const 28) (local.get $i))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $it)))
      (call $finish))

    (func (export "mstart") (param $k i32)
      (if (i32.eq (local.get $k) (i32.const 1)) (then (call $streamA) (return)))
      (if (i32.eq (local.get $k) (i32.const 2)) (then (call $streamB) (return)))
      (if (i32.eq (local.get $k) (i32.const 3)) (then (call $futureA (i32.const 0)) (return)))
      (if (i32.eq (local.get $k) (i32.const 4)) (then (call $futureB) (return)))
      (if (i32.eq (local.get $k) (i32.const 5)) (then (call $futureA (i32.const 1)) (return)))
      (unreachable))

    ;; a row's end: join its $n agents and record the verdict at 20
    (func $row (param $n i32) (result i32)
      (i32.store (i32.const 20) (call $join (local.get $n)))
      (i32.load (i32.const 20)))

    (func (export "stream") (result i32)
      (call $spawn (i32.const 1)) (call $spawn (i32.const 2)) (call $row (i32.const 2)))
    (func (export "future") (result i32)
      (call $spawn (i32.const 3)) (call $spawn (i32.const 4)) (call $row (i32.const 2)))
    (func (export "future-poll") (result i32)
      (call $spawn (i32.const 5)) (call $spawn (i32.const 4)) (call $row (i32.const 2)))
    (func (export "iters") (result i32) (global.get $iters))
    (func (export "word") (param $off i32) (result i32) (i32.load (local.get $off))))
  (core instance $gi (instantiate $gm (with "" (instance
    (export "mem" (memory $m))
    (export "spawn" (func $spawnl)) (export "now" (func $nowl))
    (export "wsnew" (func $wsnew)) (export "wswait" (func $wswait)) (export "wspoll" (func $wspoll))
    (export "wsjoin" (func $wsjoin))
    (export "snew" (func $snew)) (export "sread" (func $sread)) (export "swrite" (func $swrite))
    (export "fnew" (func $fnew)) (export "fread" (func $fread)) (export "fwrite" (func $fwrite))
    (export "fdropr" (func $fdropr))))))
  (type $at (func async (result u32)))
  (func (export "stream") (type $at) (canon lift (core func $gi "stream")))
  (func (export "future") (type $at) (canon lift (core func $gi "future")))
  (func (export "future-poll") (type $at) (canon lift (core func $gi "future-poll")))
  (func (export "iters") (result u32) (canon lift (core func $gi "iters")))
  (func (export "word") (param "off" u32) (result u32) (canon lift (core func $gi "word"))))
