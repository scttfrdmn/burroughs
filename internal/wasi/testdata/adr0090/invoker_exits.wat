(module
  (import "wasi_snapshot_preview1" "proc_exit" (func $proc_exit (param i32)))
  (import "burroughs" "spawn" (func $spawn (param i32) (result i32)))
  (memory (export "memory") 1 1 shared)

  ;; The SPAWNED agent parks forever with no waker. The mirror of probe 1: there, the invoker
  ;; parked and the sibling exited; here the sibling parks and the INVOKER exits.
  (func (export "mstart") (param i32)
    (drop (memory.atomic.wait32 (i32.const 4) (i32.const 0) (i64.const -1)))
  )

  (func (export "_start")
    (drop (call $spawn (i32.const 0)))
    ;; Give the sibling time to reach its wait, so the exit lands with an agent genuinely parked.
    (drop (memory.atomic.wait32 (i32.const 64) (i32.const 0) (i64.const 200000000)))
    (call $proc_exit (i32.const 7))
  )
)
