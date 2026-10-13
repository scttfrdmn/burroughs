;; realloc-wait: a `cabi_realloc` that calls `waitable-set.wait` (#990). The reference's
;; `LiftLowerContext.reallocate` sets `may_leave = False` for the realloc's duration, and
;; `canon_waitable_set_wait` opens with `trap_if(not thread.task.inst.may_leave)`, so the call traps.
;;
;; Two exports, because the realloc is reached two ways and Burroughs implements them at two sites:
;;
;;   * `arg(s)` — an async lift with a `string` PARAMETER. The embedder lowers `s` into the guest through
;;     the lift's realloc (`interp.Instance.InvokeMayNotLeave`, from lowerargs).
;;   * `res()` — an async lift that calls a nested child's `get() -> string` through an async lower whose
;;     realloc is the waiting one, so lowering the RESULT calls it (`interp.CanonCaller.Realloc`).
;;
;; And one control, `ctl(s)`, whose realloc calls `context.set` — one of the five built-ins the reference
;; does NOT check — so the guard is shown not to over-trap: it returns the string's length, 2.
;;
;; The wait names set 0, which is no set: an engine that skipped the may_leave check would still trap,
;; but on the handle (Burroughs: "handle 0 is not a waitable set"), and the witness tells the two apart
;; by reason. Every lift and lower here is async: the sync shapes hit #995 and Burroughs's refusal of a
;; sync lift whose arguments need guest memory, neither of which is this fixture's subject.
;;
;; Authored with `wasm-tools parse`; the reading is in realloc-wait.reading.
(component
  (component $child
    (core module $cmem (memory (export "m") 1 1) (data (i32.const 100) "abc"))
    (core instance $cmemi (instantiate $cmem))
    (alias core export $cmemi "m" (core memory $chm))
    (core func $ret (canon task.return (result string) (memory $chm)))
    (core module $cm
      (import "" "ret" (func $ret (param i32 i32)))
      ;; get: returns the string "abc" at 100 and exits
      (func (export "get") (result i32) (call $ret (i32.const 100) (i32.const 3)) (i32.const 0))
      (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
    (core instance $ci (instantiate $cm (with "" (instance (export "ret" (func $ret))))))
    (alias core export $ci "get" (core func $getf))
    (alias core export $ci "cb" (core func $cbf))
    (type $gt (func async (result string)))
    (func (export "get") (type $gt) (canon lift (core func $getf) async (callback $cbf) (memory $chm)))
  )
  (instance $c (instantiate $child))
  (alias export $c "get" (func $get))

  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $pm))

  (core func $wswait (canon waitable-set.wait (memory $pm)))
  (core func $ctxset (canon context.set i32 0))
  (core module $allocmod
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (import "" "ctxset" (func $ctxset (param i32)))
    (global $bump (mut i32) (i32.const 1024))
    (func $bumpit (param i32) (result i32)
      (local $p i32)
      (local.set $p (global.get $bump))
      (global.set $bump (i32.add (global.get $bump) (local.get 0)))
      (local.get $p))
    ;; rwait: waits on set 0 before allocating; the reference traps at the wait's first line
    (func (export "rwait") (param i32 i32 i32 i32) (result i32)
      (drop (call $wswait (i32.const 0) (i32.const 64)))
      (call $bumpit (local.get 3)))
    ;; rctx: calls an unchecked built-in, then allocates
    (func (export "rctx") (param i32 i32 i32 i32) (result i32)
      (call $ctxset (i32.const 5))
      (call $bumpit (local.get 3))))
  (core instance $alloci (instantiate $allocmod
    (with "" (instance (export "wswait" (func $wswait)) (export "ctxset" (func $ctxset))))))
  (alias core export $alloci "rwait" (core func $rwait))
  (alias core export $alloci "rctx" (core func $rctx))

  (core func $getlow (canon lower (func $get) async (memory $pm) (realloc $rwait)))
  (core func $ret (canon task.return (result u32)))
  (core module $runmod
    (import "" "get" (func $get (param i32) (result i32)))
    (import "" "ret" (func $ret (param i32)))
    (import "" "mem" (memory 1 1))
    ;; arg / ctl: return the string's length
    (func (export "len") (param i32 i32) (result i32) (call $ret (local.get 1)) (i32.const 0))
    ;; res: get() into retptr 16; it resolves at once, so return its length
    (func (export "res") (result i32)
      (drop (call $get (i32.const 16)))
      (call $ret (i32.load (i32.const 20)))
      (i32.const 0))
    (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
  (core instance $ri (instantiate $runmod
    (with "" (instance (export "get" (func $getlow)) (export "ret" (func $ret)) (export "mem" (memory $pm))))))
  (alias core export $ri "len" (core func $lenf))
  (alias core export $ri "res" (core func $resf))
  (alias core export $ri "cb" (core func $cbf))
  (type $st (func async (param "s" string) (result u32)))
  (type $rt (func async (result u32)))
  (func (export "arg") (type $st) (canon lift (core func $lenf) async (callback $cbf) (memory $pm) (realloc $rwait)))
  (func (export "ctl") (type $st) (canon lift (core func $lenf) async (callback $cbf) (memory $pm) (realloc $rctx)))
  (func (export "res") (type $rt) (canon lift (core func $resf) async (callback $cbf)))
)
