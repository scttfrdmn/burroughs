;; cli-run-trap (#1084): the smallest component exporting `wasi:cli/run`, whose `run` traps (`unreachable`). It is
;; the CLI's case: `burroughs run` on a component that traps must exit 4 (exitTrap), the code a core module's trap
;; already gets. No imports, so it runs on the stub host.
;;
;; wasmtime 49.0.3: `wasmtime run cli-run-trap.wasm` traps at `unreachable`.
(component
  (core module $m
    (func (export "run") (result i32) unreachable))
  (core instance $i (instantiate $m))
  (alias core export $i "run" (core func $runf))
  (type $rt (func (result (result))))
  (func $run (type $rt) (canon lift (core func $runf)))
  (instance $r (export "run" (func $run)))
  (export "wasi:cli/run@0.2.0" (instance $r))
)
