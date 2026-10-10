;; A child that CANNOT START PROMPTLY, so a parent can hold a subtask in STARTING and reach
;; `subtask.cancel` status 3 (CANCELLED_BEFORE_STARTED) — the status #884 registers as unmeasured.
;;
;; # Why WAT and not Rust
;;
;; The canceller beside this is already WAT, and backpressure is a canonical built-in a hand-written
;; component can call directly. That sidesteps `build.sh`'s two-toolchain requirement and all three of
;; its recorded blocked paths, and it makes the fixture precise: the only thing holding the subtask is
;; one `canon backpressure.inc` visible in this source, not something inside `wit-bindgen`'s runtime.
;;
;; Note the API drift recorded on #884: the issue cites `backpressure_set(true)` from wit-bindgen-rt
;; 0.44.0, but the version this tree builds against (0.62.0) has `backpressure_inc`/`dec` instead. In
;; WAT the built-in is spelled `canon backpressure.inc`, which `wasm-tools parse` 1.258.0 accepts —
;; checked before any of this was written.
;;
;; # The shape
;;
;;	arm   a SYNC export that increments the instance's backpressure counter
;;	run   an ASYNC export that yields, so it stays in flight IF it ever starts
;;
;; `run` yields rather than returning promptly on purpose. If backpressure does not hold the task, the
;; reading must show that clearly rather than collapsing to a completed call: a started-and-yielding
;; task gives state 1 (STARTED) and status 4, which is the #862 case and is visibly NOT a 3. A `run`
;; that returned immediately could give status 2 (RETURNED) and leave the cause ambiguous.
(component
  ;; backpressure.inc, lowered so the core module can call it.
  (core func $bpinc (;0;) (canon backpressure.inc))

  ;; **The receipt channel exists so its SILENCE is a measurement.**
  ;;
  ;; Without it, a reading of this fixture shows `RECEIPT none` because the child has no channel —
  ;; structurally absent, which looks identical to "the cancellation did not reach the guest" and
  ;; answers nothing. With it, `RECEIPT none` means the channel was there and nothing used it, which
  ;; is the fact #884 asks for: whether a task cancelled before it started can call an import.
  ;;
  ;; The callee below calls it as its first act. So if the task ever starts, the receipt fires — which
  ;; makes this channel a second, independent check on whether backpressure held, agreeing or
  ;; disagreeing with the lower's reported state rather than restating it.
  (type $notet (;0;) (func (param "code" u32)))
  (import "note" (func $note (;0;) (type $notet)))
  (core func $notelow (;1;) (canon lower (func $note)))

  (core module $m (;0;)
    (type $v (;0;) (func))
    (type $n (;1;) (func (param i32)))
    (type $callee (;2;) (func (param i32) (result i32)))
    (type $cb (;3;) (func (param i32 i32 i32) (result i32)))
    (import "" "bpinc" (func $bpinc (;0;) (type $v)))
    (import "" "note" (func $note (;1;) (type $n)))

    ;; Hold the instance. One call is enough: the counter is incremented and never decremented, so
    ;; every later task waits before starting. Nothing decrements it because this fixture never needs
    ;; the task to proceed — the parent cancels it.
    (func $arm (;1;) (type $v)
      call $bpinc
    )

    ;; `run`'s callee. Packed return is `code | (si << 4)` with code 1 = YIELD and no set index, so the
    ;; whole value is 1 — the same spelling `async-lift-yield-synth.wat` uses.
    (func $callee (;2;) (type $callee) (param i32) (result i32)
      ;; The receipt, fired as the FIRST act of the task body. If backpressure held, this never runs
      ;; and the reading's `RECEIPT none` is a measured silence. If it did not hold, this fires and
      ;; the reading says so independently of the lower's reported state.
      i32.const 1
      call $note
      i32.const 1
    )

    ;; The callback. Yields again, so the task stays in flight until the parent cancels it rather than
    ;; completing while the cancellation is in flight.
    (func $cbf (;3;) (type $cb) (param i32 i32 i32) (result i32)
      i32.const 1
    )

    (export "arm" (func $arm))
    (export "callee" (func $callee))
    (export "cb" (func $cbf))
  )

  (core instance $deps (;0;)
    (export "bpinc" (func $bpinc))
    (export "note" (func $notelow))
  )
  (core instance $i (;1;) (instantiate $m
      (with "" (instance $deps))
    )
  )
  (alias core export $i "arm" (core func $armf (;1;)))
  (alias core export $i "callee" (core func $calleef (;2;)))
  (alias core export $i "cb" (core func $cbf (;3;)))

  (type $armt (;0;) (func))
  (func $arm (;0;) (type $armt) (canon lift (core func $armf)))
  (export (;1;) "arm" (func $arm))

  (type $runt (;1;) (func async (param "id" u32) (result u32)))
  (func $run (;1;) (type $runt) (canon lift (core func $calleef) async (callback $cbf)))
  (export (;2;) "run" (func $run))
)
