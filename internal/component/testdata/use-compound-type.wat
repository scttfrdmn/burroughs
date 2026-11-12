;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; use-compound-type: ADR 0118's type exports carry their own references. `test:t/types` exports a record
;; `b` whose field is its enum `a`, written as a reference to `a`'s local index. `test:t/use` `use`s `b`
;; (a top-level alias export and an outer alias), and its `get` returns it. The field's reference is an
;; ordinal of the types instance's scope, so it must be resolved there before `b` leaves it. Decode-only.
;; Assembled with `wasm-tools parse use-compound-type.wat -o use-compound-type.wasm` (wasm-tools 1.258.0).
(component
  (type $types-t (instance
    (type $a (enum "x" "y"))
    (export "a" (type $ea (eq $a)))
    (type $b (record (field "f" $ea)))
    (export "b" (type (eq $b)))))
  (import "test:t/types@0.1.0" (instance $types (type $types-t)))
  (alias export $types "b" (type $b))
  (type $use-t (instance
    (alias outer 1 $b (type $ob))
    (export "b" (type $eb (eq $ob)))
    (export "get" (func (result $eb)))))
  (import "test:t/use@0.1.0" (instance $use (type $use-t))))
