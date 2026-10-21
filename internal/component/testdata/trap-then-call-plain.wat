;; trap-then-call-plain (#1040): trap-then-call with NO Threads, which is what showed the divergence is the
;; whole default path's and not cm-threads's. `run` is a bare `unreachable`; `ok` returns 7. The host calls
;; `run`, then `ok`, on one instance.
;;
;; wasmtime 49.0.3 (the two-call host, cmthreads/twocall): call 1 traps, and call 2 is refused with "cannot
;; enter component instance". The pin locks the store on the trap. Before #1040's repair, Burroughs ran call 2
;; and returned 7.
(component
  (core module $m
    (func (export "run") (result i32) unreachable)
    (func (export "ok") (result i32) (i32.const 7)))
  (core instance $mi (instantiate $m))
  (alias core export $mi "run" (core func $runf))
  (alias core export $mi "ok" (core func $okf))
  (type $ft (func (result u32)))
  (func (export "run") (type $ft) (canon lift (core func $runf)))
  (func (export "ok") (type $ft) (canon lift (core func $okf)))
)
