;; index-call: the smallest component that CALLS `canon thread.index` and returns what it got.
;;
;; # Why a second fixture, when switch.wat already covers all eight forms
;;
;; `switch.wat` is the family's oracle and uses all eight phase-1 productions, so Burroughs cannot run it
;; until all eight are modeled — `thread.resume-later` (0x28) is still refused at decode, before any gate
;; speaks. ADR 0100's point 4 lands one form at a time, so the first slice needs a fixture that uses
;; **only** `thread.index`. This is it.
;;
;; `thread-index-synth.wasm` (committed with #957) is smaller still but calls nothing: it declares the
;; canon and stops, which is enough to witness a refusal and not enough to witness a value.
;;
;; # What it measures
;;
;; A SYNC lift over a core func that calls `thread.index` and returns it. wasmtime 49.0.2 answers **1**,
;; the same entry-Thread index `switch.reading` records for its async lift — so the number is the
;; reference's reserved-zero `Table` policy showing through (`array = [None]`, so the first allocation is
;; 1) and not an artefact of either lift shape.
;;
;;   $ wasmtime run -W component-model-threading=y --invoke 'idx()' index-call.wasm
;;   1
;;
;; Authored with `wasm-tools parse` 1.258.0. The .wat is committed beside the .wasm:
;;
;;   $ wasm-tools parse index-call.wat -o index-call.wasm
(component
  (core func $ti (canon thread.index))
  (core module $m
    (import "" "ti" (func $ti (result i32)))
    (func (export "f") (result i32) (call $ti))
  )
  (core instance $i (instantiate $m
    (with "" (instance (export "ti" (func $ti))))
  ))
  (func (export "idx") (result u32)
    (canon lift (core func $i "f"))
  )
)
