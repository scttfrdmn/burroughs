;; poll-aligned: a callback task polls an empty waitable set with its event pointer at 4. The pin's unpack_event
;; (2e7895c, upstream #741) traps unless the pointer is 4-aligned. This shape the control: 100 on wasmtime 49.0.2 (10 of 10), the pin, and Burroughs.
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core func $wsnew (canon waitable-set.new))
  (core func $wspoll (canon waitable-set.poll (memory $cm)))
  (core func $ret (canon task.return (result u32)))
  (core module $m
    (import "" "mem" (memory 1 1))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wspoll" (func $wspoll (param i32 i32) (result i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "run") (result i32)
      (call $ret (i32.add (i32.const 100) (call $wspoll (call $wsnew) (i32.const 4))))
      (i32.const 0))
    (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
  (core instance $mi (instantiate $m (with "" (instance (export "mem" (memory $cm)) (export "wsnew" (func $wsnew))
    (export "wspoll" (func $wspoll)) (export "ret" (func $ret))))))
  (alias core export $mi "run" (core func $runf))
  (alias core export $mi "cb" (core func $cbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf))))
