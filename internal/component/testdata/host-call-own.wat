;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; host-call-own: E2-in-a's host call (#1384, ADR 0136). `handle` is an async callback export that takes an
;; own<thing> of the host's test resource (hostresource_test.go's thingHost) and returns result<own<thing>, u32>:
;; `wasi:http/handler#handle`'s shape, over a resource the tests implement. It borrows the thing (`poke`, recording
;; its id), calls task.return(ok(thing)), handing the own back, and then parks: it starts reading a future<u32>
;; nobody has written and WAITs on it. Only the sync export `release` writes that future, and only the callback
;; that its event reaches sets `exited` and EXITs. So between the host's answer and `release`, the task is parked
;; at a place the tests can name, with its result already returned.
;;
;; `make` is an async export taking nothing and returning result<own<thing>, u32>: it marks `made`, mints a thing
;; through the host's `make` import, and returns it, EXITing in its first call. Its own is minted by the guest, not
;; passed in, and an embedder's CallValues must refuse it before `made` is marked.
;;
;; Memory: [0] the id poke read, [4] exited, [8] the future's writable end, [12] its readable end, [16] the read
;; buffer, [20] the write buffer, [24] made.
;; Assembled with `wasm-tools parse host-call-own.wat -o host-call-own.wasm` (wasm-tools 1.258.0).
(component
  (type $things (instance
    (export "thing" (type (sub resource)))
    (export "make" (func (result (own 0))))
    (export "[method]thing.poke" (func (param "self" (borrow 0)) (result u32)))))
  (import "test:res/things@0.1.0" (instance $t (type $things)))
  (alias export $t "thing" (type $thing))
  (alias export $t "make" (func $make))
  (alias export $t "[method]thing.poke" (func $poke))
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core func $make-low (canon lower (func $make)))
  (core func $poke-low (canon lower (func $poke)))
  (type $fut (future u32))
  (core func $fnew (canon future.new $fut))
  (core func $fread (canon future.read $fut (memory $cm) async))
  (core func $fwrite (canon future.write $fut (memory $cm) async))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (type $res (result (own $thing) (error u32)))
  (core func $ret (canon task.return (result $res)))
  (core module $m
    (import "" "mem" (memory 1 1))
    (import "" "make" (func $make (result i32)))
    (import "" "poke" (func $poke (param i32) (result i32)))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "ret" (func $ret (param i32 i32)))
    (func (export "handle") (param $t i32) (result i32) (local $f i64) (local $s i32)
      (i32.store (i32.const 0) (call $poke (local.get $t)))
      (call $ret (i32.const 0) (local.get $t)) ;; ok(thing): the own goes back to the caller
      (local.set $f (call $fnew))
      (i32.store (i32.const 12) (i32.wrap_i64 (local.get $f)))
      (i32.store (i32.const 8) (i32.wrap_i64 (i64.shr_u (local.get $f) (i64.const 32))))
      (drop (call $fread (i32.load (i32.const 12)) (i32.const 16))) ;; BLOCKED: nothing has written it
      (local.set $s (call $wsnew))
      (call $wsjoin (i32.load (i32.const 12)) (local.get $s))
      (i32.or (i32.shl (local.get $s) (i32.const 4)) (i32.const 2))) ;; WAIT, until release writes the future
    (func (export "cb") (param i32 i32 i32) (result i32)
      (i32.store (i32.const 4) (i32.const 1))
      (i32.const 0)) ;; EXIT
    (func (export "release") (result i32)
      (i32.store (i32.const 20) (i32.const 9))
      (call $fwrite (i32.load (i32.const 8)) (i32.const 20)))
    (func (export "make") (result i32)
      (i32.store (i32.const 24) (i32.const 1))
      (call $ret (i32.const 0) (call $make)) ;; ok(a thing the host's import minted)
      (i32.const 0)) ;; EXIT
    (func (export "made") (result i32) (i32.load (i32.const 24)))
    (func (export "exited") (result i32) (i32.load (i32.const 4)))
    (func (export "id") (result i32) (i32.load (i32.const 0))))
  (core instance $mi (instantiate $m
    (with "" (instance (export "mem" (memory $cm)) (export "make" (func $make-low)) (export "poke" (func $poke-low))
                       (export "fnew" (func $fnew))
                       (export "fread" (func $fread)) (export "fwrite" (func $fwrite)) (export "wsnew" (func $wsnew))
                       (export "wsjoin" (func $wsjoin)) (export "ret" (func $ret))))))
  (type $ht (func async (param "t" (own $thing)) (result $res)))
  (func (export "handle") (type $ht) (canon lift (core func $mi "handle") async (callback (func $mi "cb"))))
  (type $mt (func async (result $res)))
  (func (export "make") (type $mt) (canon lift (core func $mi "make") async (callback (func $mi "cb"))))
  (func (export "made") (result u32) (canon lift (core func $mi "made")))
  (func (export "release") (result u32) (canon lift (core func $mi "release")))
  (func (export "exited") (result u32) (canon lift (core func $mi "exited")))
  (func (export "id") (result u32) (canon lift (core func $mi "id"))))
