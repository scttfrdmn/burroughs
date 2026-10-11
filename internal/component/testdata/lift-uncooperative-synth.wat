;; lift-uncooperative-synth: a guest that ignores TASK_CANCELLED and never stops.
;;
;; # Why it exists
;;
;; `TestCloseReachesItsBoundWithANamedOutcome` needs a call that cannot finish within `Close`'s bound.
;; It used to get one by arithmetic — `componentCloseBound` set to 0 against a *cooperating* fixture, so
;; the bound was reached only while the cancellation round trip was slower than the window between
;; `CancelAll()` and `Close`'s single sample of `active`. That window is sub-millisecond but real, and
;; under load the guest finished first: `active` dropped to 0, `Close` correctly reported a clean close,
;; and the test called the correct answer a failure. Measured, forced: with the call driven to completion
;; before `Close`'s sample, `Close(0)` returns nil 20/20 with the call carrying the cancellation sentinel;
;; with the unmodified ordering it returns `ErrCloseIncomplete` 60/60 on an idle host.
;;
;; The old fixture's own comment named this gap and declined to close it: *"A real uncooperative guest
;; would be a better specimen and there is no committed one: a guest that ignores `TASK_CANCELLED`
;; entirely would have to be authored, and the fixtures that exist all cooperate."* This is that guest.
;;
;; # What makes it uncooperative, and it is one instruction
;;
;; The callback **ignores its event triple entirely** and returns YIELD. A cooperating guest inspects the
;; code, sees `TASK_CANCELLED`, resolves with `task.return` and returns EXIT. This one cannot: it never
;; calls `task.return`, so it never resolves, and it never returns EXIT, so the loop re-enters it forever.
;; `active` therefore stays non-zero across `Close`'s sample **by construction** rather than by timing.
;;
;; # What ends the call, measured, because a guest that never stops has to be reclaimed by something
;;
;;   Close(0) after 14.25µs -> ErrCloseIncomplete ("gave up after 0s; the guest did not finish its
;;                             cancellation")
;;   the call ENDED 48.958µs after Close returned, with:
;;                             instance is closed: "cb" was not called (contract §5 H-3)
;;
;; `Close` tears down on both paths, so `c.in.Close()` closes the world; the callback loop's next
;; re-entry goes through `internal/interp/interp.go:invokeIndex`, whose first act refuses a closed
;; instance with `internal/interp/host.go:ErrClosed`. Nothing else was going to end it: the loop requests
;; cancellation **once** (`!ctxRequested`, *"because a refused request would otherwise be retried every
;; iteration for the rest of a spinning guest's life"*), and the `callbackYield` arm re-enters at once
;; with EVENT_NONE and never parks, so `liftParkBound` is never consulted.
;;
;; **So the reclamation needs the guest to come back.** This fixture does — it re-enters on every yield.
;; A goroutine that never re-enters is not covered by this, which matters for ADR 0100's parked Threads.
;;
;; Authored with `wasm-tools parse` 1.258.0, modelled on `async-lift-yield-synth.wat`. The .wat is
;; committed beside the .wasm:
;;
;;   $ wasm-tools parse lift-uncooperative-synth.wat -o lift-uncooperative-synth.wasm
(component
  (core module $memmod (;0;)
    (memory (;0;) 1)
    (export "m" (memory 0))
  )
  (core instance $memi (;0;) (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm (;0;)))

  (type $rt (;0;) (func async (result u32)))

  ;; Imported but never called: the point of this guest is that it does NOT resolve. Declared so the
  ;; module's shape matches the cooperating fixtures it is compared against, and so a future repair that
  ;; made it resolve would be a one-line edit rather than a re-authoring.
  (core func $taskret (;0;) (canon task.return (result u32)))

  (core module $runmod (;1;)
    (type (;0;) (func (param i32)))
    (type (;1;) (func (result i32)))
    (type (;2;) (func (param i32 i32 i32) (result i32)))
    (import "" "taskret" (func $taskret (;0;) (type 0)))
    (export "callee" (func 1))
    (export "cb" (func 2))

    ;; The callee yields once. Packed return is `code | (si << 4)` with code 1 = YIELD and no set index,
    ;; so the whole value is 1 — the same spelling `async-lift-yield-synth.wat` uses.
    (func (;1;) (type 1) (result i32)
      i32.const 1
    )

    ;; The callback, and the whole fixture is this function. It does not read its event triple — not even
    ;; to branch — so TASK_CANCELLED is indistinguishable from EVENT_NONE to it, and it yields again.
    ;; A guest cannot be made less cooperative than this without refusing to return at all, which would
    ;; be a hang inside the guest rather than a guest that will not stop.
    (func (;2;) (type 2) (param i32 i32 i32) (result i32)
      i32.const 1
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
