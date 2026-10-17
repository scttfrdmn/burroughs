;; poll-before-suspend (#1020's class, default path: gate:cm-threads off, no Threads, no host). A parent's async
;; callback `run` async-lowers a provider's `op` (which YIELDs once, then returns x), joins the subtask to a
;; set, and then POLLS that set in a long loop without suspending. In the pin and in wasmtime the provider
;; cannot progress until the parent suspends, so every poll reads NONE. `run` returns seen * 100 + lower-state.
(component
  (component $prov
    (core module $pmem (memory (export "m") 1 1))
    (core instance $pmemi (instantiate $pmem))
    (alias core export $pmemi "m" (core memory $pm))
    (core func $ret (canon task.return (result u32)))
    (core module $pm2
      (import "" "ret" (func $ret (param i32)))
      (import "" "mem" (memory 1 1))
      (func (export "op") (param i32) (result i32) (i32.store (i32.const 0) (local.get 0)) (i32.const 1))
      (func (export "cb") (param i32 i32 i32) (result i32) (call $ret (i32.load (i32.const 0))) (i32.const 0)))
    (core instance $pi (instantiate $pm2 (with "" (instance (export "ret" (func $ret)) (export "mem" (memory $pm))))))
    (alias core export $pi "op" (core func $opf))
    (alias core export $pi "cb" (core func $cbf))
    (type $opt (func async (param "x" u32) (result u32)))
    (func (export "op") (type $opt) (canon lift (core func $opf) async (callback $cbf))))
  (instance $p (instantiate $prov))
  (alias export $p "op" (func $OP))
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core func $oplow (canon lower (func $OP) async (memory $cm)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wspoll (canon waitable-set.poll (memory $cm)))
  (core func $ret (canon task.return (result u32)))
  (core module $m
    (import "" "mem" (memory 1 1))
    (import "" "op" (func $op (param i32 i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wspoll" (func $wspoll (param i32 i32) (result i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "run") (result i32) (local $p i32) (local $s i32) (local $i i32) (local $seen i32)
      (local.set $p (call $op (i32.const 7) (i32.const 0)))
      (if (i32.ne (i32.and (local.get $p) (i32.const 15)) (i32.const 1)) ;; not STARTED: not this shape
        (then (call $ret (i32.add (i32.const 9000) (i32.and (local.get $p) (i32.const 15)))) (return (i32.const 0))))
      (local.set $s (call $wsnew))
      (call $wsjoin (i32.shr_u (local.get $p) (i32.const 4)) (local.get $s))
      (block $done
        (loop $again
          (if (i32.ne (call $wspoll (local.get $s) (i32.const 8)) (i32.const 0))
            (then (local.set $seen (i32.const 1)) (br $done)))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br_if $again (i32.lt_u (local.get $i) (i32.const 200000)))))
      (call $ret (i32.add (i32.mul (local.get $seen) (i32.const 100)) (i32.and (local.get $p) (i32.const 15))))
      (i32.const 0))
    (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
  (core instance $mi (instantiate $m (with "" (instance (export "mem" (memory $cm)) (export "op" (func $oplow))
    (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "wspoll" (func $wspoll)) (export "ret" (func $ret))))))
  (alias core export $mi "run" (core func $runf))
  (alias core export $mi "cb" (core func $cbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf))))
