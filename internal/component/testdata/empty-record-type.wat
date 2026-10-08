;; empty-record-type: a component declaring `record {}`, which the REFERENCE VALIDATOR REJECTS (#904).
;;
;; ## What this fixture is for
;;
;; It is not a working guest and is not meant to load. It is the specimen for a conformance rule this
;; engine adopted after asking the reference: a component may not declare a record with no fields.
;;
;; The measurement, taken with wasm-tools 1.258.0 — the same version the other fixtures here were
;; authored with:
;;
;;   $ wasm-tools parse empty-record-type.wat -o empty-record-type.wasm     # succeeds
;;   $ wasm-tools validate empty-record-type.wasm --features all
;;   error: record type must have at least one field (at offset 0xb)
;;
;; **Parse accepts it and validate refuses it**, which is why the `.wasm` exists at all: the bytes are
;; well-formed, so a decoder that read them without complaint would not be reading them wrongly. What is
;; broken is a rule about the type, not the encoding.
;;
;; ## Why the Canonical ABI makes this the right place to refuse
;;
;; `elem_size_record` ends with `assert(s > 0)` (definitions.py:1256), so an empty record has **no
;; layout** — asked of the pinned model directly, `elem_size(RecordType([]))` raises `AssertionError`,
;; and `alignment(RecordType([]))` answers 1. A type the ABI declines to lay out cannot carry a value,
;; so accepting it at load would mean accepting a type nothing can ever do anything with.
;;
;; Burroughs' codec answered **0** for this type's size before #904, and 0 was worse than refusing: a
;; zero-size element makes `canon.ListByteLength` return 0 for *any* count, so a list of a million empty
;; records would have been framed as zero bytes with no error.
;;
;; ## The `(param "x" $t)` is load-bearing
;;
;; The record is referenced by a function type, so a decoder that only walked *reachable* types would
;; still meet it. A fixture declaring the type and never using it would pass an engine that happened to
;; skip unreferenced type definitions, and would not be testing the rule.
(component
  (type $t (record))
  (type $f (func (param "x" $t)))
  (core module $m (func (export "f") (param i32)))
  (core instance $mi (instantiate $m))
  (alias core export $mi "f" (core func $cf))
  (func (;0;) (type $f) (canon lift (core func $cf)))
  (export "e" (func 0))
)
