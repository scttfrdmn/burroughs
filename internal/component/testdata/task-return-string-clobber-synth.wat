;; task-return-string-clobber-synth: the witness that `task.return` lifts its result EAGERLY (#903).
;;
;; # What it discriminates, and why nothing else could
;;
;; Burroughs stored `task.return`'s flat words on the lift task and lifted them after the callback loop
;; exited. For a `u32` that is indistinguishable from lifting eagerly — the value IS the word — so every
;; committed guest in this tree passed either way, and the defect was invisible. For a `string` the two
;; words are a `(ptr, len)` into guest memory, and between `task.return` and the loop's exit **the guest
;; runs again**. It may reuse that buffer. The late lift then reads whatever the guest wrote second.
;;
;; So this guest does exactly that, on purpose:
;;
;;   1. its result bytes — "burroughs" — sit at offset 1024, placed by a data segment;
;;   2. `callee` calls `task.return(1024, 9)`;
;;   3. it then **overwrites those nine bytes with 'X'**;
;;   4. it returns EXIT.
;;
;; An engine that lifts inside `task.return` (definitions.py:2336, the order the model uses) returns
;; "burroughs". An engine that lifts after the loop returns "XXXXXXXXX". Both are nine bytes of valid
;; UTF-8, so the failure is a **wrong value and not a decode error** — which is the stronger witness: an
;; engine could pass a UTF-8 check and still be reading the guest's scratch.
;;
;; # Why hand-authored
;;
;; `wit-bindgen` will not emit a guest that reuses its result buffer immediately after returning it — its
;; generated glue keeps the allocation alive precisely because real ABIs lift eagerly. The behaviour is
;; legal (the guest is done with the buffer the moment `task.return` has taken it) and no real toolchain
;; exercises it, which is the same reason `async-lift-yield-synth.wat` and `lift-cancel-yield-synth.wat`
;; are hand-written.
;;
;; # Two core modules, because the dependency is genuinely circular
;;
;; `task.return`'s canonopts name the memory it lifts from, and that memory is the guest's — but the guest
;; module imports `task.return`. One module cannot both provide the memory an import needs and receive
;; that import. So the memory and its data segment live in their own module, instantiated first; the
;; canon built-in is declared against it; and the code module imports both. This is the same shape a real
;; toolchain reaches for (wit-bindgen uses an indirection module for it) and it is structural, not a
;; workaround — the first draft put memory in the code module and `wasm-tools` refused with
;; `unknown core memory: failed to find name $memx`, which is the cycle reported honestly.
;;
;; # The callback is `unreachable`, and that is an assertion
;;
;; `callee` returns EXIT directly, so the callback must never be entered. A loop that re-entered it —
;; treating the packed 0 as a yield, say — traps here rather than producing a subtly different value.
;;
;; # Opts on BOTH the lift and the task.return
;;
;; The model requires them equal (`LiftOptions.equal(opts, task.opts)`, definitions.py:2334), so both
;; carry the same `(memory …)`. Burroughs does not yet check that equality; the fixture is written to the
;; model rather than to the engine, so it stays a valid specimen when the check lands.
;;
;; Authored with `wasm-tools parse` 1.258.0. The .wat is committed alongside the .wasm.
;; # Why there is a second export, `peek`
;;
;; Without it this fixture could pass **vacuously**: if the clobber loop never ran — a mis-assembled
;; branch, a loop that exited immediately — `run` would return "burroughs" for the trivial reason that
;; nothing had overwritten it, and the test would be green while exercising none of the window it exists
;; to exercise. `peek` returns the byte now at 1024 as a `u32`, so the test can assert that the buffer
;; **was** overwritten. "run returned the result" and "the buffer was overwritten" are then two
;; independent facts, and only their conjunction says the result was read before the overwrite.
(component
  (type $ft (;0;) (func async (result string)))
  (type $pt (;1;) (func async (result u32)))

  ;; The memory module: memory plus the result bytes. Instantiated before anything references it.
  (core module $memmod (;0;)
    (memory (;0;) (export "mem") 1)
    ;; Nine bytes, no NUL, all ASCII — so "the engine read the right range" and "the engine read valid
    ;; UTF-8" are separable failures.
    (data (;0;) (i32.const 1024) "burroughs")
  )
  (core instance $memi (;0;) (instantiate $memmod))
  (alias core export $memi "mem" (core memory $mem (;0;)))

  ;; task.return takes the lowered result: a string is two flat words, (ptr, byte-length), lifted from
  ;; the memory named here.
  (core func $taskret (;0;) (canon task.return (result string) (memory $mem)))
  ;; `peek`'s own task.return: a u32 result, so a separate built-in — a task.return's result type is
  ;; static and must match the lift it resolves (definitions.py:2333).
  (core func $taskretu32 (;1;) (canon task.return (result u32)))
  (core instance $ci (;1;)
    (export "taskret" (func $taskret))
    (export "taskretu32" (func $taskretu32))
  )

  (core module $m (;1;)
    (type $ret2 (;0;) (func (param i32 i32)))
    (type $calleet (;1;) (func (result i32)))
    (type $cbt (;2;) (func (param i32 i32 i32) (result i32)))

    (type $ret1 (;3;) (func (param i32)))
    (import "" "taskret" (func $taskret (;0;) (type $ret2)))
    (import "" "taskretu32" (func $taskretu32 (;1;) (type $ret1)))
    (import "mem" "mem" (memory (;0;) 1))

    (func $callee (;1;) (type $calleet) (result i32)
      (local $i i32)
      ;; Resolve: the result is the nine bytes at 1024.
      i32.const 1024
      i32.const 9
      call $taskret

      ;; Now reuse the buffer. This is the whole fixture: a guest is entitled to do this the moment
      ;; task.return has taken the value, and an engine that had not yet read it loses the result.
      i32.const 0
      local.set $i
      block $done
        loop $l
          local.get $i
          i32.const 9
          i32.ge_u
          br_if $done
          i32.const 1024
          local.get $i
          i32.add
          i32.const 0x58 ;; 'X'
          i32.store8
          local.get $i
          i32.const 1
          i32.add
          local.set $i
          br $l
        end
      end

      ;; EXIT (code 0): the task is already resolved, so the loop ends here.
      i32.const 0
    )

    ;; Never entered — see the header.
    (func $cb (;2;) (type $cbt) (param i32 i32 i32) (result i32)
      unreachable
    )

    ;; peek: resolve with the byte now at 1024, so a test can tell whether the clobber above ran.
    (func $peek (;3;) (type $calleet) (result i32)
      i32.const 1024
      i32.load8_u
      call $taskretu32
      i32.const 0
    )

    (export "callee" (func $callee))
    (export "cb" (func $cb))
    (export "peek" (func $peek))
  )
  (core instance $mi (;2;) (instantiate $m
      (with "" (instance $ci))
      (with "mem" (instance $memi))
    )
  )
  (alias core export $mi "callee" (core func $calleef (;2;)))
  (alias core export $mi "cb" (core func $cbf (;3;)))
  (alias core export $mi "peek" (core func $peekf (;4;)))
  (func (;0;) (type $ft) (canon lift (core func $calleef) async (callback $cbf) (memory $mem)))
  (func (;1;) (type $pt) (canon lift (core func $peekf) async (callback $cbf)))
  (export (;2;) "run" (func 0))
  (export (;3;) "peek" (func 1))
)
