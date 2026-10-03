;; task-cancel-synth: the witness for `task.cancel` (0x05) on a task that has NOT been cancelled (#864).
;;
;; The committed Rust guests cannot exercise this. `wit-bindgen` emits `task.cancel` only inside a
;; `TaskCancelOnDrop` drop handler, which never runs absent a cancellation — so they IMPORT the intrinsic
;; and never call it. Burroughs cannot cancel a task at all, and neither can wasmtime once one has started
;; (see asynclift/ABANDONMENT.md), so no composed artifact short of #862's reaches the branch either.
;;
;; Hence a hand-authored component whose async-lifted callee calls `task.cancel` IMMEDIATELY. The Canonical
;; ABI's rule is that this traps, and the trap is what the witness asserts: the opcode's uncancelled branch
;; is reachable and fires by name.
;;
;; Modelled on `async-lift-exit-synth.wat`'s shape: a memory module, a canon built-in lowered to a core
;; func, a core module importing it, and an async lift with a callback.
;;
;; Authored with `wasm-tools parse` 1.258.0. **The .wat is committed alongside the .wasm**, which the other
;; fixtures here do not do — regenerating one of those means re-authoring it from its prose description.
(component
  (core module $memmod (;0;)
    (memory (;0;) 1)
    (export "m" (memory 0))
  )
  (core instance $memi (;0;) (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm (;0;)))

  (type $rt (;0;) (func async (result u32)))

  ;; The subject: canon task.cancel, lowered to a core func the module below calls.
  (core func $taskcancel (;0;) (canon task.cancel))

  (core module $runmod (;1;)
    (type (;0;) (func))
    (type (;1;) (func (result i32)))
    (type (;2;) (func (param i32 i32 i32) (result i32)))
    (import "" "taskcancel" (func $taskcancel (;0;) (type 0)))
    (export "callee" (func 1))
    (export "cb" (func 2))
    ;; Calls task.cancel on a task nobody has cancelled. Per the Canonical ABI this traps, so the
    ;; `i32.const 0` after it is unreachable — kept so the function's declared result type is satisfied
    ;; and the module validates. A fixture that did not validate would be refused before reaching the
    ;; behaviour under test.
    (func (;1;) (type 1) (result i32)
      call $taskcancel
      i32.const 0
    )
    (func (;2;) (type 2) (param i32 i32 i32) (result i32)
      i32.const 0
    )
  )
  (core instance (;1;)
    (export "taskcancel" (func $taskcancel))
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
