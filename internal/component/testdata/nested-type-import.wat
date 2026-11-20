;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; nested-type-import: #1257. nested-used-resource's guest, with the child taking `output-stream` as a type import
;; rather than from an instance: `(import "os" (type (sub resource)))`, which the parent binds with
;; `(with "os" (type $output-stream))`. So the child's types name the resource `os`, where the host serves
;; `wasi:io/streams@0.2.3::output-stream`. It is valid and wasmtime 49.0.3 runs it, so the child's lower of
;; get-stdout must read `os` as the parent's type it is bound to.
;; Assembled with `wasm-tools parse nested-type-import.wat -o nested-type-import.wasm` (wasm-tools 1.258.0).
(component
  (type $streams-t (instance
    (export "output-stream" (type (sub resource)))))
  (import "wasi:io/streams@0.2.3" (instance $streams (type $streams-t)))
  (alias export $streams "output-stream" (type $output-stream))
  (type $stdout-t (instance
    (alias outer 1 $output-stream (type))
    (export "output-stream" (type (eq 0)))
    (type (own 1))
    (type (func (result 2)))
    (export "get-stdout" (func (type 3)))))
  (import "wasi:cli/stdout@0.2.3" (instance $stdout (type $stdout-t)))
  (component $child
    (import "os" (type $cos (sub resource)))
    (import "stdout" (instance $co
      (alias outer 1 $cos (type))
      (export "output-stream" (type (eq 0)))
      (type (own 1))
      (type (func (result 2)))
      (export "get-stdout" (func (type 3)))))
    (alias export $co "get-stdout" (func $gs))
    (core func $gs-low (canon lower (func $gs)))
    (core module $m
      (import "host" "get-stdout" (func (result i32)))
      (func (export "seven") (result i32) i32.const 7))
    (core instance $i (instantiate $m (with "host" (instance (export "get-stdout" (func $gs-low))))))
    (func (export "seven") (result u32) (canon lift (core func $i "seven"))))
  (instance $c (instantiate $child (with "os" (type $output-stream)) (with "stdout" (instance $stdout))))
  (alias export $c "seven" (func $seven))
  (export "seven" (func $seven)))
