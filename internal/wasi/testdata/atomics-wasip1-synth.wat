(module
  (import "wasi_snapshot_preview1" "proc_exit" (func $exit (param i32)))
  (memory 1 1)
  (func (export "_start")
    ;; The ONLY gated construct: one 0xFE-region atomic RMW. The memory is deliberately NOT shared,
    ;; so the threads gate's other half (the limits shared bit) cannot be what refuses this module —
    ;; which keeps the refusal message discriminating for the atomics region specifically.
    i32.const 0
    i32.const 1
    i32.atomic.rmw.add
    drop
    i32.const 0
    call $exit)
)
