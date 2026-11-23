;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; spawn-door-wrong-type (#1337, ADR 0131): spawn-door-no-mstart with an `mstart`, importing the door at
;; `func(arg: u64)` where contract §2 T-1 names `func(arg: u32)`. Refused at the lower, by name. Invalid at the pin
;; (a shared canonical memory, ADR 0129). Assembled with `wasm-tools parse spawn-door-wrong-type.wat -o
;; spawn-door-wrong-type.wasm` (wasm-tools 1.258.0).
(component
  (import "burroughs:threads/spawn@0.1.0" (instance $th
    (export "spawn" (func (param "arg" u64)))))
  (core module $mm (memory (export "m") 1 1 shared))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (alias export $th "spawn" (func $spawn))
  (core func $spawnl (canon lower (func $spawn)))
  (core func $wswait (canon waitable-set.wait (memory $m)))
  (core module $gm
    (import "" "mem" (memory 1 1 shared))
    (import "" "spawn" (func (param i64)))
    (import "" "wswait" (func (param i32 i32) (result i32)))
    (func (export "mstart") (param i32))
    (func (export "run") (result i32) (i32.const 0)))
  (core instance $gi (instantiate $gm (with "" (instance
    (export "mem" (memory $m)) (export "spawn" (func $spawnl)) (export "wswait" (func $wswait))))))
  (type $rt (result))
  (func $run async (result $rt) (canon lift (core func $gi "run")))
  (instance $ri (export "run" (func $run)))
  (export "wasi:cli/run@0.3.0" (instance $ri)))
