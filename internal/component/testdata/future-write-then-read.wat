;; future-write-then-read: #1325 item 1, the rendezvous from the writer's side. One async callback task creates a
;; future<u8>, WRITES 7 first (async, so it parks BLOCKED with no reader), then READS it (async). The read is the
;; second arriver and drives the copy (def:End.copy), so it completes inline and the parked write's FUTURE_WRITE
;; event is pending. The task joins BOTH ends to a set and WAITs; the callback polls the set once more, so a
;; duplicate event on either end would show as a second event code.
;;
;; The result is the tuple (write status, read status, byte read, event code, event index, event payload, the
;; writable end's index, the second poll's code).
;;
;; If the read does NOT complete inline (the engine left both ends parked, #1325's defect), the task does not WAIT
;; on a set no event can wake: it polls once and returns the same tuple with event code 0xdead, so the defect reads
;; as a value rather than a hang.
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (type $fut (future u8))
  (core func $fnew (canon future.new $fut))
  (core func $fread (canon future.read $fut (memory $cm) async))
  (core func $fwrite (canon future.write $fut (memory $cm) async))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wspoll (canon waitable-set.poll (memory $cm)))
  (type $tup (tuple u32 u32 u32 u32 u32 u32 u32 u32))
  (core func $ret (canon task.return (result $tup)))
  (core module $m
    (import "" "mem" (memory 1 1))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wspoll" (func $wspoll (param i32 i32) (result i32)))
    (import "" "ret" (func $ret (param i32 i32 i32 i32 i32 i32 i32 i32)))
    ;; 16: ri  20: wi  24: write status  28: read status  32: the set  64: the read buffer  80: the write buffer
    ;; 96: the poll's (index, payload)
    (func $finish (param $ev i32) (param $p1 i32) (param $p2 i32)
      (call $ret (i32.load (i32.const 24)) (i32.load (i32.const 28)) (i32.load8_u (i32.const 64))
                 (local.get $ev) (local.get $p1) (local.get $p2) (i32.load (i32.const 20))
                 (call $wspoll (i32.load (i32.const 32)) (i32.const 96))))
    (func (export "run") (result i32) (local $f i64) (local $s i32)
      (local.set $f (call $fnew))
      (i32.store (i32.const 16) (i32.wrap_i64 (local.get $f)))
      (i32.store (i32.const 20) (i32.wrap_i64 (i64.shr_u (local.get $f) (i64.const 32))))
      (i32.store8 (i32.const 80) (i32.const 7))
      (i32.store (i32.const 24) (call $fwrite (i32.load (i32.const 20)) (i32.const 80)))
      (i32.store (i32.const 28) (call $fread (i32.load (i32.const 16)) (i32.const 64)))
      (local.set $s (call $wsnew))
      (i32.store (i32.const 32) (local.get $s))
      (call $wsjoin (i32.load (i32.const 20)) (local.get $s))
      (call $wsjoin (i32.load (i32.const 16)) (local.get $s))
      (if (i32.eq (i32.load (i32.const 28)) (i32.const -1))
        (then (call $finish (i32.const 0xdead) (i32.const 0) (i32.const 0)) (return (i32.const 0))))
      (i32.or (i32.shl (local.get $s) (i32.const 4)) (i32.const 2))) ;; WAIT
    (func (export "cb") (param $ev i32) (param $p1 i32) (param $p2 i32) (result i32)
      (call $finish (local.get $ev) (local.get $p1) (local.get $p2))
      (i32.const 0)))
  (core instance $mi (instantiate $m
    (with "" (instance (export "mem" (memory $cm)) (export "fnew" (func $fnew)) (export "fread" (func $fread))
                       (export "fwrite" (func $fwrite)) (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
                       (export "wspoll" (func $wspoll)) (export "ret" (func $ret))))))
  (alias core export $mi "run" (core func $runf))
  (alias core export $mi "cb" (core func $cbf))
  (type $rt (func async (result $tup)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf))))
