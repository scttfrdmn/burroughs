;; future-write-then-read-bool: #1325 item 1's element row. future-write-then-read's order over a future<bool>: the
;; write parks (a parked write does not copy, so it does not trap), and the read, arriving second, is where the
;; copy is, so the read traps: a non-numeric element cannot be copied between two ends of one instance
;; (def:End.copy:"trap_if(self.owner and self.owner is self.other.owner and not none_or_number_type(self.t))").
;;
;; If the read does not trap and does not complete either, the task returns the write status and 0xdead.
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (type $fut (future bool))
  (core func $fnew (canon future.new $fut))
  (core func $fread (canon future.read $fut (memory $cm) async))
  (core func $fwrite (canon future.write $fut (memory $cm) async))
  (core func $ret (canon task.return (result (tuple u32 u32))))
  (core module $m
    (import "" "mem" (memory 1 1))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))
    (import "" "ret" (func $ret (param i32 i32)))
    (func (export "run") (result i32) (local $f i64) (local $wr i32) (local $rd i32)
      (local.set $f (call $fnew))
      (i32.store8 (i32.const 80) (i32.const 1))
      (local.set $wr (call $fwrite (i32.wrap_i64 (i64.shr_u (local.get $f) (i64.const 32))) (i32.const 80)))
      (local.set $rd (call $fread (i32.wrap_i64 (local.get $f)) (i32.const 64)))
      (if (i32.eq (local.get $rd) (i32.const -1))
        (then (call $ret (local.get $wr) (i32.const 0xdead)) (return (i32.const 0))))
      (call $ret (local.get $wr) (local.get $rd))
      (i32.const 0)) ;; EXIT
    (func (export "cb") (param $ev i32) (param $p1 i32) (param $p2 i32) (result i32)
      (unreachable)))
  (core instance $mi (instantiate $m
    (with "" (instance (export "mem" (memory $cm)) (export "fnew" (func $fnew)) (export "fread" (func $fread))
                       (export "fwrite" (func $fwrite)) (export "ret" (func $ret))))))
  (alias core export $mi "run" (core func $runf))
  (alias core export $mi "cb" (core func $cbf))
  (type $rt (func async (result (tuple u32 u32))))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf))))
