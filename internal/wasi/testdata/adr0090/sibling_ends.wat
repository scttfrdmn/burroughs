(module
  (import "wasi_snapshot_preview1" "proc_exit" (func $proc_exit (param i32)))
  (import "burroughs" "spawn" (func $spawn (param i32) (result i32)))
  (memory (export "memory") 1 1 shared)

  ;; What a spawned agent runs. The arg selects the arm: 0 = proc_exit, 1 = trap.
  ;; It first parks on a never-notified address with a bounded timeout, so the INVOKING agent
  ;; has reached its own infinite wait before this agent ends. Either ordering hangs, but this
  ;; one puts the invoker in the exact state #819 observed.
  (func (export "mstart") (param i32)
    (drop (memory.atomic.wait32 (i32.const 64) (i32.const 0) (i64.const 200000000)))
    (if (i32.eq (local.get 0) (i32.const 0))
      (then (call $proc_exit (i32.const 7)))
      (else (unreachable)))
  )

  ;; The invoking agent: spawn a sibling, then park in an infinite atomic.wait32 with NO waker.
  (func (export "_start")
    (drop (call $spawn (i32.load (i32.const 0))))
    (drop (memory.atomic.wait32 (i32.const 4) (i32.const 0) (i64.const -1)))
  )
)
