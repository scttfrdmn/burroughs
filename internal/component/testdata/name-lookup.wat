;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; name-lookup (#1278 E7c, #1268, ADR 0126): wasi:sockets/ip-name-lookup's resolve-addresses at the ABI, below std,
;; lowered WITHOUT the async option, as wasi-libc's getaddrinfo lowers it: core type (i32 i32 i32) -> (), the name's
;; (ptr, len) and a return pointer. The import is an `async func` in its WIT, so the lower is the pin's blocking sync
;; lower (def:canon_lower:"thread.wait_until(subtask.resolved)"): the caller waits until the lookup resolves, and
;; that wait meets the sync tail only when the lifted function is sync-typed.
;;
;; `run(row)` is async-typed and lifted with no async option, as Rust std's `run` is, so it may block.
;; `run-sync(row)` is the same core function lifted sync-typed, so a lookup that does not resolve inline traps it.
;; `run-async(row)` is async-typed too, and lowers the lookup WITH the async option, the other lower the host serves
;; it through: it waits for the subtask in waitable-set.wait when the lower does not return RETURNED.
;; Each row resolves one name and returns four bytes packed into a u32, byte 0 the low one:
;;   byte 0: 0 for ok, 0x80 | the error-code case for an error (0x81 invalid-argument, 0x84 permanent-resolver-failure);
;;   byte 1: 1 if the list is not empty;
;;   byte 2: 1 if the list holds the row's expected address (below);
;;   byte 3: 1 if every address in the list is loopback (127.0.0.0/8 or ::1).
;; The rows, and the address each expects:
;;   0 "localhost" 127.0.0.1     3 "a b"   -             6 "127.1"             127.0.0.1 (an IPv4 shorthand)
;;   1 "127.0.0.1" 127.0.0.1     4 ""      -             7 "::ffff:127.0.0.1"  that IPv6 address, as given
;;   2 "::1"       ::1           5 "[::1]" ::1           8 "fe80::1%1"         -
;; No row needs a DNS server: localhost is answered from the hosts file, the literals are parsed, and the rest are
;; refused as invalid. wasmtime 49.0.3 resolves every row but localhost inline (run-sync returns them).
;;
;; result<list<ip-address>, error-code> is lowered at the return pointer, 256: its case byte at +0, then at +4 the
;; list's (ptr, len) or the error-code's case byte. An ip-address is 18 bytes, align 2: its case byte, then at +2
;; the four ipv4 octets or the eight ipv6 u16s. The list's backing comes from the bump realloc below, from 4096.
;;
;; Assembled with `wasm-tools parse name-lookup.wat -o name-lookup.wasm` (wasm-tools 1.258.0).
(component
  (type $lookup-t (instance
    (type $ip (variant (case "ipv4" (tuple u8 u8 u8 u8)) (case "ipv6" (tuple u16 u16 u16 u16 u16 u16 u16 u16))))
    (export "ip-address" (type $ip2 (eq $ip)))
    (type $ec (variant (case "access-denied") (case "invalid-argument") (case "name-unresolvable")
      (case "temporary-resolver-failure") (case "permanent-resolver-failure") (case "other" (option string))))
    (export "error-code" (type $ec2 (eq $ec)))
    (type $l (list $ip2))
    (type $r (result $l (error $ec2)))
    (export "resolve-addresses" (func async (param "name" string) (result $r)))))
  (import "wasi:sockets/ip-name-lookup@0.3.0" (instance $nl (type $lookup-t)))
  (alias export $nl "resolve-addresses" (func $resolve))
  (core module $mm
    (memory (export "m") 1)
    (global $bump (mut i32) (i32.const 4096))
    ;; A bump allocator: align up, hand out, never free.
    (func (export "realloc") (param i32 i32 i32 i32) (result i32)
      (local $p i32)
      (local.set $p (i32.and (i32.add (global.get $bump) (i32.sub (local.get 2) (i32.const 1)))
        (i32.sub (i32.const 0) (local.get 2))))
      (global.set $bump (i32.add (local.get $p) (local.get 3)))
      (local.get $p))
    ;; The names, at 16 + 32*row.
    (data (i32.const 16) "localhost")
    (data (i32.const 48) "127.0.0.1")
    (data (i32.const 80) "::1")
    (data (i32.const 112) "a b")
    (data (i32.const 176) "[::1]")
    (data (i32.const 208) "127.1")
    (data (i32.const 240) "::ffff:127.0.0.1")
    (data (i32.const 272) "fe80::1%1")
    ;; At 512 + 16*row, the name's length (u32), then the expected address: its case byte at +4, and for ipv6 its
    ;; last three segments, u16s at +6, +8 and +10 (the first five are zero). An ipv4 row expects 127.0.0.1.
    (data (i32.const 512)
      "\09\00\00\00\00\00\00\00\00\00\00\00\00\00\00\00"
      "\09\00\00\00\00\00\00\00\00\00\00\00\00\00\00\00"
      "\03\00\00\00\01\00\00\00\00\00\01\00\00\00\00\00"
      "\03\00\00\00\00\00\00\00\00\00\00\00\00\00\00\00"
      "\00\00\00\00\00\00\00\00\00\00\00\00\00\00\00\00"
      "\05\00\00\00\01\00\00\00\00\00\01\00\00\00\00\00"
      "\05\00\00\00\00\00\00\00\00\00\00\00\00\00\00\00"
      "\10\00\00\00\01\00\ff\ff\00\7f\01\00\00\00\00\00"
      "\09\00\00\00\00\00\00\00\00\00\00\00\00\00\00\00"))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (alias core export $mi "realloc" (core func $realloc))
  (core func $resolve-low (canon lower (func $resolve) (memory $m) (realloc $realloc) string-encoding=utf8))
  (core func $resolve-async-low (canon lower (func $resolve) async (memory $m) (realloc $realloc) string-encoding=utf8))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $m)))
  (core func $stdrop (canon subtask.drop))
  (core module $gm
    (import "" "m" (memory 1))
    (import "" "resolve" (func $resolve (param i32 i32 i32)))
    (import "" "resolve-async" (func $resolve-async (param i32 i32 i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (import "" "stdrop" (func $stdrop (param i32)))
    ;; is-v4-loopback1 reports whether the ip-address at p is 127.0.0.1.
    (func $is-v4-loopback1 (param $p i32) (result i32)
      (i32.and (i32.eqz (i32.load8_u (local.get $p)))
        (i32.eq (i32.load offset=2 (local.get $p)) (i32.const 0x0100007f))))
    ;; is-v6 reports whether the ip-address at p is the ipv6 address 0:0:0:0:0:s5:s6:s7: the load at +2 covers the
    ;; first four segments, and the u16s at +10..+17 are the last four.
    (func $is-v6 (param $p i32) (param $s5 i32) (param $s6 i32) (param $s7 i32) (result i32)
      (i32.and
        (i32.and (i32.eq (i32.load8_u (local.get $p)) (i32.const 1)) (i64.eqz (i64.load offset=2 (local.get $p))))
        (i32.and
          (i32.and (i32.eqz (i32.load16_u offset=10 (local.get $p)))
            (i32.eq (i32.load16_u offset=12 (local.get $p)) (local.get $s5)))
          (i32.and (i32.eq (i32.load16_u offset=14 (local.get $p)) (local.get $s6))
            (i32.eq (i32.load16_u offset=16 (local.get $p)) (local.get $s7))))))
    (func $loopback (param $p i32) (result i32)
      (if (result i32) (i32.eqz (i32.load8_u (local.get $p)))
        (then (i32.eq (i32.load8_u offset=2 (local.get $p)) (i32.const 127)))
        (else (call $is-v6 (local.get $p) (i32.const 0) (i32.const 0) (i32.const 1)))))
    (func (export "run") (param $row i32) (result i32)
      (call $resolve (i32.add (i32.const 16) (i32.shl (local.get $row) (i32.const 5)))
        (i32.load (i32.add (i32.const 512) (i32.shl (local.get $row) (i32.const 4)))) (i32.const 256))
      (call $read (local.get $row)))
    ;; run-async lowers the same lookup WITH the async option, and waits for its subtask in waitable-set.wait when the
    ;; lower does not return RETURNED (2), then reads the result the subtask's resolution stored at 256.
    (func (export "run-async") (param $row i32) (result i32)
      (local $packed i32) (local $set i32)
      (local.set $packed (call $resolve-async (i32.add (i32.const 16) (i32.shl (local.get $row) (i32.const 5)))
        (i32.load (i32.add (i32.const 512) (i32.shl (local.get $row) (i32.const 4)))) (i32.const 256)))
      (if (i32.ne (local.get $packed) (i32.const 2))
        (then
          (local.set $set (call $wsnew))
          (call $wsjoin (i32.shr_u (local.get $packed) (i32.const 4)) (local.get $set))
          (drop (call $wswait (local.get $set) (i32.const 300)))
          (call $stdrop (i32.shr_u (local.get $packed) (i32.const 4)))))
      (call $read (local.get $row)))
    ;; read packs the result at 256 into the four bytes the header lists.
    (func $read (param $row i32) (result i32)
      (local $want i32) (local $ptr i32) (local $len i32) (local $i i32) (local $e i32) (local $has i32)
      (local $all i32) (local $hit i32)
      (local.set $want (i32.add (i32.const 512) (i32.shl (local.get $row) (i32.const 4))))
      (if (i32.load8_u (i32.const 256))
        (then (return (i32.or (i32.const 0x80) (i32.load8_u (i32.const 260))))))
      (local.set $ptr (i32.load (i32.const 260)))
      (local.set $len (i32.load (i32.const 264)))
      (local.set $all (i32.const 1))
      (block $done
        (loop $next
          (br_if $done (i32.ge_u (local.get $i) (local.get $len)))
          (local.set $e (i32.add (local.get $ptr) (i32.mul (local.get $i) (i32.const 18))))
          (if (i32.load8_u offset=4 (local.get $want))
            (then (local.set $hit (call $is-v6 (local.get $e) (i32.load16_u offset=6 (local.get $want))
              (i32.load16_u offset=8 (local.get $want)) (i32.load16_u offset=10 (local.get $want)))))
            (else (local.set $hit (call $is-v4-loopback1 (local.get $e)))))
          (if (local.get $hit) (then (local.set $has (i32.const 1))))
          (if (i32.eqz (call $loopback (local.get $e))) (then (local.set $all (i32.const 0))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $next)))
      (i32.or (i32.shl (i32.ne (local.get $len) (i32.const 0)) (i32.const 8))
        (i32.or (i32.shl (local.get $has) (i32.const 16)) (i32.shl (local.get $all) (i32.const 24))))))
  (core instance $gi (instantiate $gm (with "" (instance (export "m" (memory $m)) (export "resolve" (func $resolve-low))
    (export "resolve-async" (func $resolve-async-low)) (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
    (export "wswait" (func $wswait)) (export "stdrop" (func $stdrop))))))
  (type $at (func async (param "row" u32) (result u32)))
  (type $st (func (param "row" u32) (result u32)))
  (func (export "run") (type $at) (canon lift (core func $gi "run")))
  (func (export "run-sync") (type $st) (canon lift (core func $gi "run")))
  (func (export "run-async") (type $at) (canon lift (core func $gi "run-async"))))
