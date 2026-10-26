;; parked-runner (#771's sp5 and h4 shape; the plan on #771, accepted by the chair on 2026-10-26).
;;
;; ONE async-callback task, `run(n, k)`, whose n created Threads each park in `waitable-set.wait` on their own
;; future, while a further created Thread, the runner, executes. Not n tasks: `def:WaitableSet.wait` keeps
;; `exclusive_thread`, so a second callback task could not enter while the first is parked in a sync wait.
;;
;;   - each parked Thread i reads future i (BLOCKED), joins it to a set, counts itself parked (mem[0]), and waits;
;;   - `run` creates the runner but does not ready it. The n-th parked Thread readies it (`thread.resume-later`)
;;     just before its own wait. Only one Thread runs at a time (`def:Store.tick`), so the runner cannot start
;;     until that Thread has parked, and every earlier Thread counted itself and parked in one uninterrupted run.
;;     No `thread.yield` (0x0c) is needed; Burroughs refuses it by name, as one of the unlowered async built-ins (#952);
;;   - the runner counts mem[8] up to k, then writes the n futures;
;;   - each parked Thread wakes and counts itself woken (mem[4]);
;;   - `run`'s callback returns mem[8] once all n have woken.
;;
;; So `run` returning k means the runner ran to completion while n Threads were parked in a guest-called
;; built-in. sp5's harness Stops the instance while they are parked and the runner is counting; h4's (n = 1) reads
;; the runner's progress across A's park. The harness reads mem[0], mem[4] and mem[8] directly.
;;
;; Oracle: the REFERENCE READING, `value 1000` for n = 4 and n = 1, and HANG for the never-released control
;; (reference/refread.reading, `parked_threads_and_a_runner`). The wasmtime reading is taken by build.sh. Stock
;; wasmtime 49.0.3 may run into #1045 (a created thread's wait taking the task-wide YIELD event). If it does, the
;; pin decides (ADR 0102), and the patched build's reading is corroboration only.
;;
;; Memory: 0 parked, 4 woke, 8 counter, 12 n, 16 k, 20 the runner's Thread index; futures at 64+8i (readable)
;; and 68+8i (writable);
;; read buffers at 1024+i; wait event records at 2048+8i; the write buffer at 900.
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (type $fut (future u8))
  (core type $bodyt (func (param i32)))
  (core func $fnew (canon future.new $fut))
  (core func $fread (canon future.read $fut (memory $cm) async))
  (core func $fwrite (canon future.write $fut (memory $cm) async))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $cm)))
  (core func $later (canon thread.resume-later))
  (core func $ret (canon task.return (result u32)))
  (core module $tmod
    (import "" "mem" (memory 1 1))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (import "" "later" (func $later (param i32)))
    (table (export "t") 2 2 funcref)
    ;; A parked Thread: read future i; if BLOCKED, join it to a fresh set, count itself parked, and, if it is the
    ;; n-th, ready the runner. Then wait.
    (func $parked (param $i i32) (local $r i32) (local $s i32)
      (local.set $r (i32.load (i32.add (i32.const 64) (i32.mul (local.get $i) (i32.const 8)))))
      (if (i32.eq (call $fread (local.get $r) (i32.add (i32.const 1024) (local.get $i))) (i32.const -1))
        (then
          (local.set $s (call $wsnew))
          (call $wsjoin (local.get $r) (local.get $s))
          (i32.store (i32.const 0) (i32.add (i32.load (i32.const 0)) (i32.const 1)))
          (if (i32.eq (i32.load (i32.const 0)) (i32.load (i32.const 12)))
            (then (call $later (i32.load (i32.const 20)))))
          (drop (call $wswait (local.get $s) (i32.add (i32.const 2048) (i32.mul (local.get $i) (i32.const 8)))))))
      (i32.store (i32.const 4) (i32.add (i32.load (i32.const 4)) (i32.const 1))))
    ;; The runner: readied by the last parked Thread, so all n are parked when it starts. It counts mem[8] up to
    ;; k, then releases the n futures.
    (func $runner (param i32) (local $i i32)
      (block $counted
        (loop $count
          (br_if $counted (i32.ge_u (i32.load (i32.const 8)) (i32.load (i32.const 16))))
          (i32.store (i32.const 8) (i32.add (i32.load (i32.const 8)) (i32.const 1)))
          (br $count)))
      (i32.store8 (i32.const 900) (i32.const 1))
      (local.set $i (i32.const 0))
      (block $released
        (loop $release
          (br_if $released (i32.ge_u (local.get $i) (i32.load (i32.const 12))))
          (drop (call $fwrite (i32.load (i32.add (i32.const 68) (i32.mul (local.get $i) (i32.const 8))))
                              (i32.const 900)))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $release))))
    (elem (i32.const 0) $parked $runner))
  (core instance $ti (instantiate $tmod
    (with "" (instance (export "mem" (memory $cm)) (export "fread" (func $fread)) (export "fwrite" (func $fwrite))
                       (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "wswait" (func $wswait))
                       (export "later" (func $later))))))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $ti "t")))
  (core module $m
    (import "" "mem" (memory 1 1))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "new" (func $new (param i32 i32) (result i32)))
    (import "" "later" (func $later (param i32)))
    (import "" "ret" (func $ret (param i32)))
    ;; run(n, k): create the runner WITHOUT readying it, make n futures, ready n parked Threads, then YIELD to the
    ;; callback loop. The last parked Thread readies the runner.
    (func (export "run") (param $n i32) (param $k i32) (result i32) (local $i i32) (local $f i64)
      (i32.store (i32.const 12) (local.get $n))
      (i32.store (i32.const 16) (local.get $k))
      (i32.store (i32.const 20) (call $new (i32.const 1) (i32.const 0)))
      (local.set $i (i32.const 0))
      (block $made
        (loop $make
          (br_if $made (i32.ge_u (local.get $i) (local.get $n)))
          (local.set $f (call $fnew))
          (i32.store (i32.add (i32.const 64) (i32.mul (local.get $i) (i32.const 8))) (i32.wrap_i64 (local.get $f)))
          (i32.store (i32.add (i32.const 68) (i32.mul (local.get $i) (i32.const 8)))
                     (i32.wrap_i64 (i64.shr_u (local.get $f) (i64.const 32))))
          (call $later (call $new (i32.const 0) (local.get $i)))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $make)))
      (i32.const 1)) ;; YIELD
    ;; The callback: return the count once all n parked Threads have woken; otherwise YIELD again.
    (func (export "cb") (param i32 i32 i32) (result i32)
      (if (i32.ge_u (i32.load (i32.const 4)) (i32.load (i32.const 12)))
        (then (call $ret (i32.load (i32.const 8))) (return (i32.const 0))))
      (i32.const 1)))
  (core instance $mi (instantiate $m
    (with "" (instance (export "mem" (memory $cm)) (export "fnew" (func $fnew)) (export "new" (func $new))
                       (export "later" (func $later)) (export "ret" (func $ret))))))
  (alias core export $mi "run" (core func $runf))
  (alias core export $mi "cb" (core func $cbf))
  (type $rt (func async (param "n" u32) (param "k" u32) (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf)))
)
