;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; nested-used-resource: #1257. A nested component that lowers the parent's `wasi:cli/stdout@0.2.3` get-stdout,
;; whose result is an own of `output-stream`, a type `use`d from another import, as wit-component emits it: an alias
;; export of the `streams` import, pulled into the `stdout` instance type by an outer alias. The child names its
;; imports `streams` and `stdout`, so its types name the resource `streams::output-stream`, where the host serves
;; `wasi:io/streams@0.2.3::output-stream`. It is valid and wasmtime 49.0.3 runs it, so a nested lower's checks must
;; read its identities as the instances its imports are bound to.
;; Assembled with `wasm-tools parse nested-used-resource.wat -o nested-used-resource.wasm` (wasm-tools 1.258.0).
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
    (import "streams" (instance $cs (export "output-stream" (type (sub resource)))))
    (alias export $cs "output-stream" (type $cos))
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
  (instance $c (instantiate $child (with "streams" (instance $streams)) (with "stdout" (instance $stdout))))
  (alias export $c "seven" (func $seven))
  (export "seven" (func $seven)))
