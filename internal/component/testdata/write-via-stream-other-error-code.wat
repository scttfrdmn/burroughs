;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; write-via-stream-other-error-code: #1248, ADR 0118's payload agreement. A guest importing WASI 0.3's
;; `wasi:cli/stdout` `write-via-stream`, shaped as p3async-hello imports it: `error-code` is `use`d from
;; `wasi:cli/types` through an alias export and an outer alias. Its `error-code` has the labels `io` and
;; `pipe` only, where the host's interface declares `io`, `illegal-byte-sequence` and `pipe`, so the host's
;; completion would be read at the guest's discriminants. The lower must be refused at instantiate.
;; Assembled with `wasm-tools parse write-via-stream-other-error-code.wat -o write-via-stream-other-error-code.wasm`
;; (wasm-tools 1.258.0).
(component
  (type $types-t (instance
    (type $ec (enum "io" "pipe"))
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
  (alias export $stdout "write-via-stream" (func $wvs))
  (core func $wvs-low (canon lower (func $wvs)))
  (core module $m
    (import "host" "write-via-stream" (func (param i32) (result i32))))
  (core instance (instantiate $m (with "host" (instance (export "write-via-stream" (func $wvs-low)))))))
