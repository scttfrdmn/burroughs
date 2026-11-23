;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; spawn-door-unshared (#1337, ADR 0131): spawn-door-no-mstart with an `mstart`, and its canonical memory UNSHARED,
;; so its instance is not free (ADR 0130's §1). Contract §2 T-1 says the new agent shares the component's shared
;; canonical memory, so the door is refused by name, with the gate on or off. Valid at the pin but for the door,
;; which no other engine supplies. Assembled with `wasm-tools parse spawn-door-unshared.wat -o
;; spawn-door-unshared.wasm` (wasm-tools 1.258.0).
(component
  (import "burroughs:threads/spawn@0.1.0" (instance $th
    (export "spawn" (func (param "arg" u32)))))
  (core module $mm (memory (export "m") 1 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (alias export $th "spawn" (func $spawn))
  (core func $spawnl (canon lower (func $spawn)))
  (core func $wswait (canon waitable-set.wait (memory $m)))
  (core module $gm
    (import "" "mem" (memory 1 1))
    (import "" "spawn" (func $spawn (param i32)))
    (import "" "wswait" (func (param i32 i32) (result i32)))
    (func (export "mstart") (param i32))
    (func (export "run") (result i32) (call $spawn (i32.const 1)) (i32.const 0)))
  (core instance $gi (instantiate $gm (with "" (instance
    (export "mem" (memory $m)) (export "spawn" (func $spawnl)) (export "wswait" (func $wswait))))))
  (type $rt (result))
  (func $run async (result $rt) (canon lift (core func $gi "run")))
  (instance $ri (export "run" (func $run)))
  (export "wasi:cli/run@0.3.0" (instance $ri)))
