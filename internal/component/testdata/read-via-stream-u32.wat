;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; read-via-stream-u32 (ADR 0122): read-park.wat's import of wasi:cli/stdin@0.3.0's read-via-stream, but
;; declaring the tuple's stream as stream<u32>. The host writes bytes into the stream, so the guest's
;; declaration must be the host's, tuple<stream<u8>, future<result<_, error-code>>>, and this is refused at
;; instantiate. read-park.wasm, which declares the host's, is the control that instantiates.
;; Assembled with `wasm-tools parse read-via-stream-u32.wat -o read-via-stream-u32.wasm` (wasm-tools 1.258.0).
(component
  (type $types-t (instance
    (type $ec (enum "io" "illegal-byte-sequence" "pipe"))
    (export "error-code" (type (eq $ec)))))
  (import "wasi:cli/types@0.3.0" (instance $types (type $types-t)))
  (alias export $types "error-code" (type $error-code))
  (type $stdin-t (instance
    (alias outer 1 $error-code (type $ec))
    (export "error-code" (type $ec2 (eq $ec)))
    (type $s (stream u32))
    (type $r (result (error $ec2)))
    (type $f (future $r))
    (type $t (tuple $s $f))
    (export "read-via-stream" (func (result $t)))))
  (import "wasi:cli/stdin@0.3.0" (instance $stdin (type $stdin-t)))
  (alias export $stdin "read-via-stream" (func $rvs))
  (core module $mm (memory (export "m") 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (core func $rvs-low (canon lower (func $rvs) (memory $m)))
  (core module $gm (import "" "rvs" (func (param i32))))
  (core instance (instantiate $gm (with "" (instance (export "rvs" (func $rvs-low)))))))
