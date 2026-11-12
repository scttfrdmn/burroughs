;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; write-via-stream-error-code: #1248, ADR 0118's payload agreement, the control. The same guest as
;; write-via-stream-other-error-code.wat, byte for byte but for one label: its `error-code` is the host
;; interface's, `io`, `illegal-byte-sequence` and `pipe`. It must instantiate, so the other fixture's
;; refusal is the labels' and nothing else's.
;; Assembled with `wasm-tools parse write-via-stream-error-code.wat -o write-via-stream-error-code.wasm`
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
  (alias export $stdout "write-via-stream" (func $wvs))
  (core func $wvs-low (canon lower (func $wvs)))
  (core module $m
    (import "host" "write-via-stream" (func (param i32) (result i32))))
  (core instance (instantiate $m (with "host" (instance (export "write-via-stream" (func $wvs-low)))))))
