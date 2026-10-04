;; The POSITIVE socket: a parent whose single import is exactly what the committed `receipt/` guest
;; exports, so `wac plug` can satisfy it.
;;
;; # Why every part of this shape is forced
;;
;; Each of these was found by `wac` refusing, not chosen — recorded so the next author does not re-derive
;; them:
;;
;;   1. `import run` must be **async**. `receipt/`'s world says `export run: async func(id: u32) -> u32`,
;;      and a sync import of the same name and value types does not match:
;;      `error: the socket component had no matching imports for the plugs that were provided`.
;;   2. The `canon lower` must carry **`async`** to match, and an async lower requires a **`memory`**
;;      canonopt: `error: canonical option \`memory\` is required`. Hence the memory module.
;;   3. The core import's flat signature is **`(param i32 i32) (result i32)`** — the `id` argument plus a
;;      **return pointer**, returning the packed i32. With `(param i32) (result i32)` wasm-tools reports
;;      `expected: (func (param i32) (result i32)) / found: (func (param i32 i32) (result i32))`.
;;
;; The parent does not need to be *runnable* for this fixture's purpose: what is under test is whether a
;; composition satisfies the import, which is a property of the composed artifact's world. It does have to
;; validate, because an invalid socket is refused before composition is attempted.
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

  (core module $m
    (type (;0;) (func (param i32 i32) (result i32)))
    (type (;1;) (func (result i32)))
    (import "" "run" (func $r (;0;) (type 0)))
    (func (;1;) (type 1) (result i32)
      i32.const 7
      i32.const 0
      call $r
    )
    (export "go" (func 1))
  )
  (core instance $deps (;1;)
    (export "run" (func $lowered))
  )
  (core instance $i (;2;) (instantiate $m
      (with "" (instance $deps))
    )
  )
  (alias core export $i "go" (core func $g (;1;)))
  (type $gt (;1;) (func (result u32)))
  (func (;1;) (type $gt) (canon lift (core func $g)))
  (export (;2;) "go" (func 1))
)
