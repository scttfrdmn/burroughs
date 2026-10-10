;; The STARTING canceller: a parent that holds the child's instance with backpressure, starts the
;; child's async export so the subtask cannot start, cancels it, and reports the numeric status.
;;
;; # What this reaches that `cancel-wat-parent` cannot
;;
;; `cancel-wat-parent` measures status 4 (CANCELLED_BEFORE_RETURNED): its child runs to its first
;; `await` before yielding, so the async lower returns STARTED synchronously and there is no window in
;; which the parent holds a subtask that has not started. `CANCELLATION.md` records status 3 as
;; **unmeasured rather than assumed** for exactly that reason, and #884 registers the gap.
;;
;; This parent calls the child's `arm` first, which increments the child instance's backpressure
;; counter. The model's task entry waits while backpressure is held, so the async lower that follows
;; should return with the subtask still in STARTING — and `subtask.cancel` on a subtask that never
;; started is status 3.
;;
;; # Reported facts, and why the lower's state is one of them
;;
;;	kind 1  the async lower's returned STATE (packed & 0xf)
;;	kind 2  the status `subtask.cancel` returned
;;
;; The state is what makes the reading self-checking, and here it carries more weight than in the 4
;; reading: a 0 (STARTING) with status 3 is the measurement, while a 1 (STARTED) with status 4 means
;; **backpressure did not hold the task** and the run measured the case that was already measured. The
;; second outcome is a finding about the mechanism, not a failed test, and the reading must be able to
;; say which happened without anyone inferring it.
;;
;; Authored with `wasm-tools parse` 1.258.0; the `.wat` is committed beside the `.wasm`.
(component
  (core module $memmod (;0;)
    (memory (;0;) 1)
    (export "m" (memory 0))
  )
  (core instance $memi (;0;) (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm (;0;)))

  ;; The child's holding export: sync, no params, no results. Lowered plainly.
  (type $armt (;0;) (func))
  (import "arm" (func $arm (;0;) (type $armt)))
  (core func $armlow (;0;) (canon lower (func $arm)))

  ;; The child's async export. `async` and the `memory` canonopt are both forced — see
  ;; ../../compose/README.md, where each was found by a refusal.
  (type $runt (;1;) (func async (param "id" u32) (result u32)))
  (import "run" (func $run (;1;) (type $runt)))
  (core func $runlow (;1;) (canon lower (func $run) async (memory $cm)))

  ;; The reporting channel. A host import rather than stdout, so the harness is the witness rather
  ;; than the subject.
  (type $rept (;2;) (func (param "kind" u32) (param "value" u32)))
  (import "report" (func $report (;2;) (type $rept)))
  (core func $replow (;2;) (canon lower (func $report)))

  (core func $cancel (;3;) (canon subtask.cancel))
  (core func $stdrop (;4;) (canon subtask.drop))

  (core module $m (;1;)
    (type $armty (;0;) (func))
    (type $runty (;1;) (func (param i32 i32) (result i32)))
    (type $repty (;2;) (func (param i32 i32)))
    (type $canty (;3;) (func (param i32) (result i32)))
    (type $dropty (;4;) (func (param i32)))
    (type $goty (;5;) (func))
    (import "" "arm" (func $arm (;0;) (type $armty)))
    (import "" "run" (func $run (;1;) (type $runty)))
    (import "" "report" (func $rep (;2;) (type $repty)))
    (import "" "cancel" (func $can (;3;) (type $canty)))
    (import "" "stdrop" (func $drop (;4;) (type $dropty)))

    (func $go (;5;) (type $goty)
      (local $packed i32)
      (local $sub i32)
      (local $status i32)

      ;; Hold the child's instance FIRST. Everything this fixture is for depends on this call having
      ;; taken effect before the task below tries to enter.
      call $arm

      ;; Start the child. Same flat ABI as `cancel-wat-parent`: (id, retptr) -> packed, where packed is
      ;; [state | subtaski<<4]. retptr 0 is fine because the result is never lifted — the point is to
      ;; cancel before it arrives.
      i32.const 1
      i32.const 0
      call $run
      local.set $packed

      ;; kind 1: the state the lower returned. 0 (STARTING) is the measurement; 1 (STARTED) means
      ;; backpressure did not hold and this run measured the already-measured case.
      i32.const 1
      local.get $packed
      i32.const 15
      i32.and
      call $rep

      local.get $packed
      i32.const 4
      i32.shr_u
      local.set $sub

      ;; kind 2: the status subtask.cancel returns. THE fact this parent exists for.
      local.get $sub
      call $can
      local.set $status
      i32.const 2
      local.get $status
      call $rep

      ;; Release the subtask handle: the model asserts a dropped subtask is resolved or cancelled.
      local.get $sub
      call $drop
    )
    (export "go" (func $go))
  )

  (core instance $deps (;1;)
    (export "arm" (func $armlow))
    (export "run" (func $runlow))
    (export "report" (func $replow))
    (export "cancel" (func $cancel))
    (export "stdrop" (func $stdrop))
  )
  (core instance $i (;2;) (instantiate $m
      (with "" (instance $deps))
    )
  )
  (alias core export $i "go" (core func $g (;5;)))
  (type $goct (;3;) (func))
  (func (;3;) (type $goct) (canon lift (core func $g)))
  (export (;4;) "go" (func 3))
)
