;; lift-cancel-yield-synth: the witness for the lift loop's TOP-OF-LOOP cancellation check (#887).
;;
;; # Why this fixture exists: the check had no witness, and that was measured
;;
;; ADR 0094 put a `deliver_pending_cancel` at the top of the lift loop, mirroring definitions.py def:2129.
;; Neutering it left **the whole component suite green**, including #887's own end-to-end cancellation
;; witness — because that witness drives a `wit-bindgen` guest, which awaits an import and therefore
;; returns WAIT, and a WAIT's cancellation is delivered by the park's own check (def:789, `awaitEvent`).
;; So the engine had a check nothing covered: the same shape as grave #885 one level up, found by
;; falsifying rather than by review.
;;
;; # What only this fixture can reach
;;
;; A guest that **yields repeatedly and never parks**. The YIELD arm re-enters the callback immediately,
;; so the park is never entered and its check never runs. The top-of-loop check is then the ONLY path by
;; which a host cancellation can reach the task. Remove it and this guest cannot be cancelled at all —
;; it spins until the bound below trips.
;;
;; That is also why a yielding guest is the honest case and not a contrived one: a guest doing CPU work
;; with cooperative yields is exactly the guest a host most wants to be able to cancel, and it is the one
;; a WAIT-only witness cannot speak for.
;;
;; `wit-bindgen` cannot produce it — its async codegen awaits imports, which yields WAIT, and nothing in
;; its surface emits a cooperative yield (the same reason `async-lift-yield-synth.wat` is hand-authored).
;;
;; # The bound, and why a fixture that spins must carry one
;;
;; The callback counts its entries in a core global and traps at 200000. Without it, an engine that never
;; delivers the cancellation would make this test **hang** rather than fail, and *a park that cannot be
;; satisfied must end in a verdict* — the principle `liftParkBound` exists for, applied to a spin instead
;; of a park. 200000 guest re-entries is well past what the test's cancel request needs and still fails in
;; seconds rather than never.
;;
;; # Trapping on an unexpected event code is part of the assertion
;;
;; The callback accepts exactly two event codes: 0 (NONE, a yield's re-entry) and 6 (TASK_CANCELLED).
;; Anything else traps, so "the engine delivered the RIGHT code" is checked inside the guest rather than
;; inferred from the call completing. An engine that delivered, say, EVENT_SUBTASK on cancellation would
;; otherwise look identical from the host side.
;;
;; Authored with `wasm-tools parse` 1.258.0, modelled on `async-lift-yield-synth.wat`. The .wat is
;; committed alongside the .wasm.
(component
  (type $rt (;0;) (func async (result u32)))

  ;; task.cancel (0x05): what the guest calls once it has been told its task is cancelled. This is the
  ;; built-in whose CANCEL_DELIVERED precondition the mechanism exists to satisfy — before ADR 0094 it
  ;; could only trap.
  (core func $taskcancel (;0;) (canon task.cancel))

  (core module $runmod (;0;)
    (type (;0;) (func))
    (type (;1;) (func (result i32)))
    (type (;2;) (func (param i32 i32 i32) (result i32)))
    (import "" "taskcancel" (func $taskcancel (;0;) (type 0)))
    (global $n (;0;) (mut i32) (i32.const 0))
    (export "callee" (func 1))
    (export "cb" (func 2))

    ;; The callee: yield immediately. Packed return is `code | (si << 4)`; code 1 = YIELD and a yield
    ;; names no set, so the whole value is 1.
    (func (;1;) (type 1) (result i32)
      i32.const 1
    )

    ;; The callback. Re-entered once per yield with EVENT_NONE, and once with EVENT_TASK_CANCELLED (6)
    ;; when the host cancels.
    (func (;2;) (type 2) (param i32 i32 i32) (result i32)
      ;; TASK_CANCELLED = 6: resolve the task through task.cancel, then EXIT (code 0). The task resolves
      ;; WITHOUT task.return, which is the whole point — a cancelled task has no result.
      local.get 0
      i32.const 6
      i32.eq
      if
        call $taskcancel
        i32.const 0
        return
      end

      ;; Any code other than NONE or TASK_CANCELLED means the engine invented or carried over an event.
      local.get 0
      if
        unreachable
      end
      ;; A yield's re-entry carries the all-zero triple, so non-zero payloads are wrong too.
      local.get 1
      local.get 2
      i32.or
      if
        unreachable
      end

      ;; Bound the spin: trap rather than hang if the cancellation never arrives.
      global.get $n
      i32.const 1
      i32.add
      global.set $n
      global.get $n
      i32.const 200000
      i32.gt_u
      if
        unreachable
      end

      ;; Yield again (code 1).
      i32.const 1
    )
  )
  (core instance (;0;)
    (export "taskcancel" (func $taskcancel))
  )
  (core instance $runi (;1;) (instantiate $runmod
      (with "" (instance 0))
    )
  )
  (alias core export $runi "callee" (core func $calleef (;1;)))
  (alias core export $runi "cb" (core func $cbf (;2;)))
  (func (;0;) (type $rt) (canon lift (core func $calleef) async (callback $cbf)))
  (export (;1;) "run" (func 0))
)
