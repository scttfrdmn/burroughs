;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; host-resource-reuse: #1231, ADR 0117's differential arm. A `wasi:cli/run` guest that prints the handle
;; index `get-stdout` returns, three times: once, then again after dropping the first, then a third time
;; while the second is still held. The reference reuses a freed slot first (def:Table.add), so it prints
;; 1, 1, 2. The index is printed as one ASCII digit, so the guest is valid only while the indices stay below
;; 10, which three calls do.
;; Assembled with `wasm-tools parse host-resource-reuse.wat -o host-resource-reuse.wasm` (wasm-tools 1.258.0);
;; the readings are in host-resource-reuse.reading.
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
  (alias export $stdout "get-stdout" (func $get-stdout))
  (alias export $streams "[method]output-stream.blocking-write-and-flush" (func $write))

  (core module $mem (memory (export "memory") 1))
  (core instance $memi (instantiate $mem))
  (alias core export $memi "memory" (core memory $memory))

  (core func $get-stdout-low (canon lower (func $get-stdout)))
  (core func $write-low (canon lower (func $write) (memory $memory)))
  (core func $drop-low (canon resource.drop $output-stream))
  (core instance $host
    (export "memory" (memory $memory))
    (export "get-stdout" (func $get-stdout-low))
    (export "write" (func $write-low))
    (export "drop" (func $drop-low)))

  (core module $m
    (import "host" "memory" (memory 1))
    (import "host" "get-stdout" (func $get-stdout (result i32)))
    (import "host" "write" (func $write (param i32 i32 i32 i32)))
    (import "host" "drop" (func $drop (param i32)))
    ;; print writes "<digit>\n" for handle h through h itself: the bytes at 0, the result at 16.
    (func $print (param $h i32)
      (i32.store8 (i32.const 0) (i32.add (i32.const 48) (local.get $h)))
      (i32.store8 (i32.const 1) (i32.const 10))
      (call $write (local.get $h) (i32.const 0) (i32.const 2) (i32.const 16)))
    (func (export "run") (result i32)
      (local $a i32) (local $b i32) (local $c i32)
      (local.set $a (call $get-stdout))
      (call $print (local.get $a))
      (call $drop (local.get $a))
      (local.set $b (call $get-stdout))
      (call $print (local.get $b))
      (local.set $c (call $get-stdout))
      (call $print (local.get $c))
      (call $drop (local.get $c))
      (call $drop (local.get $b))
      (i32.const 0)))
  (core instance $i (instantiate $m (with "host" (instance $host))))
  (type $run-t (func (result (result))))
  (func $run (type $run-t) (canon lift (core func $i "run")))
  (instance $run-inst (export "run" (func $run)))
  (export "wasi:cli/run@0.2.3" (instance $run-inst)))
