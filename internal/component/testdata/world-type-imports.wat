;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; world-type-imports: ADR 0118's top-level type imports. A world-level type import bound `eq` names the
;; type it is equal to, and one bound `sub resource` is a fresh resource whose identity is the import's
;; name, which is how a world-level resource and its `[method]r.peek` import arrive. Decode-only: nothing
;; is lowered, so nothing is instantiated.
;; Assembled with `wasm-tools parse world-type-imports.wat -o world-type-imports.wasm` (wasm-tools 1.258.0).
(component
  (type $u (record (field "a" u32)))
  (import "u" (type $iu (eq $u)))
  (import "r" (type $r (sub resource)))
  (import "[method]r.peek" (func (param "self" (borrow $r)) (result $iu))))
