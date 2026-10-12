;; reenter: parent -> child -> parent, all synchronous, through a lift the parent passes its child
;; (result-store slice, ADR 0101 amendment). wasmtime 49.0.2: 8 (3 of 3). See
;; TestAChainReentersAnInstanceItAlreadyHolds.
(component
  (core module $p1 (func (export "f") (result i32) i32.const 7))
  (core instance $p1i (instantiate $p1))
  (alias core export $p1i "f" (core func $ff))
  (type $ft (func (result u32)))
  (func $F (type $ft) (canon lift (core func $ff)))
  (component $child
    (import "f" (func $f (result u32)))
    (core func $fl (canon lower (func $f)))
    (core module $cm
      (import "" "f" (func $f (result i32)))
      (func (export "g") (result i32) (i32.add (call $f) (i32.const 1))))
    (core instance $ci (instantiate $cm (with "" (instance (export "f" (func $fl))))))
    (alias core export $ci "g" (core func $gf))
    (type $gt (func (result u32)))
    (func (export "g") (type $gt) (canon lift (core func $gf)))
  )
  (instance $c (instantiate $child (with "f" (func $F))))
  (alias export $c "g" (func $G))
  (core func $gl (canon lower (func $G)))
  (core module $p2
    (import "" "g" (func $g (result i32)))
    (func (export "run") (result i32) (call $g)))
  (core instance $p2i (instantiate $p2 (with "" (instance (export "g" (func $gl))))))
  (alias core export $p2i "run" (core func $runf))
  (func (export "run") (type $ft) (canon lift (core func $runf)))
)
