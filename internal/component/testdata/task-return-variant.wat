;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; task-return-variant (#1320): async-lifted exports whose `task.return` carries a variant-family value, one
;; export per shape, so each reading names one rule of def:lift_flat_variant.
;;
;; The four result types and the flat parameters each one's task.return takes (def:flatten_variant):
;;
;;   $ou64  option<u64>                            [i32, i64]       some's u64 is the payload slot
;;   $v     variant { a(u32), b(f64), c(string) }  [i32, i64, i32]  slot 1 is join(i32, f64, i32) = i64
;;   $n     result<option<u8>, string>             [i32, i32, i32]  ok's [i32, i32] joined with err's
;;   $e     enum { x, y, z }                       [i32]            the case index alone
;;
;; What each export hands task.return, and the rule it reaches:
;;
;;   opt-some        1, 0x1_0000_0002              some's u64 keeps all 64 bits of an i64 slot
;;   opt-none        0, 0x7777                     an unused slot is drained, never read as a value
;;   mixed-a         0, 0xFFFFFFFF_DEADBEEF, 5     an i32 payload in an i64 slot is wrapped to its low 32 bits
;;   mixed-b         1, bits of pi, 0              an f64 payload in an i64 slot is the slot's bits reinterpreted
;;   mixed-c         2, 1024, 9                    a string's pointer in an i64 slot, its length in the i32 one
;;   nested-ok-some  0, 1, 42                      the case's payload is itself a variant, lifted from the joined slots
;;   nested-ok-none  0, 0, 0x99                    the inner none drains the slot the inner some would use
;;   nested-err      1, 1024, 9                    the other case of the same joined slots
;;   enum-y          1                             an enum is a variant of payload-free cases
;;   bad-case        3, 0, 0                       def:lift_flat_variant:"trap_if(case_index >= len(cases))"
;;
;; The variant and the enum are nominal, so their types are exported and the functions exported over the
;; exported indices, record-synth.wat's shape. option and result are structural and need neither. The
;; callback is never entered: every export calls task.return and EXITs at once, so `unreachable` makes an
;; engine that did enter it fail loudly.
;;
;; Assembled with `wasm-tools parse task-return-variant.wat -o task-return-variant.wasm` (wasm-tools 1.258.0;
;; validates with --features all). task-return-variant.reading is wasmtime 49.0.3's reading for each export.
(component
  (type $ou64 (option u64))
  (type $v (variant (case "a" u32) (case "b" f64) (case "c" string)))
  (type $n (result (option u8) (error string)))
  (type $e (enum "x" "y" "z"))

  ;; The memory module comes first, because the task.return built-ins whose results carry a string name the
  ;; memory the string is read from.
  (core module $memmod
    (memory (export "mem") 1)
    (data (i32.const 1024) "burroughs"))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "mem" (core memory $mem))

  (core func $retou64 (canon task.return (result $ou64)))
  (core func $retv (canon task.return (result $v) (memory $mem)))
  (core func $retn (canon task.return (result $n) (memory $mem)))
  (core func $rete (canon task.return (result $e)))
  (core instance $ci
    (export "ou64" (func $retou64))
    (export "v" (func $retv))
    (export "n" (func $retn))
    (export "e" (func $rete)))

  (core module $m
    (import "" "ou64" (func $ou64 (param i32 i64)))
    (import "" "v" (func $v (param i32 i64 i32)))
    (import "" "n" (func $n (param i32 i32 i32)))
    (import "" "e" (func $e (param i32)))
    (func (export "opt-some") (result i32)
      (call $ou64 (i32.const 1) (i64.const 0x1_0000_0002)) (i32.const 0))
    (func (export "opt-none") (result i32)
      (call $ou64 (i32.const 0) (i64.const 0x7777)) (i32.const 0))
    (func (export "mixed-a") (result i32)
      (call $v (i32.const 0) (i64.const 0xFFFFFFFF_DEADBEEF) (i32.const 5)) (i32.const 0))
    (func (export "mixed-b") (result i32)
      (call $v (i32.const 1) (i64.reinterpret_f64 (f64.const 3.141592653589793)) (i32.const 0)) (i32.const 0))
    (func (export "mixed-c") (result i32)
      (call $v (i32.const 2) (i64.const 1024) (i32.const 9)) (i32.const 0))
    (func (export "nested-ok-some") (result i32)
      (call $n (i32.const 0) (i32.const 1) (i32.const 42)) (i32.const 0))
    (func (export "nested-ok-none") (result i32)
      (call $n (i32.const 0) (i32.const 0) (i32.const 0x99)) (i32.const 0))
    (func (export "nested-err") (result i32)
      (call $n (i32.const 1) (i32.const 1024) (i32.const 9)) (i32.const 0))
    (func (export "enum-y") (result i32)
      (call $e (i32.const 1)) (i32.const 0))
    (func (export "bad-case") (result i32)
      (call $v (i32.const 3) (i64.const 0) (i32.const 0)) (i32.const 0))
    (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
  (core instance $mi (instantiate $m (with "" (instance $ci))))

  (func $opt-some async (result $ou64) (canon lift (core func $mi "opt-some") async (callback (func $mi "cb"))))
  (func $opt-none async (result $ou64) (canon lift (core func $mi "opt-none") async (callback (func $mi "cb"))))
  ;; A lift whose result carries a string names the memory too, though the string crosses through task.return:
  ;; wasm-tools 1.258.0 refuses these seven without it ("canonical option `memory` is required").
  (func $mixed-a async (result $v)
    (canon lift (core func $mi "mixed-a") async (callback (func $mi "cb")) (memory $mem)))
  (func $mixed-b async (result $v)
    (canon lift (core func $mi "mixed-b") async (callback (func $mi "cb")) (memory $mem)))
  (func $mixed-c async (result $v)
    (canon lift (core func $mi "mixed-c") async (callback (func $mi "cb")) (memory $mem)))
  (func $nested-ok-some async (result $n)
    (canon lift (core func $mi "nested-ok-some") async (callback (func $mi "cb")) (memory $mem)))
  (func $nested-ok-none async (result $n)
    (canon lift (core func $mi "nested-ok-none") async (callback (func $mi "cb")) (memory $mem)))
  (func $nested-err async (result $n)
    (canon lift (core func $mi "nested-err") async (callback (func $mi "cb")) (memory $mem)))
  (func $enum-y async (result $e) (canon lift (core func $mi "enum-y") async (callback (func $mi "cb"))))
  (func $bad-case async (result $v)
    (canon lift (core func $mi "bad-case") async (callback (func $mi "cb")) (memory $mem)))

  (export $vx "v" (type $v))
  (export $ex "e" (type $e))
  (type $vft (func async (result $vx)))
  (type $eft (func async (result $ex)))
  (export "opt-some" (func $opt-some))
  (export "opt-none" (func $opt-none))
  (export "mixed-a" (func $mixed-a) (func (type $vft)))
  (export "mixed-b" (func $mixed-b) (func (type $vft)))
  (export "mixed-c" (func $mixed-c) (func (type $vft)))
  (export "nested-ok-some" (func $nested-ok-some))
  (export "nested-ok-none" (func $nested-ok-none))
  (export "nested-err" (func $nested-err))
  (export "enum-y" (func $enum-y) (func (type $eft)))
  (export "bad-case" (func $bad-case) (func (type $vft))))
