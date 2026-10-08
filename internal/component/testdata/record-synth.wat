;; record-synth: a record crosses the component boundary in BOTH directions (#904, ADR 0097 item 5).
;;
;; ## Why this fixture has a shape the string and list ones did not
;;
;; **A record is nominal in the component model.** `tuple u32 u32` validates as an exported function's
;; parameter; `record (field "a" u32) (field "b" u32)` gives *"func not valid to be used as export"* —
;; same core module, same lift, one token different. A record referenced by an exported function must
;; have its own type exported too, which the inline form the `string` and `list` fixtures use cannot do.
;;
;; So the shape here is the one `wit-component` generates, learned by generating a component from a
;; `.wit` with `wasm-tools component embed --dummy` + `component new` and reading the result back:
;;
;;   1. declare the record type;
;;   2. `(export "pair" (type $r))`, which yields an **exported** type index;
;;   3. declare a function type over *that* index;
;;   4. export the function with that type **ascribed**: `(export "f" (func 0) (func (type $ft2)))`.
;;
;; Step 4 is the condition: an exported function's type must reference only exportable types.
;;
;; ## What each export is for
;;
;; `addrec` takes a `pair { a: u32, b: u32 }` and returns their sum. A record of two u32s flattens to
;; two i32 words and needs **no memory at all**, which makes it the clean test of the parameter
;; direction: field order and the flat lowering, with nothing else involved. A caller that swapped the
;; fields would still get a sum, so the test uses values whose sum identifies the order.
;;
;; `makerec` returns a `named { name: string, n: u32 }` and is the RESULT direction, with the
;; clobber-after-return shape. The string field is why the record has one: a record of scalars flattens
;; to words and leaves nothing in memory to overwrite, so there would be no eager-lift claim to make.
;; With a string field the record's payload lives at 1024, the guest resolves and then overwrites those
;; bytes, and an engine that lifted after the guest resumed hands back "XXXXXXXXX" instead of
;; "burroughs" — nine bytes of valid UTF-8 either way, so only the content separates them.
;;
;; `peek` reports the byte now at 1024, so "the embedder got the right string" and "the guest did
;; overwrite it" are independent facts rather than one assumed from the other. Without it a clobber loop
;; that never ran would satisfy the eager assertion for the wrong reason.
;;
;; Authored with `wasm-tools parse` 1.258.0. The .wat is committed alongside the .wasm.
(component
  (type $pair (;0;) (record (field "a" u32) (field "b" u32)))
  (type $named (;1;) (record (field "name" string) (field "n" u32)))

  (type $addt (;2;) (func async (param "p" $pair) (result u32)))
  (type $maket (;3;) (func async (result $named)))
  (type $peekt (;4;) (func async (result u32)))

  ;; The memory module comes FIRST, because `canon task.return (result $named)` names the memory its
  ;; string field is read from and an alias must exist before it is referenced.
  (core module $memmod (;0;)
    (memory (;0;) (export "mem") 1)
    ;; Nine bytes, no NUL, all ASCII — so "the engine read the right range" and "the engine read valid
    ;; UTF-8" stay separable failures.
    (data (;0;) (i32.const 1024) "burroughs")
  )
  (core instance $memi (;0;) (instantiate $memmod))
  (alias core export $memi "mem" (core memory $mem (;0;)))

  ;; Two task.return built-ins: a task.return's result type is static and must match the lift it
  ;; resolves (definitions.py:2333), so a u32 result and a record result cannot share one.
  (core func $retu32 (;0;) (canon task.return (result u32)))
  (core func $retnamed (;1;) (canon task.return (result $named) (memory $mem)))
  (core instance $ci (;1;)
    (export "retu32" (func $retu32))
    (export "retnamed" (func $retnamed))
  )

  (core module $m (;1;)
    (type $ret1 (;0;) (func (param i32)))
    (type $ret3 (;1;) (func (param i32 i32 i32)))
    (type $add2 (;2;) (func (param i32 i32) (result i32)))
    (type $none (;3;) (func (result i32)))
    (type $cbt (;4;) (func (param i32 i32 i32) (result i32)))

    (import "" "retu32" (func $retu32 (;0;) (type $ret1)))
    (import "" "retnamed" (func $retnamed (;1;) (type $ret3)))
    (import "mem" "mem" (memory (;0;) 1))

    ;; addrec: the record's two u32 fields arrive as two flat words, in DECLARED order.
    (func $addrec (;2;) (type $add2) (param $a i32) (param $b i32) (result i32)
      local.get $a
      local.get $b
      i32.add
      call $retu32
      i32.const 0)

    ;; makerec: resolve with { name: "burroughs" at 1024, n: 42 }, then overwrite the string's bytes.
    ;; A record with a string field flattens to (ptr, len, n) — three words.
    (func $makerec (;3;) (type $none) (result i32)
      (local $i i32)
      i32.const 1024
      i32.const 9
      i32.const 42
      call $retnamed

      ;; The buffer is the guest's to reuse the moment task.return has taken the value. An engine that
      ;; had not yet read the bytes loses the string.
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
      i32.const 0)

    ;; peek: the byte now at 1024, so a test can tell whether the clobber above ran.
    (func $peek (;4;) (type $none) (result i32)
      i32.const 1024
      i32.load8_u
      call $retu32
      i32.const 0)

    ;; Never entered: each callee resolves and exits without ever returning a WAIT, so the event loop
    ;; has nothing to deliver. `unreachable` rather than a stub, so an engine that DID call it fails
    ;; loudly instead of being quietly tolerated.
    (func $cb (;5;) (type $cbt) (param i32 i32 i32) (result i32)
      unreachable)

    (export "addrec" (func $addrec))
    (export "makerec" (func $makerec))
    (export "peek" (func $peek))
    (export "cb" (func $cb))
  )
  (core instance $mi (;2;) (instantiate $m
      (with "" (instance $ci))
      (with "mem" (instance $memi))
    )
  )
  (alias core export $mi "addrec" (core func $addf (;2;)))
  (alias core export $mi "makerec" (core func $makef (;3;)))
  (alias core export $mi "peek" (core func $peekf (;4;)))
  (alias core export $mi "cb" (core func $cbf (;5;)))

  (func (;0;) (type $addt) (canon lift (core func $addf) async (callback $cbf) (memory $mem)))
  (func (;1;) (type $maket) (canon lift (core func $makef) async (callback $cbf) (memory $mem)))
  (func (;2;) (type $peekt) (canon lift (core func $peekf) async (callback $cbf) (memory $mem)))

  ;; The nominal-record dance: export each record type, then export each function with a type ascribed
  ;; over the EXPORTED type index. See the header.
  (export $pairx (;5;) "pair" (type $pair))
  (export $namedx (;6;) "named" (type $named))
  (type $addt2 (;7;) (func async (param "p" $pairx) (result u32)))
  (type $maket2 (;8;) (func async (result $namedx)))
  (export (;3;) "addrec" (func 0) (func (type $addt2)))
  (export (;4;) "makerec" (func 1) (func (type $maket2)))
  (export (;5;) "peek" (func 2))
)
