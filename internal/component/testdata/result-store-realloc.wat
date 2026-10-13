;; result-store-realloc: an async lower whose result is a STRING, so lowering it calls the caller's
;; `cabi_realloc`, which is guest code (ADR 0101's result-store amendment). The impl resolves inline, so the
;; store is queued while the run slot's holder is in host code and applied as it leaves (`leaveHost`).
;;
;; `realloc` is a pure bump allocator, legal under the reference's `may_leave = False` (it calls nothing),
;; with a long spin, so a witness can ask whether an engine lock is held while it runs.
(component
  (type $opsit (instance
    (type $opt (func async (param "x" u32) (result string)))
    (export "op" (func (type $opt)))))
  (import "test:async/ops" (instance $ops (type $opsit)))
  (alias export $ops "op" (func $op))
  (core module $mm
    (memory (export "m") 1 1)
    (global $bump (mut i32) (i32.const 1024))
    (func (export "realloc") (param i32 i32 i32 i32) (result i32)
      (local $p i32) (local $i i32)
      ;; spin: long enough to observe what is held while guest code runs
      (block $done (loop $again
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br_if $again (i32.lt_u (local.get $i) (i32.const 3000000)))))
      (local.set $p (global.get $bump))
      (global.set $bump (i32.add (global.get $bump) (local.get 3)))
      (local.get $p)))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $cm))
  (alias core export $mi "realloc" (core func $rf))
  (core func $lowered (canon lower (func $op) async (memory $cm) (realloc $rf)))
  (core module $runmod
    (import "" "mem" (memory 1))
    (import "" "lower" (func $lower (param i32 i32) (result i32)))
    ;; run: op(7) into retptr 16; it resolves inline (packed == 2), so the string is already lowered:
    ;; return its length, the second word of the (ptr, len) pair.
    (func (export "run") (result i32)
      (drop (call $lower (i32.const 7) (i32.const 16)))
      (i32.load (i32.const 20))))
  (core instance $ri (instantiate $runmod
    (with "" (instance (export "mem" (memory $cm)) (export "lower" (func $lowered))))))
  (alias core export $ri "run" (core func $runf))
  (type $rt (func (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf)))
)
