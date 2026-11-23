;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; spawn-door (#1337, ADR 0131): a component that starts its own agents through the component spawn door,
;; `burroughs:threads/spawn@0.1.0#spawn: func(arg: u32)` (contract §2 T-1). Its canonical memory is SHARED, so it is
;; invalid at the pin (ADR 0129), needs BURROUGHS_CM_SHARED_MEMORY=1, and has no external reader. Assembled with
;; `wasm-tools parse spawn-door.wat -o spawn-door.wasm` (wasm-tools 1.258.0).
;;
;; `wasi:cli/run`'s run spawns agents 1 and 2, whose entry is this core instance's `mstart`. Each agent records the
;; global it starts with (its own T-6 global, so the initializer 0, not run's 0x77), sets its global to its argument,
;; arrives, and spins until run releases it (bounded at 2s on the monotonic clock). run meets both (bounded at 2s),
;; keeps all three executing for 50ms, and releases them. Each agent then computes `chain(k)`, a fixed 64-bit mix
;; of 100000 rounds the test recomputes, writes it and its global into shared memory, and signals through
;; memory.atomic.notify. run joins both through memory.atomic.wait32 (bounded at 10s) and prints one line per agent
;; and one for itself to stdout, through write-via-stream.
;;
;; **What the output says about the agents**: `released=00000001` means the agent saw run's release, which run
;; issued only after it had met both agents while it was itself executing. An agent run inline on run's goroutine
;; never sees it (run is inside the spawn), and prints `released=00000000` after its 2s bound.
;;
;; Layout: 0 arrived, 4 release, 8 done; area(k) = 64*k: +0 chain (i64), +8 entry global, +12 global after the
;; work, +16 saw the release; 512 run's global after the join; 1024 the output; 2048 the text pieces; 4096 an
;; event record.
(component
  (type $types-t (instance
    (type $ec (enum "io" "illegal-byte-sequence" "pipe"))
    (export "error-code" (type (eq $ec)))))
  (import "wasi:cli/types@0.3.0" (instance $types (type $types-t)))
  (alias export $types "error-code" (type $error-code))
  (type $stdout-t (instance
    (alias outer 1 $error-code (type $ec))
    (export "error-code" (type $ec2 (eq $ec)))
    (type $s (stream u8))
    (type $r (result (error $ec2)))
    (type $f (future $r))
    (export "write-via-stream" (func (param "data" $s) (result $f)))))
  (import "wasi:cli/stdout@0.3.0" (instance $stdout (type $stdout-t)))
  (import "wasi:clocks/monotonic-clock@0.3.0" (instance $mc
    (export "now" (func (result u64)))))
  (import "burroughs:threads/spawn@0.1.0" (instance $th
    (export "spawn" (func (param "arg" u32)))))
  (core module $mm
    (memory (export "m") 1 1 shared)
    (data (i32.const 2048) "agent  chain= entry-global= global= released=run global=\n"))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (alias export $th "spawn" (func $spawn))
  (alias export $mc "now" (func $now))
  (alias export $stdout "write-via-stream" (func $wvs))
  (type $st (stream u8))
  ;; the door first, so with the gate off its own refusal is the one heard
  (core func $spawnl (canon lower (func $spawn)))
  (core func $nowl (canon lower (func $now)))
  (core func $wvsl (canon lower (func $wvs)))
  (core func $snew (canon stream.new $st))
  (core func $swrite (canon stream.write $st async (memory $m)))
  (core func $sdropw (canon stream.drop-writable $st))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $m)))
  (core module $gm
    (import "" "mem" (memory 1 1 shared))
    (import "" "spawn" (func $spawn (param i32)))
    (import "" "now" (func $now (result i64)))
    (import "" "wvs" (func $wvs (param i32) (result i32)))
    (import "" "snew" (func $snew (result i64)))
    (import "" "swrite" (func $swrite (param i32 i32 i32) (result i32)))
    (import "" "sdropw" (func $sdropw (param i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    ;; a defined global, per agent under T-6
    (global $g (mut i32) (i32.const 0))
    (global $out (mut i32) (i32.const 1024))

    ;; spin until the word at $p is at least $want, or $ns have passed on the monotonic clock, mostly in guest code
    (func $spinfor (param $p i32) (param $want i32) (param $ns i64) (local $dl i64) (local $i i32)
      (local.set $dl (i64.add (call $now) (local.get $ns)))
      (block $b (loop $l
        (br_if $b (i32.ge_u (i32.atomic.load (local.get $p)) (local.get $want)))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (if (i32.eqz (i32.and (local.get $i) (i32.const 4095)))
          (then (br_if $b (i64.gt_u (call $now) (local.get $dl)))))
        (br $l))))

    (func (export "mstart") (param $k i32) (local $a i32) (local $i i32) (local $x i64)
      (local.set $a (i32.mul (local.get $k) (i32.const 64)))
      (i32.store offset=8 (local.get $a) (global.get $g))
      (global.set $g (local.get $k))
      (drop (i32.atomic.rmw.add (i32.const 0) (i32.const 1)))
      (call $spinfor (i32.const 4) (i32.const 1) (i64.const 2000000000))
      (i32.store offset=16 (local.get $a) (i32.atomic.load (i32.const 4)))
      (local.set $x (i64.add (i64.mul (i64.extend_i32_u (local.get $k)) (i64.const 0x9E3779B97F4A7C15)) (i64.const 1)))
      (block $b (loop $l
        (br_if $b (i32.ge_u (local.get $i) (i32.const 100000)))
        (local.set $x (i64.xor (local.get $x) (i64.shr_u (local.get $x) (i64.const 33))))
        (local.set $x (i64.mul (local.get $x) (i64.const 0xFF51AFD7ED558CCD)))
        (local.set $x (i64.xor (local.get $x) (i64.shr_u (local.get $x) (i64.const 29))))
        (local.set $x (i64.add (local.get $x) (i64.extend_i32_u (local.get $i))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $l)))
      (i64.store (local.get $a) (local.get $x))
      (i32.store offset=12 (local.get $a) (global.get $g))
      (drop (i32.atomic.rmw.add (i32.const 8) (i32.const 1)))
      (drop (memory.atomic.notify (i32.const 8) (i32.const -1))))

    ;; append the $n bytes at $p to the output
    (func $lit (param $p i32) (param $n i32)
      (memory.copy (global.get $out) (local.get $p) (local.get $n))
      (global.set $out (i32.add (global.get $out) (local.get $n))))

    ;; append $v's low $digits nibbles, most significant first, in lower-case hex
    (func $hex (param $v i64) (param $digits i32) (local $d i32)
      (block $b (loop $l
        (br_if $b (i32.eqz (local.get $digits)))
        (local.set $digits (i32.sub (local.get $digits) (i32.const 1)))
        (local.set $d (i32.wrap_i64 (i64.and (i64.shr_u (local.get $v)
          (i64.extend_i32_u (i32.mul (local.get $digits) (i32.const 4)))) (i64.const 15))))
        (i32.store8 (global.get $out) (i32.add (local.get $d)
          (select (i32.const 48) (i32.const 87) (i32.lt_u (local.get $d) (i32.const 10)))))
        (global.set $out (i32.add (global.get $out) (i32.const 1)))
        (br $l))))

    (func $line (param $k i32) (local $a i32)
      (local.set $a (i32.mul (local.get $k) (i32.const 64)))
      (call $lit (i32.const 2048) (i32.const 6))
      (call $hex (i64.extend_i32_u (local.get $k)) (i32.const 1))
      (call $lit (i32.const 2054) (i32.const 7))
      (call $hex (i64.load (local.get $a)) (i32.const 16))
      (call $lit (i32.const 2061) (i32.const 14))
      (call $hex (i64.load32_u offset=8 (local.get $a)) (i32.const 8))
      (call $lit (i32.const 2075) (i32.const 8))
      (call $hex (i64.load32_u offset=12 (local.get $a)) (i32.const 8))
      (call $lit (i32.const 2083) (i32.const 10))
      (call $hex (i64.load32_u offset=16 (local.get $a)) (i32.const 8))
      (call $lit (i32.const 2104) (i32.const 1)))

    ;; write memory[1024, $out) to stdout through write-via-stream
    (func $print (local $p i64) (local $w i32) (local $s i32) (local $done i32) (local $len i32) (local $r i32)
      (local.set $len (i32.sub (global.get $out) (i32.const 1024)))
      (local.set $p (call $snew))
      (local.set $w (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
      (drop (call $wvs (i32.wrap_i64 (local.get $p))))
      (local.set $s (call $wsnew))
      (call $wsjoin (local.get $w) (local.get $s))
      (block $b (loop $l
        (br_if $b (i32.ge_u (local.get $done) (local.get $len)))
        (local.set $r (call $swrite (local.get $w) (i32.add (i32.const 1024) (local.get $done))
          (i32.sub (local.get $len) (local.get $done))))
        (if (i32.eq (local.get $r) (i32.const -1))
          (then
            (drop (call $wswait (local.get $s) (i32.const 4096)))
            (local.set $r (i32.load (i32.const 4100)))))
        (local.set $done (i32.add (local.get $done) (i32.shr_u (local.get $r) (i32.const 4))))
        (br_if $b (i32.and (local.get $r) (i32.const 15)))
        (br $l)))
      (call $sdropw (local.get $w)))

    (func (export "run") (result i32) (local $n i32)
      (global.set $g (i32.const 0x77))
      (call $spawn (i32.const 1))
      (call $spawn (i32.const 2))
      ;; meet both agents while executing, keep all three executing for 50ms, then release them
      (call $spinfor (i32.const 0) (i32.const 2) (i64.const 2000000000))
      (call $spinfor (i32.const 0) (i32.const 3) (i64.const 50000000))
      (i32.atomic.store (i32.const 4) (i32.const 1))
      (block $b (loop $l
        (local.set $n (i32.atomic.load (i32.const 8)))
        (br_if $b (i32.ge_u (local.get $n) (i32.const 2)))
        (br_if $b (i32.eq (i32.const 2)
          (memory.atomic.wait32 (i32.const 8) (local.get $n) (i64.const 10000000000))))
        (br $l)))
      (i32.store (i32.const 512) (global.get $g))
      (call $line (i32.const 1))
      (call $line (i32.const 2))
      (call $lit (i32.const 2093) (i32.const 11))
      (call $hex (i64.load32_u (i32.const 512)) (i32.const 8))
      (call $lit (i32.const 2104) (i32.const 1))
      (call $print)
      (i32.ne (i32.atomic.load (i32.const 8)) (i32.const 2))))
  (core instance $gi (instantiate $gm (with "" (instance
    (export "mem" (memory $m))
    (export "spawn" (func $spawnl)) (export "now" (func $nowl)) (export "wvs" (func $wvsl))
    (export "snew" (func $snew)) (export "swrite" (func $swrite)) (export "sdropw" (func $sdropw))
    (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "wswait" (func $wswait))))))
  (type $rt (result))
  (func $run async (result $rt) (canon lift (core func $gi "run")))
  (instance $ri (export "run" (func $run)))
  (export "wasi:cli/run@0.3.0" (instance $ri)))
