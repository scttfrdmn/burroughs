;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; stream-read-sync (ADR 0122): a SYNC `canon stream.read` of a stream<u8>, with no `async` option. The pin
;; admits it, waiting in the copy (def:copy:"end.wait_for_pending_event()"); Burroughs builds only the async
;; form, so this is refused at instantiate by name. It is stream-read-u32's twin but for the option and the
;; element, so each refusal is its own.
;; Assembled with `wasm-tools parse stream-read-sync.wat -o stream-read-sync.wasm` (wasm-tools 1.258.0).
(component
  (core module $mm (memory (export "m") 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (type $bytes (stream u8))
  (core func $sread (canon stream.read $bytes (memory $m)))
  (core module $gm (import "" "sread" (func (param i32 i32 i32) (result i32))))
  (core instance (instantiate $gm (with "" (instance (export "sread" (func $sread)))))))
