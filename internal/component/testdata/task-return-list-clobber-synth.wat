;; task-return-list-clobber-synth: the witness that `task.return` lifts a LIST result eagerly (#902).
;;
;; The sibling of `task-return-string-clobber-synth.wat`, and the same proof for the kind whose framing
;; is more than a byte range. Read that fixture's header first; what follows is only what differs.
;;
;; ## What a list adds over a string
;;
;; A string's two flat words are `(ptr, byte-length)`. A list's are `(ptr, COUNT)` — the same shape
;; meaning a different thing, so an engine that treated the second word as a byte length would lift four
;; elements as one, and one that treated it as a count for a string would read four times too many bytes.
;; Separate arms, and this fixture is the one that fails if they are merged.
;;
;; The element stride is also real here: `u32` is four bytes at alignment 4, where a string's element is
;; one byte at alignment 1. So a lift that got the stride wrong returns the right NUMBER of elements with
;; the wrong values — which is why the test asserts the values and not just the length.
;;
;; ## The clobber, and what its conjunction proves
;;
;; `run` resolves with the four u32s at 1024 — 10, 20, 30, 40 — and then overwrites all sixteen bytes
;; with 0xFF. An engine that lifted eagerly returns [10, 20, 30, 40]; one that lifted after the loop
;; returns [0xFFFFFFFF × 4]. Both are four well-formed u32s, so neither outcome is a crash and only the
;; values tell them apart.
;;
;; `peek` reports the first word as it stands afterwards, so "the embedder got the right elements" and
;; "the guest did overwrite them" are independent facts. Without `peek` this fixture could pass
;; **vacuously**: a mis-assembled clobber loop that never ran would leave the original bytes in place and
;; the eager-lift assertion would hold for the wrong reason.
;;
;; The clobber value is 0xFF bytes rather than a small number on purpose: 0xFFFFFFFF is also the
;; "sign-extended" wrong answer, so a lift that narrowed a u32 incorrectly lands on the same value the
;; stale read would give, and the test cannot mistake one defect for the other — it asserts the
;; originals, which neither defect produces.
;;
;; Authored with `wasm-tools parse` 1.258.0. The .wat is committed alongside the .wasm.
(component
  (type $lu32 (;0;) (list u32))
  (type $ft (;1;) (func async (result $lu32)))
  (type $pt (;2;) (func async (result u32)))

  ;; The memory module: memory plus the element backing. Four u32s, little-endian, at a 4-aligned
  ;; address — 1024 is a multiple of 4, which `load_list`'s alignment trap (definitions.py:1715)
  ;; requires and which is NOT vacuous for a u32 the way it is for a string's bytes.
  ;;
  ;; 10, 20, 30, 40 = 0x0a, 0x14, 0x1e, 0x28. Distinct, non-zero, and none of them a byte that repeats
  ;; across words, so a stride error cannot produce a plausible-looking sequence.
  (core module $memmod (;0;)
    (memory (;0;) (export "mem") 1)
    (data (;0;) (i32.const 1024) "\0a\00\00\00\14\00\00\00\1e\00\00\00\28\00\00\00")
  )
  (core instance $memi (;0;) (instantiate $memmod))
  (alias core export $memi "mem" (core memory $mem (;0;)))

  ;; task.return takes the lowered result: a list is two flat words, (ptr, count), lifted from the
  ;; memory named here.
  (core func $taskret (;0;) (canon task.return (result $lu32) (memory $mem)))
  ;; peek's own task.return: a u32 result, so a separate built-in — a task.return's result type is
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
      ;; Resolve: the result is the FOUR ELEMENTS at 1024. The second word is a count, not a byte
      ;; length — sixteen bytes of data, four elements.
      i32.const 1024
      i32.const 4
      call $taskret

      ;; Now reuse the backing. A guest is entitled to do this the moment task.return has taken the
      ;; value, and an engine that had not yet read it loses the elements.
      i32.const 0
      local.set $i
      block $done
        loop $l
          local.get $i
          i32.const 16
          i32.ge_u
          br_if $done
          i32.const 1024
          local.get $i
          i32.add
          i32.const 0xff
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

    ;; Never entered: the callee resolves and exits without ever returning a WAIT, so the event loop has
    ;; nothing to deliver. `unreachable` rather than a stub return, so an engine that DID call it fails
    ;; loudly instead of being quietly tolerated.
    (func $cb (;2;) (type $cbt) (param i32 i32 i32) (result i32)
      unreachable
    )

    ;; peek: resolve with the first word as it stands now, so a test can tell whether the clobber ran.
    (func $peek (;3;) (type $calleet) (result i32)
      i32.const 1024
      i32.load
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
