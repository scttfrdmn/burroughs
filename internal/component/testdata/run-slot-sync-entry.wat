;; run-slot-sync-entry: the run slot's own witness. An async task's callback-lifted entry executes a long loop
;; while a SYNC export of the same instance is entered; a sync entry takes no entrySem, so only the run slot
;; keeps the two apart. `spin` calls the host's `note` when its loop starts, then watches word 4 for a change
;; and returns `changed << 16` through task.return. `poke` writes word 4 and returns 7. No overlap reads 0.
;;
;; `(memory 1 1)`: heap-backed, so `-race` can see the loop's loads and poke's store (#979).
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (import "note" (func $note))
  (core func $notelow (canon lower (func $note)))
  (core func $ret (canon task.return (result u32)))
  (core module $m
    (import "" "mem" (memory 1 1))
    (import "" "note" (func $note))
    (import "" "ret" (func $ret (param i32)))
    (func (export "spin") (result i32) (local $seen i32) (local $changed i32) (local $i i32)
      (local.set $seen (i32.load (i32.const 4)))
      (call $note)
      (block $done
        (loop $again
          (if (i32.ne (i32.load (i32.const 4)) (local.get $seen))
            (then (local.set $changed (i32.const 1)) (br $done)))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br_if $again (i32.lt_u (local.get $i) (i32.const 3000000)))))
      (call $ret (i32.shl (local.get $changed) (i32.const 16)))
      (i32.const 0)) ;; EXIT
    (func (export "cb") (param i32 i32 i32) (result i32) unreachable)
    (func (export "poke") (result i32)
      (i32.store (i32.const 4) (i32.add (i32.load (i32.const 4)) (i32.const 1)))
      (i32.const 7)))
  (core instance $i (instantiate $m (with "" (instance
    (export "mem" (memory $cm)) (export "note" (func $notelow)) (export "ret" (func $ret))))))
  (alias core export $i "spin" (core func $spinf))
  (alias core export $i "cb" (core func $cbf))
  (alias core export $i "poke" (core func $pokef))
  (type $st (func async (result u32)))
  (type $pt (func (result u32)))
  (func (export "spin") (type $st) (canon lift (core func $spinf) async (callback $cbf)))
  (func (export "poke") (type $pt) (canon lift (core func $pokef))))
