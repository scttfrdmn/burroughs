;; The WAT caller: a parent that calls the child's async export, **waits for it, and reports the value it
;; returned** (#888).
;;
;; # Why this exists beside cancel-wat-parent
;;
;; Every composed artefact in the tree before this one **cancels**. That made the cross-component *call*
;; unwitnessable: the only composed paths either cancelled the child or, in `resultlist_test.go`, asserted
;; load and instantiate without calling at all. So the capability #888 adds had no arm that exercised it
;; end to end, and the cancellation arm is blocked on a separate defect
;; ([#892](https://github.com/scttfrdmn/burroughs/issues/892) — `subtask.cancel` returns BLOCKED where the
;; model waits), which would have made a cancelling witness fail for a reason that is not about the call.
;;
;; This parent is the completing path: start the child, park on its subtask, read the result the lower
;; lowered to the retptr, report it.
;;
;; # What it reports
;;
;;	kind 1  the async lower's returned STATE (packed & 0xf) -- WHICH arm the reading is of
;;	kind 2  the result value, read at the retptr on the INLINE arm (lower returned RETURNED)
;;	kind 3  the result value, read at the retptr after the PARK (lower returned STARTED, then an event)
;;
;; The state is reported for the same reason `cancel-wat-parent` reports it: it says which case the reading
;; is of. Here it does more — kinds 2 and 3 are **different arms of the engine**, and a reading that showed
;; only "the value was 49" could not tell a parked cross-component call from one that resolved inline.
;;
;; **Both arms are reachable, and this comment was wrong twice before saying so.**
;;
;; Draft 1: *"a host whose `tick` resolves immediately produces kind 2; one that defers produces kind 3"*
;; — falsified locally, where both hosts produced kind 3.
;;
;; Draft 2 concluded from that: *"kind 2 is unreachable through THIS child … the child is a `wit-bindgen`
;; guest, and its async lift always returns WAIT … so the parent's lower never sees RETURNED inline no
;; matter how fast the host is. The inline arm is a property of the callee's ABI, not of the host's
;; latency."* — falsified by CI's `ubuntu-24.04-arm`, which reported kind 2 with the right value.
;;
;; What is actually true: the child's lift runs on the adapter's own goroutine (ADR 0095). If it
;; completes — `task.return` included — before the lower re-checks the subtask, the lower legitimately
;; returns RETURNED with the result already at the retptr. **Which arm is taken is a scheduling fact**,
;; and neither the host's latency nor the callee's ABI decides it alone.
;;
;; Draft 2's error is the more instructive one: it generalised from a local measurement that agreed with
;; a plausible mechanism, and the mechanism was wrong. The witness asserted it, which is how it was
;; caught — so the arm is now reported and the value checked in whichever channel carries it.
;;
;; # Why the returned value is the *child's* arithmetic and not a constant
;;
;; The child is `receipt/`, whose `run(id) -> u32` returns whatever the host's `tick(id)` resolved with. So
;; the test picks a `tick` value that is **not** the `id`, not zero, and not a value a lazy
;; implementation would produce by accident — because the one piece of this slice with no prior code path
;; is lifting the child's flat result into the parent's component value, and a mis-lift is a value of the
;; right type with the wrong contents. A result of 0 would have passed against a zero-initialised read.
;;
;; # The `& 0xf` is deliberate, and `async-waitset-synth.wat` does it differently
;;
;; That fixture compares the WHOLE packed value to 2, which is only correct while the subtask index is 0.
;; Here the state is masked out first. Not a repair of that fixture — its subtask index is 0 and it is
;; right for its own case — but this one must not inherit the shortcut, because a composed parent gets
;; whatever index the child's table assigned.
;;
;; Authored with `wasm-tools parse` 1.258.0, modelled on `cancel-wat-parent/parent.wat` for the component
;; scaffolding and on `async-waitset-synth.wasm` for the join/wait idiom. The `.wat` is committed beside
;; the `.wasm`.
(component
  (core module $memmod
    (memory (;0;) 1)
    (export "m" (memory 0))
  )
  (core instance $memi (;0;) (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm (;0;)))

  ;; The child's export. `async` and the `memory` canonopt are both forced — see ../../compose/README.md.
  (type $runt (;0;) (func async (param "id" u32) (result u32)))
  (import "run" (func $run (;0;) (type $runt)))
  (core func $runlow (;0;) (canon lower (func $run) async (memory $cm)))

  ;; The reporting channel: a host import, so the harness is the witness rather than the subject.
  (type $rept (;1;) (func (param "kind" u32) (param "value" u32)))
  (import "report" (func $report (;1;) (type $rept)))
  (core func $replow (;1;) (canon lower (func $report)))

  (core func $wsnew (;2;) (canon waitable-set.new))
  (core func $wsjoin (;3;) (canon waitable.join))
  (core func $wswait (;4;) (canon waitable-set.wait (memory $cm)))
  (core func $stdrop (;5;) (canon subtask.drop))

  (core module $m
    (type $runty (;0;) (func (param i32 i32) (result i32)))
    (type $repty (;1;) (func (param i32 i32)))
    (type $newty (;2;) (func (result i32)))
    (type $jointy (;3;) (func (param i32 i32)))
    (type $waitty (;4;) (func (param i32 i32) (result i32)))
    (type $dropty (;5;) (func (param i32)))
    (type $goty (;6;) (func (result i32)))
    (import "" "mem" (memory (;0;) 1))
    (import "" "run" (func $run (;0;) (type $runty)))
    (import "" "report" (func $rep (;1;) (type $repty)))
    (import "" "wsnew" (func $wsnew (;2;) (type $newty)))
    (import "" "wsjoin" (func $wsjoin (;3;) (type $jointy)))
    (import "" "wswait" (func $wswait (;4;) (type $waitty)))
    (import "" "stdrop" (func $drop (;5;) (type $dropty)))

    (func $go (;6;) (type $goty) (result i32)
      (local $packed i32)
      (local $state i32)
      (local $sub i32)
      (local $si i32)

      ;; Start the child. The async lower's flat ABI is (id, retptr) -> packed, where packed is
      ;; [state | subtaski<<4]. retptr 0 is where the lowered u32 result lands.
      i32.const 7
      i32.const 0
      call $run
      local.set $packed

      local.get $packed
      i32.const 15
      i32.and
      local.set $state

      ;; kind 1: which arm this reading is of.
      i32.const 1
      local.get $state
      call $rep

      ;; RETURNED (2) inline: the lower already wrote the result to the retptr, and there is no subtask
      ;; handle to join or drop (the sync-resolving arm registers none).
      local.get $state
      i32.const 2
      i32.eq
      if
        i32.const 2
        i32.const 0
        i32.load
        call $rep
        i32.const 0
        i32.load
        return
      end

      ;; STARTED: park on the subtask, then read the result the resolution lowered.
      local.get $packed
      i32.const 4
      i32.shr_u
      local.set $sub

      call $wsnew
      local.set $si
      local.get $sub
      local.get $si
      call $wsjoin
      local.get $si
      i32.const 8
      call $wswait
      drop

      ;; kind 3: the value after the park.
      i32.const 3
      i32.const 0
      i32.load
      call $rep

      ;; The wait delivered the resolution, so the handle is resolve-delivered and dropping it is legal.
      ;; Leaving it would be a handle leak the engine may legitimately refuse.
      local.get $sub
      call $drop

      i32.const 0
      i32.load
    )
    (export "go" (func $go))
  )

  (core instance $deps (;1;)
    (export "mem" (memory $cm))
    (export "run" (func $runlow))
    (export "report" (func $replow))
    (export "wsnew" (func $wsnew))
    (export "wsjoin" (func $wsjoin))
    (export "wswait" (func $wswait))
    (export "stdrop" (func $stdrop))
  )
  (core instance $i (;2;) (instantiate $m
      (with "" (instance $deps))
    )
  )
  (alias core export $i "go" (core func $g (;6;)))
  (type $goct (;2;) (func (result u32)))
  (func (;2;) (type $goct) (canon lift (core func $g)))
  (export (;3;) "go" (func 2))
)
