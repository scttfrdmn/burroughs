;; string-arg-realloc-synth: the witness for lowering a `string` ARGUMENT through the guest's own
;; `cabi_realloc` (#902, ADR 0098).
;;
;; # Why no real toolchain produces what this needs
;;
;; The fixture `wit-bindgen` would emit allocates into a pre-grown heap, so a write that used a stale
;; memory base would land correctly anyway and the hazard would be invisible. This guest's realloc
;; **grows the memory and returns a pointer into the page it just added**, which is the shape that
;; distinguishes a write resolving the image *inside* the growth lock from one that captured a base
;; before calling realloc. ADR 0073 is why that distinction exists at all.
;;
;; # Three lifts, because the three failure modes are unrelated
;;
;;   * `echo` — the working path: a `string` parameter, a realloc that grows, and a `u32` result that is
;;     the **sum of the bytes the guest read**. A checksum rather than a length, so the test learns the
;;     bytes arrived intact and at the right address, not merely that something of the right size did.
;;   * `echo-trap` — the same signature against a realloc whose body is `unreachable`. A guest is entitled
;;     to refuse an allocation, and the host must report that refusal rather than write at whatever the
;;     trap left behind.
;;   * `peek-arg` — returns the byte at the allocation's address, so a test can check that the host
;;     **left the allocation alone** after the call. The model's rule is that a lowered argument belongs to
;;     the callee; a host that freed or zeroed it would show up here and nowhere else.
;;
;; # The allocation address is predictable on purpose
;;
;; `realloc` grows by exactly one page per call and returns the old page count × 65536, so the first
;; allocation is at 65536 — the first byte of the page that did not exist when the call began. `peek-arg`
;; reads that address. A bump allocator with a hidden watermark would make the ownership witness
;; unwritable from outside.
;;
;; # Two core modules, for the reason task-return-string-clobber-synth gives
;;
;; The lift's canonopts name the memory and the realloc; the code module imports `task.return`. One module
;; cannot both supply an import's dependency and receive the import, so the memory lives in its own
;; module, instantiated first, and the code module imports it.
;;
;; Authored with `wasm-tools parse` 1.258.0. The .wat is committed alongside the .wasm.
(component
  (type $echot (;0;) (func async (param "s" string) (result u32)))
  (type $peekt (;1;) (func async (result u32)))
  (type $sumt (;2;) (func async (param "xs" (list u32)) (result u32)))

  (core module $memmod (;0;)
    ;; One page to start, growable to four. The limit is deliberate: an unbounded maximum would let a
    ;; runaway realloc allocate until the host OOMs, and a fixture should fail rather than swap.
    ;; One page to start, growable to 16. The ceiling is deliberate — an unbounded maximum would let a
    ;; runaway realloc allocate until the host swaps, and a fixture should fail rather than that. 16
    ;; rather than 4 because each lowering grows by a page and one test makes seven of them; at 4 the
    ;; ceiling was reached mid-test and `memory.grow` returned -1, which this module turns into a trap.
    ;; That surfaced as an alignment test failing on a REALLOC error, which is the right failure for the
    ;; wrong reason and is why the number is now justified rather than picked.
    (memory (;0;) (export "mem") 1 16)
  )
  (core instance $memi (;0;) (instantiate $memmod))
  (alias core export $memi "mem" (core memory $mem (;0;)))

  (core func $taskret (;0;) (canon task.return (result u32)))
  (core instance $ci (;1;)
    (export "taskret" (func $taskret))
  )

  (core module $m (;1;)
    (type $ret1 (;0;) (func (param i32)))
    (type $reallocT (;1;) (func (param i32 i32 i32 i32) (result i32)))
    (type $echoT (;2;) (func (param i32 i32) (result i32)))
    (type $peekT (;3;) (func (result i32)))
    (type $cbT (;4;) (func (param i32 i32 i32) (result i32)))

    (import "" "taskret" (func $taskret (;0;) (type $ret1)))
    (import "mem" "mem" (memory (;0;) 1 16))

    ;; cabi_realloc(orig_ptr, orig_size, align, new_size) -> ptr
    ;;
    ;; Grows by one page and hands back the start of the page it just added. It ignores `orig_*` because
    ;; the host only ever makes fresh allocations here, and it ignores `align` because 65536 is aligned
    ;; to anything a component value needs — both stated so a reader does not take the omission for an
    ;; oversight.
    (func $realloc (;1;) (type $reallocT) (param i32 i32 i32 i32) (result i32)
      (local $old i32)
      i32.const 1
      memory.grow          ;; returns the PREVIOUS page count, or -1 on failure
      local.set $old
      ;; A refused growth must not be reported as the address 0xFFFF0000.
      local.get $old
      i32.const 0
      i32.lt_s
      if
        unreachable
      end
      local.get $old
      i32.const 16
      i32.shl              ;; old_pages * 65536
    )

    ;; A realloc that refuses. `echo-trap`'s lift names this one.
    (func $reallocTrap (;2;) (type $reallocT) (param i32 i32 i32 i32) (result i32)
      unreachable
    )

    ;; A realloc that returns a MISALIGNED pointer: the page start plus 2.
    ;;
    ;; Two is the realistic offence and the reason this is not just "an odd address". It is a multiple of
    ;; 2 and not of 4, so it satisfies a `string`'s alignment of 1 and a `u16`'s of 2 while violating a
    ;; `u32`'s of 4 — which is exactly the case definitions.py:1599 and :1715 trap on, and exactly the
    ;; case a guest allocator with a 2-byte bump could produce by accident. A non-power-of-two demand
    ;; shows the engine's check runs; this shows it catches something the ABI can actually hand it.
    (func $reallocMisaligned (;3;) (type $reallocT) (param i32 i32 i32 i32) (result i32)
      (local $old i32)
      i32.const 1
      memory.grow
      local.set $old
      local.get $old
      i32.const 0
      i32.lt_s
      if
        unreachable
      end
      local.get $old
      i32.const 16
      i32.shl
      i32.const 2
      i32.add            ;; page start + 2: aligned to 2, never to 4
    )

    ;; echo(ptr, len) -> packed. Sums the bytes it was handed and resolves with the sum.
    (func $echo (;3;) (type $echoT) (param $ptr i32) (param $len i32) (result i32)
      (local $i i32)
      (local $sum i32)
      i32.const 0
      local.set $i
      block $done
        loop $l
          local.get $i
          local.get $len
          i32.ge_u
          br_if $done
          local.get $sum
          local.get $ptr
          local.get $i
          i32.add
          i32.load8_u
          i32.add
          local.set $sum
          local.get $i
          i32.const 1
          i32.add
          local.set $i
          br $l
        end
      end
      local.get $sum
      call $taskret
      i32.const 0          ;; EXIT
    )

    ;; sum-list(ptr, count) -> packed. Sums `count` u32s at `ptr` and resolves with the total.
    ;;
    ;; A `list<u32>` is the first argument whose **alignment is not 1**: elements are 4 bytes at a 4-byte
    ;; stride, so the realloc's pointer must be a multiple of 4 and the engine's misalignment trap stops
    ;; being vacuous. It also reads through the stride, so a lowering that packed the elements at the
    ;; wrong pitch produces a wrong sum rather than merely a wrong length.
    (func $sumList (;5;) (type $echoT) (param $ptr i32) (param $count i32) (result i32)
      (local $i i32)
      (local $sum i32)
      i32.const 0
      local.set $i
      block $done
        loop $l
          local.get $i
          local.get $count
          i32.ge_u
          br_if $done
          local.get $sum
          local.get $ptr
          local.get $i
          i32.const 2
          i32.shl            ;; i * 4 — the u32 stride
          i32.add
          i32.load
          i32.add
          local.set $sum
          local.get $i
          i32.const 1
          i32.add
          local.set $i
          br $l
        end
      end
      local.get $sum
      call $taskret
      i32.const 0          ;; EXIT
    )

    ;; peek-arg() -> packed. Resolves with the byte at 65536 — the first allocation's address.
    (func $peek (;4;) (type $peekT) (result i32)
      i32.const 65536
      i32.load8_u
      call $taskret
      i32.const 0          ;; EXIT
    )

    ;; Never entered: every callee above returns EXIT directly.
    (func $cb (;5;) (type $cbT) (param i32 i32 i32) (result i32)
      unreachable
    )

    (export "realloc" (func $realloc))
    (export "realloc-trap" (func $reallocTrap))
    (export "realloc-misaligned" (func $reallocMisaligned))
    (export "echo" (func $echo))
    (export "sum-list" (func $sumList))
    (export "peek" (func $peek))
    (export "cb" (func $cb))
  )
  (core instance $mi (;2;) (instantiate $m
      (with "" (instance $ci))
      (with "mem" (instance $memi))
    )
  )
  (alias core export $mi "realloc" (core func $reallocf (;1;)))
  (alias core export $mi "realloc-trap" (core func $realloctrapf (;2;)))
  (alias core export $mi "realloc-misaligned" (core func $reallocmisf (;3;)))
  (alias core export $mi "sum-list" (core func $sumlistf (;7;)))
  (alias core export $mi "echo" (core func $echof (;3;)))
  (alias core export $mi "peek" (core func $peekf (;4;)))
  (alias core export $mi "cb" (core func $cbf (;5;)))

  (func (;0;) (type $echot) (canon lift (core func $echof) async (callback $cbf)
      (memory $mem) (realloc $reallocf)))
  (func (;1;) (type $echot) (canon lift (core func $echof) async (callback $cbf)
      (memory $mem) (realloc $realloctrapf)))
  (func (;2;) (type $peekt) (canon lift (core func $peekf) async (callback $cbf) (memory $mem)))
  ;; `echo-misaligned` exists to make the misaligned realloc **reachable**, not because a `string`
  ;; argument exercises its offence: a string's alignment is 1, so page+2 satisfies it and this export
  ;; succeeds. The trap it witnesses is the alignment-4 demand a `list<u32>` makes, asked of this lift's
  ;; realloc directly until the list argument path lands. That a string still crosses through it is
  ;; asserted too — it shows the check is alignment-sensitive rather than address-sensitive.
  (func (;3;) (type $echot) (canon lift (core func $echof) async (callback $cbf)
      (memory $mem) (realloc $reallocmisf)))
  ;; `list<u32>` through the page-aligned realloc: the working path for an argument whose alignment is 4.
  (func (;4;) (type $sumt) (canon lift (core func $sumlistf) async (callback $cbf)
      (memory $mem) (realloc $reallocf)))
  ;; The same signature through the realloc that returns page+2. **This is the pair's live half**: a
  ;; `list<u32>` demands alignment 4, page+2 is a multiple of 2 and not of 4, so the misalignment trap
  ;; fires through a real parameter rather than a hand-built request.
  (func (;5;) (type $sumt) (canon lift (core func $sumlistf) async (callback $cbf)
      (memory $mem) (realloc $reallocmisf)))

  (export (;3;) "echo" (func 0))
  (export (;4;) "echo-trap" (func 1))
  (export (;5;) "peek" (func 2))
  (export (;6;) "echo-misaligned" (func 3))
  (export (;7;) "sum-list" (func 4))
  (export (;8;) "sum-list-misaligned" (func 5))
)
