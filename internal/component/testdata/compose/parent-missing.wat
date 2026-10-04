;; The NEGATIVE socket: identical to `parent-supplied.wat` plus ONE import the plug cannot satisfy.
;;
;; `wac plug` composes this and **exits 0**, leaving `missing` in the composed world. That is the failure
;; mode the check exists for: a success status over an unsatisfied dependency. Without this arm, "the
;; plugged import is gone" is consistent with a check that cannot tell a satisfied composition from an
;; unsatisfied one, because the status is 0 either way.
;;
;; `missing` is deliberately a trivial sync func with a distinct name and a param type nothing else uses,
;; so a check that finds it cannot be matching something incidental.
(component
  (core module $memmod
    (memory (;0;) 1)
    (export "m" (memory 0))
  )
  (core instance $memi (;0;) (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm (;0;)))

  (type $t (;0;) (func async (param "id" u32) (result u32)))
  (import "run" (func $run (;0;) (type $t)))
  (core func $lowered (;0;) (canon lower (func $run) async (memory $cm)))

  (type $mt (;1;) (func (param "k" u32)))
  (import "missing" (func $miss (;1;) (type $mt)))
  (core func $misslowered (;1;) (canon lower (func $miss)))

  (core module $m
    (type (;0;) (func (param i32 i32) (result i32)))
    (type (;1;) (func (param i32)))
    (type (;2;) (func (result i32)))
    (import "" "run" (func $r (;0;) (type 0)))
    (import "" "missing" (func $mi (;1;) (type 1)))
    (func (;2;) (type 2) (result i32)
      i32.const 1
      call $mi
      i32.const 7
      i32.const 0
      call $r
    )
    (export "go" (func 2))
  )
  (core instance $deps (;1;)
    (export "run" (func $lowered))
    (export "missing" (func $misslowered))
  )
  (core instance $i (;2;) (instantiate $m
      (with "" (instance $deps))
    )
  )
  (alias core export $i "go" (core func $g (;2;)))
  (type $gt (;2;) (func (result u32)))
  (func (;2;) (type $gt) (canon lift (core func $g)))
  (export (;3;) "go" (func 2))
)
