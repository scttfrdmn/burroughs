;; empty-tuple-type: a component declaring `tuple<>`, which the REFERENCE VALIDATOR REJECTS (#904).
;;
;; The sibling of `empty-record-type.wat`; read that one's header for the reasoning, which is the same.
;; What follows is only what differs.
;;
;; The measurement, wasm-tools 1.258.0:
;;
;;   $ wasm-tools parse empty-tuple-type.wat -o empty-tuple-type.wasm      # succeeds
;;   $ wasm-tools validate empty-tuple-type.wasm --features all
;;   error: tuple type must have at least one type (at offset 0xb)
;;
;; ## Why this is the same rule and not a second one
;;
;; A tuple **despecializes to a record** (definitions.py:1133), so `tuple<>` is `record {}` by the time
;; any layout rule sees it — `elem_size` despecializes first (:1228), and `elem_size(TupleType([]))`
;; raises the same `AssertionError`. One rule, reached two ways, which is why the two decoder arms refuse
;; together rather than one being the general case.
;;
;; ## The one asymmetry worth not losing
;;
;; An empty tuple was the specimen on which this engine's bridge and its instantiate-time predicate
;; **disagreed**: the bridge refused it (no layout) while `unmodeledValKind` accepted it, because a loop
;; over zero elements finds nothing to refuse. That divergence was declared in
;; `TestTheBridgeAndTheUnmodeledPredicateAgree` and is retired by this rule — once the decoder refuses
;; the type, no decoded `ValType` can be an empty tuple, so there is nothing left for the two to
;; disagree about.
(component
  (type $t (tuple))
  (type $f (func (param "x" $t)))
  (core module $m (func (export "f") (param i32)))
  (core instance $mi (instantiate $m))
  (alias core export $mi "f" (core func $cf))
  (func (;0;) (type $f) (canon lift (core func $cf)))
  (export "e" (func 0))
)
