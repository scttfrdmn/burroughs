;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; nested-write-via-stream-error-code: #1257, the control. nested-write-via-stream-other-error-code's component,
;; byte for byte but for one label in each `error-code`: both are the host interface's, `io`,
;; `illegal-byte-sequence` and `pipe`. It must instantiate and its `seven` return 7, so the other fixture's refusal
;; is the labels' and nothing else's.
;; Assembled with `wasm-tools parse nested-write-via-stream-error-code.wat -o nested-write-via-stream-error-code.wasm`
;; (wasm-tools 1.258.0).
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
  (component $child
    (import "stdout" (instance $so
      (type $ec (enum "io" "illegal-byte-sequence" "pipe"))
      (export "error-code" (type $ec2 (eq $ec)))
      (type $s (stream u8))
      (type $r (result (error $ec2)))
      (type $f (future $r))
      (export "write-via-stream" (func (param "data" $s) (result $f)))))
    (alias export $so "write-via-stream" (func $wvs))
    (core func $wvs-low (canon lower (func $wvs)))
    (core module $m
      (import "host" "write-via-stream" (func (param i32) (result i32)))
      (func (export "seven") (result i32) i32.const 7))
    (core instance $i (instantiate $m (with "host" (instance (export "write-via-stream" (func $wvs-low))))))
    (func (export "seven") (result u32) (canon lift (core func $i "seven"))))
  (instance $c (instantiate $child (with "stdout" (instance $stdout))))
  (alias export $c "seven" (func $seven))
  (export "seven" (func $seven)))
