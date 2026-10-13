;; run-slot: two concurrent sync-lift calls into one instance, each woken from waitable-set.wait and then
;; reaching a probe import while executing (#975).
;;
;; Derived from async-waitset-synth.wasm (printed with wasm-tools 1.258.0): `run` async-lowers `op`, joins
;; the subtask to a waitable set and waits on it, exactly as before — then calls `op` again. The second
;; call is the probe. `TestTwoCallsIntoOneInstanceNeverExecuteAtOnce` resolves it inside the impl, so it
;; executes in the caller's canon call, and holds the first caller there for a bound while it watches
;; for the second.
;;
;; The reference runs one thread at a time (`Store.tick` resumes one; `Thread.resume` runs until it
;; suspends), so the second caller can never reach the probe while the first is inside it.
;;
;; `(memory 1 1)`: max equal to initial, so the memory is on the Go heap and `-race` can see it (#979). A
;; memory with room to grow is an mmap reservation on unix, outside the detector's shadow.
(component
  (core module $memmod (;0;)
    (memory (;0;) 1 1)
    (export "m" (memory 0))
  )
  (core instance $memi (;0;) (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm (;0;)))
  (type (;0;)
    (instance
      (type (;0;) (func async (param "x" u32) (result u32)))
      (export (;0;) "op" (func (type 0)))
    )
  )
  (import "test:async/ops" (instance $ops (;0;) (type 0)))
  (alias export $ops "op" (func $impf (;0;)))
  (core func $lowered (;0;) (canon lower (func $impf) async (memory $cm)))
  (core func $wsnew (;1;) (canon waitable-set.new))
  (core func $wsjoin (;2;) (canon waitable.join))
  (core func $wswait (;3;) (canon waitable-set.wait (memory $cm)))
  (core module $runmod (;1;)
    (type (;0;) (func (param i32 i32) (result i32)))
    (type (;1;) (func (result i32)))
    (type (;2;) (func (param i32 i32)))
    (import "" "mem" (memory (;0;) 1))
    (import "" "lower" (func $lower (;0;) (type 0)))
    (import "" "wsnew" (func $wsnew (;1;) (type 1)))
    (import "" "wsjoin" (func $wsjoin (;2;) (type 2)))
    (import "" "wswait" (func $wswait (;3;) (type 0)))
    (export "run" (func 4))
    (export "sibling" (func 5))
    (func (;4;) (type 1) (result i32)
      (local $packed i32) (local $subtaski i32) (local $si i32)
      i32.const 7
      i32.const 0
      call $lower
      local.set $packed
      local.get $packed
      i32.const 2
      i32.eq
      if ;; label = @1
        i32.const 0
        i32.load
        return
      end
      local.get $packed
      i32.const 4
      i32.shr_u
      local.set $subtaski
      call $wsnew
      local.set $si
      local.get $subtaski
      local.get $si
      call $wsjoin
      local.get $si
      i32.const 8
      call $wswait
      drop
      ;; After the wake, a second call to the same import: the PROBE. The test's impl resolves it before
      ;; returning, so it runs inline in this guest's canon call, while this caller is executing — and
      ;; it is where the test asks whether the other caller is executing too.
      i32.const 7
      i32.const 0
      call $lower
      drop
      i32.const 0
      i32.load
    )
    (func (;5;) (type 1) (result i32)
      i32.const 42
    )
  )
  (core instance (;1;)
    (export "mem" (memory $cm))
    (export "lower" (func $lowered))
    (export "wsnew" (func $wsnew))
    (export "wsjoin" (func $wsjoin))
    (export "wswait" (func $wswait))
  )
  (core instance $runi (;2;) (instantiate $runmod
      (with "" (instance 1))
    )
  )
  (alias core export $runi "run" (core func $runf (;4;)))
  (alias core export $runi "sibling" (core func $sibf (;5;)))
  (type (;1;) (func (result u32)))
  (func (;1;) (type 1) (canon lift (core func $runf)))
  (type (;2;) (func (result u32)))
  (func (;2;) (type 2) (canon lift (core func $sibf)))
  (export (;3;) "run" (func 1))
  (export (;4;) "sibling" (func 2))
)
