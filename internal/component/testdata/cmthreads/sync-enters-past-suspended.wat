;; sync-enters-past-suspended: an async task's implicit Thread SUSPENDS mid-callee, and a SYNC-typed call enters
;; past its exclusive_thread to resume it (#978). The parent's `run` starts a nested child's `g`, records its own
;; thread.index and suspends with nothing runnable; the child yields, then calls the parent's sync `f`, which
;; resume-laters `run` and returns 7; `run` resumes, waits for `g` and returns its result. wasmtime 49.0.2: 8.
;; Burroughs, when committed: hangs. The oracle is committed before any engine change, as ADR 0100 requires.
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core func $later (canon thread.resume-later))
  ;; P1: the parent's SYNC-typed `f`: it resumes the implicit Thread whose index `run` stored at mem[0],
  ;; then returns 7.
  (core module $p1
    (import "" "mem" (memory 1))
    (import "" "later" (func $later (param i32)))
    (func (export "f") (result i32)
      (call $later (i32.load (i32.const 0)))
      (i32.const 7)))
  (core instance $p1i (instantiate $p1 (with "" (instance (export "mem" (memory $cm)) (export "later" (func $later))))))
  (alias core export $p1i "f" (core func $ff))
  (type $ft (func (result u32)))
  (func $F (type $ft) (canon lift (core func $ff)))

  ;; The child: its async `g` calls the parent's `f` synchronously and returns f() + 1.
  (component $child
    (import "f" (func $f (result u32)))
    (core func $fl (canon lower (func $f)))
    (core func $ret (canon task.return (result u32)))
    (core module $cm
      (import "" "f" (func $f (result i32)))
      (import "" "ret" (func $ret (param i32)))
      ;; g YIELDs first, so the parent's `run` has suspended before f is called; the callback then calls f.
      (func (export "g") (result i32) (i32.const 1)) ;; YIELD
      (func (export "cb") (param i32 i32 i32) (result i32)
        (call $ret (i32.add (call $f) (i32.const 1)))
        (i32.const 0)))
    (core instance $ci (instantiate $cm (with "" (instance (export "f" (func $fl)) (export "ret" (func $ret))))))
    (alias core export $ci "g" (core func $gf))
    (alias core export $ci "cb" (core func $cbf))
    (type $gt (func async (result u32)))
    (func (export "g") (type $gt) (canon lift (core func $gf) async (callback $cbf))))
  (instance $c (instantiate $child (with "f" (func $F))))
  (alias export $c "g" (func $G))

  ;; P2: the parent's async `run`: store its own thread.index at mem[0], start the child's g into retptr 16,
  ;; then SUSPEND mid-callee with nothing runnable. Only the child's call into f can resume it.
  (core func $glow (canon lower (func $G) async (memory $cm)))
  (core func $idx (canon thread.index))
  (core func $susp (canon thread.suspend))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $cm)))
  (core func $taskret (canon task.return (result u32)))
  (core module $runmod
    (import "" "mem" (memory 1))
    (import "" "g" (func $g (param i32) (result i32)))
    (import "" "idx" (func $idx (result i32)))
    (import "" "susp" (func $susp (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "callee") (result i32)
      (local $packed i32) (local $si i32)
      (i32.store (i32.const 0) (call $idx))
      (local.set $packed (call $g (i32.const 16)))
      (drop (call $susp)) ;; parked mid-callee, nothing runnable: f's resume-later is the only way back
      (if (i32.ne (i32.and (local.get $packed) (i32.const 15)) (i32.const 2))
        (then
          (local.set $si (call $wsnew))
          (call $wsjoin (i32.shr_u (local.get $packed) (i32.const 4)) (local.get $si))
          (drop (call $wswait (local.get $si) (i32.const 32)))))
      (call $ret (i32.load (i32.const 16)))
      (i32.const 0))
    (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
  (core instance $runi (instantiate $runmod
    (with "" (instance (export "mem" (memory $cm)) (export "g" (func $glow)) (export "idx" (func $idx))
                       (export "susp" (func $susp)) (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
                       (export "wswait" (func $wswait)) (export "ret" (func $taskret))))))
  (alias core export $runi "callee" (core func $calleef))
  (alias core export $runi "cb" (core func $runcbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $calleef) async (callback $runcbf)))
)
