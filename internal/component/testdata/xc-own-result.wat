;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; xc-own-result: #1281, the return direction of xc-own-param. A parent SYNC-lowers its child's SYNC lift
;; `give: func() -> own<output-stream>`. The child takes a stderr handle (child index 1), then `give` takes a
;; stdout handle (child index 2) and returns it. The parent holds two stderr handles first (parent indices 1 and
;; 2), so the child's index names the parent's second stderr handle. The parent writes "parent\n" through the
;; handle it was given and drops it, then prints "<a> <b> <x>\n" on a fresh stdout handle: its two stderr indices
;; and the index it was given.
;;
;; The reference moves the own back (def:lift_own in the child, def:lower_own in the parent), so the parent is
;; given a fresh index and its line goes to stdout. Passing the word through gives the parent index 2, its own
;; stderr handle, so the line goes to stderr, and the parent's last drop of that handle traps.
;; Assembled with `wasm-tools parse xc-own-result.wat -o xc-own-result.wasm` (wasm-tools 1.258.0); the readings
;; are in xc-handles.reading.
(component
  (type $error-t (instance (export "error" (type (sub resource)))))
  (import "wasi:io/error@0.2.3" (instance $error (type $error-t)))
  (alias export $error "error" (type $error-res))
  (type $streams-t (instance
    (export "output-stream" (type (sub resource)))
    (alias outer 1 $error-res (type))
    (export "error" (type (eq 1)))
    (type (own 2))
    (type (variant (case "last-operation-failed" 3) (case "closed")))
    (export "stream-error" (type (eq 4)))
    (type (borrow 0))
    (type (list u8))
    (type (result (error 5)))
    (type (func (param "self" 6) (param "contents" 7) (result 8)))
    (export "[method]output-stream.blocking-write-and-flush" (func (type 9)))))
  (import "wasi:io/streams@0.2.3" (instance $streams (type $streams-t)))
  (alias export $streams "output-stream" (type $output-stream))
  (type $stdout-t (instance
    (alias outer 1 $output-stream (type))
    (export "output-stream" (type (eq 0)))
    (type (own 1))
    (type (func (result 2)))
    (export "get-stdout" (func (type 3)))))
  (import "wasi:cli/stdout@0.2.3" (instance $stdout (type $stdout-t)))
  (type $stderr-t (instance
    (alias outer 1 $output-stream (type))
    (export "output-stream" (type (eq 0)))
    (type (own 1))
    (type (func (result 2)))
    (export "get-stderr" (func (type 3)))))
  (import "wasi:cli/stderr@0.2.3" (instance $stderr (type $stderr-t)))

  (component $child
    (type $error-t (instance (export "error" (type (sub resource)))))
    (import "error" (instance $error (type $error-t)))
    (alias export $error "error" (type $error-res))
    (type $streams-t (instance
      (export "output-stream" (type (sub resource)))
      (alias outer 1 $error-res (type))
      (export "error" (type (eq 1)))
      (type (own 2))
      (type (variant (case "last-operation-failed" 3) (case "closed")))
      (export "stream-error" (type (eq 4)))
      (type (borrow 0))
      (type (list u8))
      (type (result (error 5)))
      (type (func (param "self" 6) (param "contents" 7) (result 8)))
      (export "[method]output-stream.blocking-write-and-flush" (func (type 9)))))
    (import "streams" (instance $streams (type $streams-t)))
    (alias export $streams "output-stream" (type $output-stream))
    (type $stderr-t (instance
      (alias outer 1 $output-stream (type))
      (export "output-stream" (type (eq 0)))
      (type (own 1))
      (type (func (result 2)))
      (export "get-stderr" (func (type 3)))))
    (import "stderr" (instance $stderr (type $stderr-t)))
    (type $stdout-t (instance
      (alias outer 1 $output-stream (type))
      (export "output-stream" (type (eq 0)))
      (type (own 1))
      (type (func (result 2)))
      (export "get-stdout" (func (type 3)))))
    (import "stdout" (instance $stdout (type $stdout-t)))
    (alias export $stdout "get-stdout" (func $get-stdout))
    (alias export $stderr "get-stderr" (func $get-stderr))
    (core func $get-stdout-low (canon lower (func $get-stdout)))
    (core func $get-stderr-low (canon lower (func $get-stderr)))
    (core module $m
      (import "host" "get-stderr" (func $get-stderr (result i32)))
      (import "host" "get-stdout" (func $get-stdout (result i32)))
      (global $mine (mut i32) (i32.const 0))
      (func (export "setup") (result i32) (global.set $mine (call $get-stderr)) (global.get $mine))
      ;; give returns a fresh stdout handle, the second entry in the child's table.
      (func (export "give") (result i32) (call $get-stdout)))
    (core instance $i (instantiate $m (with "host" (instance
      (export "get-stderr" (func $get-stderr-low)) (export "get-stdout" (func $get-stdout-low))))))
    (func (export "setup") (result u32) (canon lift (core func $i "setup")))
    (type $give-t (func (result (own $output-stream))))
    (func (export "give") (type $give-t) (canon lift (core func $i "give"))))

  (instance $c (instantiate $child
    (with "error" (instance $error)) (with "streams" (instance $streams)) (with "stderr" (instance $stderr))
    (with "stdout" (instance $stdout))))
  (alias export $c "setup" (func $setup))
  (alias export $c "give" (func $give))
  (alias export $stdout "get-stdout" (func $get-stdout))
  (alias export $stderr "get-stderr" (func $get-stderr))
  (alias export $streams "[method]output-stream.blocking-write-and-flush" (func $write))
  (core module $mem (memory (export "memory") 1))
  (core instance $memi (instantiate $mem))
  (alias core export $memi "memory" (core memory $memory))
  (core func $setup-low (canon lower (func $setup)))
  (core func $give-low (canon lower (func $give)))
  (core func $get-stdout-low (canon lower (func $get-stdout)))
  (core func $get-stderr-low (canon lower (func $get-stderr)))
  (core func $write-low (canon lower (func $write) (memory $memory)))
  (core func $drop-low (canon resource.drop $output-stream))
  (core module $m
    (import "host" "memory" (memory 1))
    (import "host" "setup" (func $setup (result i32)))
    (import "host" "give" (func $give (result i32)))
    (import "host" "get-stdout" (func $get-stdout (result i32)))
    (import "host" "get-stderr" (func $get-stderr (result i32)))
    (import "host" "write" (func $write (param i32 i32 i32 i32)))
    (import "host" "drop" (func $drop (param i32)))
    (func (export "run") (result i32)
      (local $a i32) (local $b i32) (local $x i32) (local $q i32)
      (local.set $a (call $get-stderr))
      (local.set $b (call $get-stderr))
      (drop (call $setup))
      (local.set $x (call $give))
      ;; "parent\n" through the handle the child gave
      (i32.store8 (i32.const 0) (i32.const 112)) (i32.store8 (i32.const 1) (i32.const 97))
      (i32.store8 (i32.const 2) (i32.const 114)) (i32.store8 (i32.const 3) (i32.const 101))
      (i32.store8 (i32.const 4) (i32.const 110)) (i32.store8 (i32.const 5) (i32.const 116))
      (i32.store8 (i32.const 6) (i32.const 10))
      (call $write (local.get $x) (i32.const 0) (i32.const 7) (i32.const 16))
      (call $drop (local.get $x))
      (local.set $q (call $get-stdout))
      (i32.store8 (i32.const 0) (i32.add (i32.const 48) (local.get $a)))
      (i32.store8 (i32.const 1) (i32.const 32))
      (i32.store8 (i32.const 2) (i32.add (i32.const 48) (local.get $b)))
      (i32.store8 (i32.const 3) (i32.const 32))
      (i32.store8 (i32.const 4) (i32.add (i32.const 48) (local.get $x)))
      (i32.store8 (i32.const 5) (i32.const 10))
      (call $write (local.get $q) (i32.const 0) (i32.const 6) (i32.const 16))
      (call $drop (local.get $q))
      (call $drop (local.get $a))
      (call $drop (local.get $b))
      (i32.const 0)))
  (core instance $i (instantiate $m (with "host" (instance
    (export "memory" (memory $memory)) (export "setup" (func $setup-low)) (export "give" (func $give-low))
    (export "get-stdout" (func $get-stdout-low)) (export "get-stderr" (func $get-stderr-low))
    (export "write" (func $write-low)) (export "drop" (func $drop-low))))))
  (type $run-t (func (result (result))))
  (func $run (type $run-t) (canon lift (core func $i "run")))
  (instance $run-inst (export "run" (func $run)))
  (export "wasi:cli/run@0.2.3" (instance $run-inst)))
