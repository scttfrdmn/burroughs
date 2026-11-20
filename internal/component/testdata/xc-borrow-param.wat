;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; xc-borrow-param: #1281. xc-own-param with the parameter a `borrow<output-stream>`: the parent lends its stdout
;; handle (parent index 1) to the child, whose index 1 is its own stderr handle. `take` writes "child\n" through
;; the handle it was given and drops it, as a callee must drop a borrow before it returns. The parent keeps its
;; handle, so it prints "<child's stderr index> <parent's stdout index> <index the child was handed>\n" through
;; it.
;;
;; The reference lends the handle for the subtask (def:lift_borrow) and gives the child a borrow handle of its own
;; (def:lower_borrow), so the child's line goes to stdout. Passing the word through hands the child index 1, its
;; own stderr handle, so the line goes to stderr and the child's drop destroys its own own. Burroughs models no
;; borrow scope, so a cross-component borrow is refused by name (#1281).
;; Assembled with `wasm-tools parse xc-borrow-param.wat -o xc-borrow-param.wasm` (wasm-tools 1.258.0); the
;; readings are in xc-handles.reading.
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
    (alias export $stderr "get-stderr" (func $get-stderr))
    (alias export $streams "[method]output-stream.blocking-write-and-flush" (func $write))
    (core module $mem (memory (export "memory") 1))
    (core instance $memi (instantiate $mem))
    (alias core export $memi "memory" (core memory $memory))
    (core func $get-stderr-low (canon lower (func $get-stderr)))
    (core func $write-low (canon lower (func $write) (memory $memory)))
    (core func $drop-low (canon resource.drop $output-stream))
    (core module $m
      (import "host" "memory" (memory 1))
      (import "host" "get-stderr" (func $get-stderr (result i32)))
      (import "host" "write" (func $write (param i32 i32 i32 i32)))
      (import "host" "drop" (func $drop (param i32)))
      (global $mine (mut i32) (i32.const 0))
      (func (export "setup") (result i32) (global.set $mine (call $get-stderr)) (global.get $mine))
      ;; take writes "child\n" through the handle it was lent, then drops it.
      (func (export "take") (param $h i32) (result i32)
        (i32.store8 (i32.const 0) (i32.const 99)) (i32.store8 (i32.const 1) (i32.const 104))
        (i32.store8 (i32.const 2) (i32.const 105)) (i32.store8 (i32.const 3) (i32.const 108))
        (i32.store8 (i32.const 4) (i32.const 100)) (i32.store8 (i32.const 5) (i32.const 10))
        (call $write (local.get $h) (i32.const 0) (i32.const 6) (i32.const 16))
        (call $drop (local.get $h))
        (local.get $h)))
    (core instance $i (instantiate $m (with "host" (instance
      (export "memory" (memory $memory)) (export "get-stderr" (func $get-stderr-low))
      (export "write" (func $write-low)) (export "drop" (func $drop-low))))))
    (func (export "setup") (result u32) (canon lift (core func $i "setup")))
    (type $take-t (func (param "s" (borrow $output-stream)) (result u32)))
    (func (export "take") (type $take-t) (canon lift (core func $i "take"))))

  (instance $c (instantiate $child
    (with "error" (instance $error)) (with "streams" (instance $streams)) (with "stderr" (instance $stderr))))
  (alias export $c "setup" (func $setup))
  (alias export $c "take" (func $take))
  (alias export $stdout "get-stdout" (func $get-stdout))
  (alias export $streams "[method]output-stream.blocking-write-and-flush" (func $write))
  (core module $mem (memory (export "memory") 1))
  (core instance $memi (instantiate $mem))
  (alias core export $memi "memory" (core memory $memory))
  (core func $setup-low (canon lower (func $setup)))
  (core func $take-low (canon lower (func $take)))
  (core func $get-stdout-low (canon lower (func $get-stdout)))
  (core func $write-low (canon lower (func $write) (memory $memory)))
  (core func $drop-low (canon resource.drop $output-stream))
  (core module $m
    (import "host" "memory" (memory 1))
    (import "host" "setup" (func $setup (result i32)))
    (import "host" "take" (func $take (param i32) (result i32)))
    (import "host" "get-stdout" (func $get-stdout (result i32)))
    (import "host" "write" (func $write (param i32 i32 i32 i32)))
    (import "host" "drop" (func $drop (param i32)))
    (func (export "run") (result i32)
      (local $mine i32) (local $p i32) (local $x i32)
      (local.set $mine (call $setup))
      (local.set $p (call $get-stdout))
      (local.set $x (call $take (local.get $p)))
      (i32.store8 (i32.const 0) (i32.add (i32.const 48) (local.get $mine)))
      (i32.store8 (i32.const 1) (i32.const 32))
      (i32.store8 (i32.const 2) (i32.add (i32.const 48) (local.get $p)))
      (i32.store8 (i32.const 3) (i32.const 32))
      (i32.store8 (i32.const 4) (i32.add (i32.const 48) (local.get $x)))
      (i32.store8 (i32.const 5) (i32.const 10))
      (call $write (local.get $p) (i32.const 0) (i32.const 6) (i32.const 16))
      (call $drop (local.get $p))
      (i32.const 0)))
  (core instance $i (instantiate $m (with "host" (instance
    (export "memory" (memory $memory)) (export "setup" (func $setup-low)) (export "take" (func $take-low))
    (export "get-stdout" (func $get-stdout-low)) (export "write" (func $write-low)) (export "drop" (func $drop-low))))))
  (type $run-t (func (result (result))))
  (func $run (type $run-t) (canon lift (core func $i "run")))
  (instance $run-inst (export "run" (func $run)))
  (export "wasi:cli/run@0.2.3" (instance $run-inst)))
