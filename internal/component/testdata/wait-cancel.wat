;; wait-cancel (#1233, ADR 0121): WASI 0.3's wait-for and wait-until, async-lowered, against the oracle. Readings:
;; wait-cancel.reading. Each export returns its observations packed into one u32:
;;   zero                     wait-for(0), wait-until(0), wait-until(now()): each lower's packed return, a byte each
;;   order                    wait-for(10s) then wait-for(10ms) join one set; the first event's code<<24, whether
;;                            it names the short one<<16, its state<<8, then the sync cancel of the long one
;;   cancel-sync              wait-for(1h)'s packed state * 16 + a sync subtask.cancel's status, sync-typed
;;   cancel-sync-async-typed  the same, from an async-typed function
;;   cancel-async             wait-for(1h)'s state<<24, an async subtask.cancel's status<<16, and if it was
;;                            BLOCKED the next event's code<<8 and state
(component
  (import "wasi:clocks/monotonic-clock@0.3.0" (instance $mc
    (export "now" (func (result u64)))
    (export "wait-until" (func async (param "when" u64)))
    (export "wait-for" (func async (param "how-long" u64)))))
  (alias export $mc "now" (func $now))
  (alias export $mc "wait-until" (func $wu))
  (alias export $mc "wait-for" (func $wf))

  (core module $memmod (memory (export "m") 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $m))

  (core func $nowl (canon lower (func $now)))
  (core func $wul (canon lower (func $wu) async))
  (core func $wfl (canon lower (func $wf) async))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $m)))
  (core func $scancel (canon subtask.cancel))
  (core func $acancel (canon subtask.cancel async))

  (core module $gm
    (import "" "m" (memory 1))
    (import "" "now" (func $now (result i64)))
    (import "" "wu" (func $wu (param i64) (result i32)))
    (import "" "wf" (func $wf (param i64) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (import "" "scancel" (func $scancel (param i32) (result i32)))
    (import "" "acancel" (func $acancel (param i32) (result i32)))
    ;; zero: the packed returns of wait-for(0), wait-until(0) and wait-until(now()), one byte each.
    (func (export "zero") (result i32)
      (i32.or (i32.or
        (call $wf (i64.const 0))
        (i32.shl (call $wu (i64.const 0)) (i32.const 8)))
        (i32.shl (call $wu (call $now)) (i32.const 16))))
    ;; order: wait-for(10s) and wait-for(10ms) join one set; the first event's code, whether it names the
    ;; short one's index, its state, then the sync cancel of the long one (unjoined first).
    (func (export "order") (result i32) (local $l i32) (local $s i32) (local $set i32) (local $code i32) (local $c i32)
      (local.set $l (i32.shr_u (call $wf (i64.const 10000000000)) (i32.const 4)))
      (local.set $s (i32.shr_u (call $wf (i64.const 10000000)) (i32.const 4)))
      (local.set $set (call $wsnew))
      (call $wsjoin (local.get $l) (local.get $set))
      (call $wsjoin (local.get $s) (local.get $set))
      (local.set $code (call $wswait (local.get $set) (i32.const 64)))
      (call $wsjoin (local.get $l) (i32.const 0))
      (local.set $c (call $scancel (local.get $l)))
      (i32.or (i32.or (i32.or
        (i32.shl (local.get $code) (i32.const 24))
        (i32.shl (i32.eq (i32.load (i32.const 64)) (local.get $s)) (i32.const 16)))
        (i32.shl (i32.load (i32.const 68)) (i32.const 8)))
        (local.get $c)))
    ;; cancel-sync: wait-for(1h)'s packed state * 16 + the sync cancel's status.
    (func (export "cancel-sync") (result i32) (local $p i32)
      (local.set $p (call $wf (i64.const 3600000000000)))
      (i32.add (i32.mul (i32.and (local.get $p) (i32.const 15)) (i32.const 16))
               (call $scancel (i32.shr_u (local.get $p) (i32.const 4)))))
    ;; cancel-async: wait-for(1h)'s packed state, the async cancel's status, and, if BLOCKED, the event
    ;; that follows: state<<24 | (status & 0xff)<<16 | code<<8 | event state.
    (func (export "cancel-async") (result i32) (local $p i32) (local $i i32) (local $c i32) (local $set i32) (local $code i32)
      (local.set $p (call $wf (i64.const 3600000000000)))
      (local.set $i (i32.shr_u (local.get $p) (i32.const 4)))
      (local.set $c (call $acancel (local.get $i)))
      (if (i32.eq (local.get $c) (i32.const -1))
        (then
          (local.set $set (call $wsnew))
          (call $wsjoin (local.get $i) (local.get $set))
          (local.set $code (call $wswait (local.get $set) (i32.const 64))))
        (else (i32.store (i32.const 68) (i32.const 0))))
      (i32.or (i32.or (i32.or
        (i32.shl (i32.and (local.get $p) (i32.const 15)) (i32.const 24))
        (i32.shl (i32.and (local.get $c) (i32.const 255)) (i32.const 16)))
        (i32.shl (local.get $code) (i32.const 8)))
        (i32.load (i32.const 68)))))
  (core instance $gi (instantiate $gm (with "" (instance
    (export "m" (memory $m)) (export "now" (func $nowl)) (export "wu" (func $wul)) (export "wf" (func $wfl))
    (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "wswait" (func $wswait))
    (export "scancel" (func $scancel)) (export "acancel" (func $acancel))))))

  (type $st (func (result u32)))
  (type $at (func async (result u32)))
  (func (export "zero") (type $st) (canon lift (core func $gi "zero")))
  (func (export "order") (type $at) (canon lift (core func $gi "order")))
  (func (export "cancel-sync") (type $st) (canon lift (core func $gi "cancel-sync")))
  (func (export "cancel-sync-async-typed") (type $at) (canon lift (core func $gi "cancel-sync")))
  (func (export "cancel-async") (type $at) (canon lift (core func $gi "cancel-async"))))
