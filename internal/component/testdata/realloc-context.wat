;; realloc-context (ADR 0119): whose context does a lower's realloc see? `run` sets its context slot 0 to
;; 12345 and calls the host's get-random-bytes(4), which allocates the list through the lower's realloc. The
;; realloc records the context.get(0) it sees at address 0, then sets slot 0 to 0xdead. `run` returns
;; (its own context.get(0) after the call) << 16 | (what the realloc saw), each below 2^16.
;;
;; In the pin, `reallocate` lifts the realloc and invokes it as a call of its own, on a new Thread whose
;; storage starts at [0, 0], so the realloc sees 0 and run's slot survives: 12345 << 16 | 0. A realloc run in
;; the caller's context sees 12345 and leaves 0xdead behind: 0xdead << 16 | 12345.
(component
  (import "wasi:random/random@0.3.0" (instance $random
    (export "get-random-bytes" (func (param "max-len" u64) (result (list u8))))))
  (alias export $random "get-random-bytes" (func $grb))

  (core func $cget (canon context.get i32 0))
  (core func $cset (canon context.set i32 0))

  ;; The memory and the realloc, instantiated first so the lower can name them.
  (core module $alloc
    (import "" "cget" (func $cget (result i32)))
    (import "" "cset" (func $cset (param i32)))
    (memory (export "mem") 1)
    (global $bump (mut i32) (i32.const 1024))
    (func (export "realloc") (param i32 i32 i32 i32) (result i32)
      (local $p i32)
      (i32.store (i32.const 0) (call $cget))
      (call $cset (i32.const 0xdead))
      (local.set $p (global.get $bump))
      (global.set $bump (i32.add (global.get $bump) (local.get 3)))
      (local.get $p)))
  (core instance $a (instantiate $alloc
    (with "" (instance (export "cget" (func $cget)) (export "cset" (func $cset))))))
  (alias core export $a "mem" (core memory $mem))
  (alias core export $a "realloc" (core func $realloc))

  (core func $grbl (canon lower (func $grb) (memory $mem) (realloc $realloc)))

  (core module $main
    (import "" "mem" (memory 1))
    (import "" "cget" (func $cget (result i32)))
    (import "" "cset" (func $cset (param i32)))
    (import "" "grb" (func $grb (param i64 i32)))
    (func (export "run") (result i32)
      (call $cset (i32.const 12345))
      (call $grb (i64.const 4) (i32.const 16))
      (i32.or
        (i32.shl (call $cget) (i32.const 16))
        (i32.load (i32.const 0)))))
  (core instance $m (instantiate $main
    (with "" (instance
      (export "mem" (memory $mem))
      (export "cget" (func $cget))
      (export "cset" (func $cset))
      (export "grb" (func $grbl))))))

  (func (export "run") (result u32) (canon lift (core func $m "run"))))
