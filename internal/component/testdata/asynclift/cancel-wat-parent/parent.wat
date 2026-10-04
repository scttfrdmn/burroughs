;; The WAT canceller: a parent that calls the child's async export, cancels the subtask, and **reports the
;; numeric status `subtask.cancel` returned**.
;;
;; # Why this exists, and why a Rust parent cannot replace it
;;
;; `wit-bindgen` DOES issue `subtask.cancel` when an in-flight async import's future is dropped — that was
;; measured and is why #862 planned an ordinary Rust parent. But **issuing is not observing**, and four
;; facts in `wit-bindgen-rt 0.44.0` (the runtime `wit-bindgen 0.62.0` depends on) put the status out of a
;; Rust guest's reach:
;;
;;   1. the generated async import awaits immediately (`interface.rs:1155`:
;;      `_MySubtask{…}.call((args)).await`), so the guest never holds the `WaitableOperation`;
;;   2. cancellation therefore runs in `Drop`, which calls `cancel()` and **discards** its value
;;      (`waitable.rs:456`, a bare statement);
;;   3. no `pub` item exposes the numeric status, and `STATUS_*` is unexported;
;;   4. even reachable, Rust maps STATUS_STARTED_CANCELLED (3) and STATUS_RETURNED_CANCELLED (4) to one
;;      `Ok(Err(()))` (`subtask.rs:176-203`).
;;
;; So this parent is the only way the status reaches a reading — the same situation as
;; `task-cancel-synth.wat`, which exists because the committed Rust guests could not reach `task.cancel`'s
;; branch. It is a **synthetic** canceller, and the Rust parent beside it is what shows a real one behaves
;; the same on every fact they share.
;;
;; # What it reports, and why the lower's own state is one of the facts
;;
;;	kind 1  the async lower's returned STATE (packed & 0xf)
;;	kind 2  the status `subtask.cancel` returned
;;
;; The state is reported because it says **which cancellation case the reading is of**: the child must have
;; STARTED for status 4 (CANCELLED_BEFORE_RETURNED) to be the expected answer, and a reading that did not
;; record the state could not tell a 3 from a mis-set-up 4.
;;
;; Authored with `wasm-tools parse` 1.258.0; the `.wat` is committed beside the `.wasm`.
(component
  (core module $memmod
    (memory (;0;) 1)
    (export "m" (memory 0))
  )
  (core instance $memi (;0;) (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm (;0;)))

  ;; The child's export. `async` and the `memory` canonopt are both forced — see ../../compose/README.md,
  ;; where each was found by a refusal.
  (type $runt (;0;) (func async (param "id" u32) (result u32)))
  (import "run" (func $run (;0;) (type $runt)))
  (core func $runlow (;0;) (canon lower (func $run) async (memory $cm)))

  ;; The reporting channel. A host import rather than stdout, so the harness is the witness rather than
  ;; the subject — the same reason the child's `note` is an import.
  (type $rept (;1;) (func (param "kind" u32) (param "value" u32)))
  (import "report" (func $report (;1;) (type $rept)))
  (core func $replow (;1;) (canon lower (func $report)))

  (core func $cancel (;2;) (canon subtask.cancel))
  (core func $stdrop (;3;) (canon subtask.drop))

  (core module $m
    (type $runty (;0;) (func (param i32 i32) (result i32)))
    (type $repty (;1;) (func (param i32 i32)))
    (type $canty (;2;) (func (param i32) (result i32)))
    (type $dropty (;3;) (func (param i32)))
    (type $goty (;4;) (func))
    (import "" "run" (func $run (;0;) (type $runty)))
    (import "" "report" (func $rep (;1;) (type $repty)))
    (import "" "cancel" (func $can (;2;) (type $canty)))
    (import "" "stdrop" (func $drop (;3;) (type $dropty)))

    (func $go (;4;) (type $goty)
      (local $packed i32)
      (local $sub i32)
      (local $status i32)

      ;; Start the child. The async lower's flat ABI is (id, retptr) -> packed, where the packed i32 is
      ;; [state | subtaski<<4]. retptr 0 is fine: the result is never lifted here, because the point is to
      ;; cancel before it arrives.
      i32.const 1
      i32.const 0
      call $run
      local.set $packed

      ;; kind 1: the state the lower returned, so the reading says which case it is.
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

      ;; Release the subtask handle. The model asserts a dropped subtask is resolved or cancelled, which
      ;; it now is; leaving it would be a handle leak the engine may legitimately complain about.
      local.get $sub
      call $drop
    )
    (export "go" (func $go))
  )

  (core instance $deps (;1;)
    (export "run" (func $runlow))
    (export "report" (func $replow))
    (export "cancel" (func $cancel))
    (export "stdrop" (func $stdrop))
  )
  (core instance $i (;2;) (instantiate $m
      (with "" (instance $deps))
    )
  )
  (alias core export $i "go" (core func $g (;4;)))
  (type $goct (;2;) (func))
  (func (;2;) (type $goct) (canon lift (core func $g)))
  (export (;3;) "go" (func 2))
)
