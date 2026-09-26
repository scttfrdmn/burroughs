(module
  (import "burroughs" "spawn" (func $spawn (param i32) (result i32)))
  (memory (export "memory") 1 1 shared)

  ;; The spawned agent does a little bounded work and RETURNS CLEANLY. This is T-5's ordinary
  ;; per-thread exit: the instance must carry on.
  (func (export "mstart") (param i32)
    (drop (memory.atomic.wait32 (i32.const 64) (i32.const 0) (i64.const 50000000)))
  )

  ;; The invoking agent finishes its own work and returns normally.
  (func (export "_start")
    (drop (call $spawn (i32.const 0)))
    (drop (memory.atomic.wait32 (i32.const 68) (i32.const 0) (i64.const 300000000)))
  )
)
