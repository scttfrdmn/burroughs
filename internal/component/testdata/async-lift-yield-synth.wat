;; async-lift-yield-synth: the witness for the YIELD dispatch code (#871).
;;
;; # Why this is hand-authored
;;
;; The committed Rust guests never yield. `wit-bindgen`'s async codegen awaits imports, which produces
;; WAIT; nothing in its surface emits a cooperative yield, so no real guest drives `callbackYield` and the
;; branch would land untested beside two that are exercised. A hand-written component is the only consumer
;; the code has.
;;
;; # What it exercises, and what it would catch
;;
;; The callee returns **YIELD (code 1)** rather than EXIT or WAIT. The engine must then re-enter the
;; callback **immediately and with no event** — EVENT_NONE, the all-zero triple — because a yield names no
;; waitable set. The callback resolves the task with `task.return` and returns EXIT.
;;
;; Two defects this distinguishes that an EXIT-only or WAIT-only fixture cannot:
;;
;;   * a YIELD routed into the park would block forever on a set the guest never named (handle 0), so the
;;     call would end at the park's bound instead of completing;
;;   * a YIELD re-entered with a stale or fabricated event would hand the guest a non-zero event code,
;;     which this callback asserts against by trapping.
;;
;; The callback **traps unless its event triple is all zero**, so "called back with no event" is checked
;; inside the guest rather than only from the host side. A fixture that accepted any triple would pass
;; against an engine that re-entered with whatever happened to be in the last event.
;;
;; Authored with `wasm-tools parse` 1.258.0, modelled on `task-cancel-synth.wat`. **The .wat is committed
;; alongside the .wasm**, as that fixture does and the older ones do not — regenerating one of those means
;; re-authoring it from its prose description.
(component
  (core module $memmod (;0;)
    (memory (;0;) 1)
    (export "m" (memory 0))
  )
  (core instance $memi (;0;) (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm (;0;)))

  (type $rt (;0;) (func async (result u32)))

  ;; task.return, so the callback can resolve the task before returning EXIT. An async lift that returns
  ;; EXIT without resolving traps, so this is not optional decoration.
  (core func $taskret (;0;) (canon task.return (result u32)))

  (core module $runmod (;1;)
    (type (;0;) (func (param i32)))
    (type (;1;) (func (result i32)))
    (type (;2;) (func (param i32 i32 i32) (result i32)))
    (import "" "taskret" (func $taskret (;0;) (type 0)))
    (export "callee" (func 1))
    (export "cb" (func 2))

    ;; The callee: yield once. Packed return is `code | (si << 4)` with code 1 = YIELD and no set index,
    ;; so the whole value is 1.
    (func (;1;) (type 1) (result i32)
      i32.const 1
    )

    ;; The callback. Entered once, by the yield, with EVENT_NONE.
    (func (;2;) (type 2) (param i32 i32 i32) (result i32)
      ;; Assert the event triple is all zero. A yield carries no event, so a non-zero code, p1 or p2 means
      ;; the engine re-entered with something it invented or carried over.
      local.get 0
      local.get 1
      i32.or
      local.get 2
      i32.or
      if
        unreachable
      end
      ;; Resolve the task, then EXIT (code 0).
      i32.const 42
      call $taskret
      i32.const 0
    )
  )
  (core instance (;1;)
    (export "taskret" (func $taskret))
  )
  (core instance $runi (;2;) (instantiate $runmod
      (with "" (instance 1))
    )
  )
  (alias core export $runi "callee" (core func $calleef (;1;)))
  (alias core export $runi "cb" (core func $cbf (;2;)))
  (func (;0;) (type $rt) (canon lift (core func $calleef) async (callback $cbf)))
  (export (;1;) "run" (func 0))
)
