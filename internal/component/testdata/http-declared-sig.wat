;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; http-declared-sig: ADR 0135's declared-signature rule. A guest importing wasi:http/types@0.3.0 whose
;; `[method]fields.has` returns a u32 where the WIT, and the host that lowers its result, says bool. Its handles
;; are the host's resource, so checkHostResources passes it; only the whole-type check refuses it, at instantiate.
;; Assembled with `wasm-tools parse http-declared-sig.wat -o http-declared-sig.wasm` (wasm-tools 1.258.0).
(component
  (type $types (instance
    (export "fields" (type $f (sub resource)))
    (export "[method]fields.has" (func (param "self" (borrow $f)) (param "name" string) (result u32)))))
  (import "wasi:http/types@0.3.0" (instance $t (type $types)))
  (alias export $t "[method]fields.has" (func $has))
  (core module $mem
    (memory (export "memory") 1))
  (core instance $mi (instantiate $mem))
  (alias core export $mi "memory" (core memory $m))
  (core func $has-low (canon lower (func $has) (memory $m) string-encoding=utf8))
  (core module $use
    (import "host" "has" (func (param i32 i32 i32) (result i32))))
  (core instance (instantiate $use (with "host" (instance (export "has" (func $has-low)))))))
