;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; shmem-canon-roundtrip: gate:cm-shared-memory's witness fixture (#1335, ADR 0129). It is
;; shmem-canon-roundtrip-unshared.wat with the word `shared` added to its three memory declarations (the
;; child's, the parent's, and the parent's import of it), and nothing else, so every canon in it names a SHARED
;; canonical memory: get-arguments' lower, shout's lower and lift, and the write's lower.
;;
;; This component is invalid at the pin, whose canonopt rule wants the memory to be a subtype of (memory 0), and
;; Burroughs admits it only as a Burroughs-only extension behind the gate. No other engine can read it:
;;   wasm-tools 1.258.0 validate --features all  ->  "mismatch in the shared flag for memories (at offset 0x14b)"
;;   wasmtime 49.0.3 run -W threads=y,shared-memory=y  ->  the same refusal, exit 1
;; So its oracle is its twin's wasmtime reading, byte for byte (shmem-canon-roundtrip.reading).
;; Assembled with `wasm-tools parse shmem-canon-roundtrip.wat -o shmem-canon-roundtrip.wasm` (wasm-tools 1.258.0;
;; parse does not validate).
(component
  (component $child
    (core module $cm
      (memory (export "memory") 1 1 shared)
      (global $bump (mut i32) (i32.const 1024))
      (func (export "realloc") (param i32 i32 i32 i32) (result i32)
        (local $p i32)
        (local.set $p (i32.and (i32.add (global.get $bump) (i32.sub (local.get 2) (i32.const 1)))
                               (i32.sub (i32.const 0) (local.get 2))))
        (global.set $bump (i32.add (local.get $p) (local.get 3)))
        (local.get $p))
      (func (export "shout") (param $ptr i32) (param $len i32) (result i32)
        (local $i i32) (local $c i32)
        (block $done (loop $next
          (br_if $done (i32.ge_u (local.get $i) (local.get $len)))
          (local.set $c (i32.load8_u (i32.add (local.get $ptr) (local.get $i))))
          (if (i32.and (i32.ge_u (local.get $c) (i32.const 97)) (i32.le_u (local.get $c) (i32.const 122)))
            (then (i32.store8 (i32.add (local.get $ptr) (local.get $i)) (i32.sub (local.get $c) (i32.const 32)))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $next)))
        (i32.store (i32.const 16) (local.get $ptr))
        (i32.store (i32.const 20) (local.get $len))
        (i32.const 16)))
    (core instance $ci (instantiate $cm))
    (alias core export $ci "memory" (core memory $cmem))
    (alias core export $ci "realloc" (core func $crealloc))
    (alias core export $ci "shout" (core func $cshout))
    (func (export "shout") (param "s" string) (result string)
      (canon lift (core func $cshout) (memory $cmem) (realloc $crealloc))))
  (instance $c (instantiate $child))
  (alias export $c "shout" (func $shout))

  (type $env-t (instance
    (type (list string))
    (type (func (result 0)))
    (export "get-arguments" (func (type 1)))))
  (import "wasi:cli/environment@0.2.3" (instance $env (type $env-t)))
  (alias export $env "get-arguments" (func $get-arguments))
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

  (core module $libc
    (memory (export "memory") 1 1 shared)
    (global $bump (mut i32) (i32.const 1024))
    (func (export "realloc") (param i32 i32 i32 i32) (result i32)
      (local $p i32)
      (local.set $p (i32.and (i32.add (global.get $bump) (i32.sub (local.get 2) (i32.const 1)))
                             (i32.sub (i32.const 0) (local.get 2))))
      (global.set $bump (i32.add (local.get $p) (local.get 3)))
      (local.get $p)))
  (core instance $libci (instantiate $libc))
  (alias core export $libci "memory" (core memory $memory))
  (alias core export $libci "realloc" (core func $realloc))

  (core func $get-arguments-low (canon lower (func $get-arguments) (memory $memory) (realloc $realloc)))
  (core func $shout-low (canon lower (func $shout) (memory $memory) (realloc $realloc)))
  (core func $get-stdout-low (canon lower (func $get-stdout)))
  (core func $write-low (canon lower (func $write) (memory $memory)))
  (core func $drop-low (canon resource.drop $output-stream))
  (core instance $host
    (export "memory" (memory $memory))
    (export "get-arguments" (func $get-arguments-low))
    (export "shout" (func $shout-low))
    (export "get-stdout" (func $get-stdout-low))
    (export "write" (func $write-low))
    (export "drop" (func $drop-low)))

  (core module $m
    (import "host" "memory" (memory 1 1 shared))
    (import "host" "get-arguments" (func $get-arguments (param i32)))
    (import "host" "shout" (func $shout (param i32 i32 i32)))
    (import "host" "get-stdout" (func $get-stdout (result i32)))
    (import "host" "write" (func $write (param i32 i32 i32 i32)))
    (import "host" "drop" (func $drop (param i32)))
    ;; Scratch at low addresses: the argument list's (ptr, len) at 32, shout's result at 48, write's at 64, the
    ;; newline byte at 80. Both reallocs hand out memory from 1024 up.
    (func (export "run") (result i32)
      (local $out i32) (local $base i32) (local $n i32) (local $i i32) (local $e i32)
      (call $get-arguments (i32.const 32))
      (local.set $base (i32.load (i32.const 32)))
      (local.set $n (i32.load (i32.const 36)))
      (local.set $out (call $get-stdout))
      (i32.store8 (i32.const 80) (i32.const 10))
      (local.set $i (i32.const 1))
      (block $done (loop $next
        (br_if $done (i32.ge_u (local.get $i) (local.get $n)))
        (local.set $e (i32.add (local.get $base) (i32.shl (local.get $i) (i32.const 3))))
        (call $shout (i32.load (local.get $e)) (i32.load offset=4 (local.get $e)) (i32.const 48))
        (call $write (local.get $out) (i32.load (i32.const 48)) (i32.load (i32.const 52)) (i32.const 64))
        (call $write (local.get $out) (i32.const 80) (i32.const 1) (i32.const 64))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $next)))
      (call $drop (local.get $out))
      (i32.const 0)))
  (core instance $i (instantiate $m (with "host" (instance $host))))
  (type $run-t (func (result (result))))
  (func $run (type $run-t) (canon lift (core func $i "run")))
  (instance $run-inst (export "run" (func $run)))
  (export "wasi:cli/run@0.2.3" (instance $run-inst))
  ;; The child's shout, re-exported, so a host call lowers a string into the child's memory and lifts one back.
  (export "shout" (func $shout)))
