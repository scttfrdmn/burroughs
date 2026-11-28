;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; udp (#1376 E3, ADR 0140): wasi:sockets@0.3's whole udp-socket resource at the ABI, below std. `run(row)` returns
;; four result bytes packed into a u32, byte 0 the low one: an operation's byte is 0 for ok and 0x80 | its error-code
;; case for an error (0x80 access-denied, 0x82 invalid-argument, 0x85 invalid-state, 0x86 address-not-bindable, 0x87
;; address-in-use, 0x89 connection-refused, 0x8d datagram-too-large), and a property's byte is 0 or 1. Every socket a
;; row uses is its own, so no row needs a peer: two sockets of one row talk to each other over loopback. Each row's
;; comment below says what its four bytes are; udp.reading has the readings.
;;
;; send and receive are `async func`s. Every row but `sync-lowers` lowers them WITH the async option: send's 16 flat
;; parameters spill to the tuple at 160 (MAX_FLAT_ASYNC_PARAMS is 4), and each call is waited for in
;; waitable-set.wait when it does not return RETURNED, then its subtask is dropped. `sync-lowers` lowers both with no
;; async option, the blocking sync lower: send's 16 flat parameters then cross flat (MAX_FLAT_PARAMS is 16). `run` is
;; async-typed and lifted with no async option, as Rust std's `run` is, so it may block.
;;
;; Every result is lowered at a return pointer, its case byte at +0 and its payload at +4 (+8 for a u64):
;;   create at 0, the handle at 4;     an operation's result<_, error-code> at 16, its case at 20;
;;   get-local-address at 32 and get-remote-address at 96, the ip-socket-address at +4;
;;   send's result at 224; receive's at 256: the list's (ptr, len) at 260 and 264 and the sender at 268;
;;   get-unicast-hop-limit at 320, its u8 at 324; a buffer size at 336, its u64 at 344.
;; An ip-socket-address is 32 bytes, align 4: its case byte, then at +4 the port, then ipv4's octets at +6, or ipv6's
;; flow-info at +8, its eight u16s at +12 and its scope-id at +28. Rows keep their sockets' local addresses at 400 (A),
;; 432 (B) and 464 (C), and build arguments at 600. Data is sent from 0x20000 and 0x30000, and receive's list is
;; allocated by the bump realloc from 0x40000.
;;
;; Assembled with `wasm-tools parse udp.wat -o udp.wasm` (wasm-tools 1.258.0).
(component
  (type $sock-t (instance
    (export "udp-socket" (type (sub resource)))
    (type $ec (variant (case "access-denied") (case "not-supported") (case "invalid-argument")
      (case "out-of-memory") (case "timeout") (case "invalid-state") (case "address-not-bindable")
      (case "address-in-use") (case "remote-unreachable") (case "connection-refused") (case "connection-broken")
      (case "connection-reset") (case "connection-aborted") (case "datagram-too-large") (case "other" (option string))))
    (export "error-code" (type $ec2 (eq $ec)))
    (type $fam (enum "ipv4" "ipv6"))
    (export "ip-address-family" (type $fam2 (eq $fam)))
    (type $v4 (record (field "port" u16) (field "address" (tuple u8 u8 u8 u8))))
    (export "ipv4-socket-address" (type $v4e (eq $v4)))
    (type $v6 (record (field "port" u16) (field "flow-info" u32)
      (field "address" (tuple u16 u16 u16 u16 u16 u16 u16 u16)) (field "scope-id" u32)))
    (export "ipv6-socket-address" (type $v6e (eq $v6)))
    (type $addr (variant (case "ipv4" $v4e) (case "ipv6" $v6e)))
    (export "ip-socket-address" (type $addr2 (eq $addr)))
    (type $cr (result (own 0) (error $ec2)))
    (export "[static]udp-socket.create" (func (param "address-family" $fam2) (result $cr)))
    (type $r (result (error $ec2)))
    (export "[method]udp-socket.bind" (func (param "self" (borrow 0)) (param "local-address" $addr2) (result $r)))
    (export "[method]udp-socket.connect" (func (param "self" (borrow 0)) (param "remote-address" $addr2) (result $r)))
    (export "[method]udp-socket.disconnect" (func (param "self" (borrow 0)) (result $r)))
    (type $bytes (list u8))
    (type $oaddr (option $addr2))
    (export "[method]udp-socket.send" (func async (param "self" (borrow 0)) (param "data" $bytes)
      (param "remote-address" $oaddr) (result $r)))
    (type $msg (tuple $bytes $addr2))
    (type $rr (result $msg (error $ec2)))
    (export "[method]udp-socket.receive" (func async (param "self" (borrow 0)) (result $rr)))
    (type $ar (result $addr2 (error $ec2)))
    (export "[method]udp-socket.get-local-address" (func (param "self" (borrow 0)) (result $ar)))
    (export "[method]udp-socket.get-remote-address" (func (param "self" (borrow 0)) (result $ar)))
    (export "[method]udp-socket.get-address-family" (func (param "self" (borrow 0)) (result $fam2)))
    (type $hr (result u8 (error $ec2)))
    (export "[method]udp-socket.get-unicast-hop-limit" (func (param "self" (borrow 0)) (result $hr)))
    (export "[method]udp-socket.set-unicast-hop-limit" (func (param "self" (borrow 0)) (param "value" u8) (result $r)))
    (type $br (result u64 (error $ec2)))
    (export "[method]udp-socket.get-receive-buffer-size" (func (param "self" (borrow 0)) (result $br)))
    (export "[method]udp-socket.set-receive-buffer-size" (func (param "self" (borrow 0)) (param "value" u64) (result $r)))
    (export "[method]udp-socket.get-send-buffer-size" (func (param "self" (borrow 0)) (result $br)))
    (export "[method]udp-socket.set-send-buffer-size" (func (param "self" (borrow 0)) (param "value" u64) (result $r)))))
  (import "wasi:sockets/types@0.3.0" (instance $s (type $sock-t)))
  (alias export $s "[static]udp-socket.create" (func $create))
  (alias export $s "[method]udp-socket.bind" (func $bind))
  (alias export $s "[method]udp-socket.connect" (func $connect))
  (alias export $s "[method]udp-socket.disconnect" (func $disconnect))
  (alias export $s "[method]udp-socket.send" (func $send))
  (alias export $s "[method]udp-socket.receive" (func $recv))
  (alias export $s "[method]udp-socket.get-local-address" (func $local))
  (alias export $s "[method]udp-socket.get-remote-address" (func $remote))
  (alias export $s "[method]udp-socket.get-address-family" (func $family))
  (alias export $s "[method]udp-socket.get-unicast-hop-limit" (func $hopget))
  (alias export $s "[method]udp-socket.set-unicast-hop-limit" (func $hopset))
  (alias export $s "[method]udp-socket.get-receive-buffer-size" (func $rbufget))
  (alias export $s "[method]udp-socket.set-receive-buffer-size" (func $rbufset))
  (alias export $s "[method]udp-socket.get-send-buffer-size" (func $sbufget))
  (alias export $s "[method]udp-socket.set-send-buffer-size" (func $sbufset))
  (alias export $s "udp-socket" (type $udp))
  (core module $mm
    (memory (export "m") 8)
    (global $bump (mut i32) (i32.const 0x40000))
    ;; A bump allocator: align up, hand out, never free.
    (func (export "realloc") (param i32 i32 i32 i32) (result i32)
      (local $p i32)
      (local.set $p (i32.and (i32.add (global.get $bump) (i32.sub (local.get 2) (i32.const 1)))
        (i32.sub (i32.const 0) (local.get 2))))
      (global.set $bump (i32.add (local.get $p) (local.get 3)))
      (local.get $p)))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (alias core export $mi "realloc" (core func $realloc))
  (core func $create-low (canon lower (func $create) (memory $m) (realloc $realloc)))
  (core func $bind-low (canon lower (func $bind) (memory $m) (realloc $realloc)))
  (core func $connect-low (canon lower (func $connect) (memory $m) (realloc $realloc)))
  (core func $disconnect-low (canon lower (func $disconnect) (memory $m) (realloc $realloc)))
  (core func $send-low (canon lower (func $send) async (memory $m) (realloc $realloc)))
  (core func $send-sync-low (canon lower (func $send) (memory $m) (realloc $realloc)))
  (core func $recv-low (canon lower (func $recv) async (memory $m) (realloc $realloc)))
  (core func $recv-sync-low (canon lower (func $recv) (memory $m) (realloc $realloc)))
  (core func $local-low (canon lower (func $local) (memory $m) (realloc $realloc)))
  (core func $remote-low (canon lower (func $remote) (memory $m) (realloc $realloc)))
  (core func $family-low (canon lower (func $family)))
  (core func $hopget-low (canon lower (func $hopget) (memory $m) (realloc $realloc)))
  (core func $hopset-low (canon lower (func $hopset) (memory $m) (realloc $realloc)))
  (core func $rbufget-low (canon lower (func $rbufget) (memory $m) (realloc $realloc)))
  (core func $rbufset-low (canon lower (func $rbufset) (memory $m) (realloc $realloc)))
  (core func $sbufget-low (canon lower (func $sbufget) (memory $m) (realloc $realloc)))
  (core func $sbufset-low (canon lower (func $sbufset) (memory $m) (realloc $realloc)))
  (core func $drop-low (canon resource.drop $udp))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $m)))
  (core func $stcancel (canon subtask.cancel async))
  (core func $stdrop (canon subtask.drop))
  (core module $gm
    (import "" "m" (memory 8))
    (import "" "create" (func $create-low (param i32 i32)))
    (import "" "bind" (func $bind-low (param i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32)))
    (import "" "connect" (func $connect-low (param i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32)))
    (import "" "disconnect" (func $disconnect-low (param i32 i32)))
    (import "" "send" (func $send-low (param i32 i32) (result i32)))
    (import "" "send-sync" (func $send-sync-low
      (param i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32)))
    (import "" "recv" (func $recv-low (param i32 i32) (result i32)))
    (import "" "recv-sync" (func $recv-sync-low (param i32 i32)))
    (import "" "local" (func $local-low (param i32 i32)))
    (import "" "remote" (func $remote-low (param i32 i32)))
    (import "" "family" (func $family-low (param i32) (result i32)))
    (import "" "hopget" (func $hopget-low (param i32 i32)))
    (import "" "hopset" (func $hopset-low (param i32 i32 i32)))
    (import "" "rbufget" (func $rbufget-low (param i32 i32)))
    (import "" "rbufset" (func $rbufset-low (param i32 i64 i32)))
    (import "" "sbufget" (func $sbufget-low (param i32 i32)))
    (import "" "sbufset" (func $sbufset-low (param i32 i64 i32)))
    (import "" "drop" (func $drop-low (param i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (import "" "stcancel" (func $stcancel (param i32) (result i32)))
    (import "" "stdrop" (func $stdrop (param i32)))

    ;; ---- results, addresses and bytes ----

    ;; res: the result at ret, 0 for ok and 0x80 | its error-code case for an error.
    (func $res (param $ret i32) (result i32)
      (if (result i32) (i32.eqz (i32.load8_u (local.get $ret)))
        (then (i32.const 0))
        (else (i32.or (i32.const 0x80) (i32.load8_u offset=4 (local.get $ret))))))
    (func $pack (param $b0 i32) (param $b1 i32) (param $b2 i32) (param $b3 i32) (result i32)
      (i32.or (i32.or (i32.and (local.get $b0) (i32.const 0xff)) (i32.shl (i32.and (local.get $b1) (i32.const 0xff)) (i32.const 8)))
        (i32.or (i32.shl (i32.and (local.get $b2) (i32.const 0xff)) (i32.const 16))
          (i32.shl (i32.and (local.get $b3) (i32.const 0xff)) (i32.const 24)))))
    ;; v4: the ip-socket-address a.b.c.d:port at p.
    (func $v4 (param $p i32) (param $a i32) (param $b i32) (param $c i32) (param $d i32) (param $port i32)
      (memory.fill (local.get $p) (i32.const 0) (i32.const 32))
      (i32.store16 offset=4 (local.get $p) (local.get $port))
      (i32.store8 offset=6 (local.get $p) (local.get $a))
      (i32.store8 offset=7 (local.get $p) (local.get $b))
      (i32.store8 offset=8 (local.get $p) (local.get $c))
      (i32.store8 offset=9 (local.get $p) (local.get $d)))
    ;; v6: the ip-socket-address [::s5:s6:s7]:port at p, its first five segments zero.
    (func $v6 (param $p i32) (param $s5 i32) (param $s6 i32) (param $s7 i32) (param $port i32)
      (memory.fill (local.get $p) (i32.const 0) (i32.const 32))
      (i32.store8 (local.get $p) (i32.const 1))
      (i32.store16 offset=4 (local.get $p) (local.get $port))
      (i32.store16 offset=22 (local.get $p) (local.get $s5))
      (i32.store16 offset=24 (local.get $p) (local.get $s6))
      (i32.store16 offset=26 (local.get $p) (local.get $s7)))
    (func $lo4 (param $p i32) (param $port i32)
      (call $v4 (local.get $p) (i32.const 127) (i32.const 0) (i32.const 0) (i32.const 1) (local.get $port)))
    (func $lo6 (param $p i32) (param $port i32)
      (call $v6 (local.get $p) (i32.const 0) (i32.const 0) (i32.const 1) (local.get $port)))
    (func $port (param $p i32) (result i32) (i32.load16_u offset=4 (local.get $p)))
    ;; addreq: the two addresses' case, port and address are equal.
    (func $addreq (param $p i32) (param $q i32) (result i32)
      (if (i32.ne (i32.load8_u (local.get $p)) (i32.load8_u (local.get $q))) (then (return (i32.const 0))))
      (if (i32.ne (i32.load16_u offset=4 (local.get $p)) (i32.load16_u offset=4 (local.get $q))) (then (return (i32.const 0))))
      (if (result i32) (i32.eqz (i32.load8_u (local.get $p)))
        (then (i32.eq (i32.load offset=6 (local.get $p)) (i32.load offset=6 (local.get $q))))
        (else (i32.and (i64.eq (i64.load offset=12 (local.get $p)) (i64.load offset=12 (local.get $q)))
          (i64.eq (i64.load offset=20 (local.get $p)) (i64.load offset=20 (local.get $q)))))))
    ;; islo: the address is 127.0.0.1 (ipv4) or ::1 (ipv6).
    (func $islo (param $p i32) (result i32)
      (if (result i32) (i32.eqz (i32.load8_u (local.get $p)))
        (then (i32.eq (i32.load offset=6 (local.get $p)) (i32.const 0x0100007f)))
        (else (i32.and (i64.eqz (i64.load offset=12 (local.get $p)))
          (i64.eq (i64.load offset=20 (local.get $p)) (i64.const 0x0001000000000000))))))
    ;; isany: the address is 0.0.0.0 (ipv4) or :: (ipv6).
    (func $isany (param $p i32) (result i32)
      (if (result i32) (i32.eqz (i32.load8_u (local.get $p)))
        (then (i32.eqz (i32.load offset=6 (local.get $p))))
        (else (i32.and (i64.eqz (i64.load offset=12 (local.get $p))) (i64.eqz (i64.load offset=20 (local.get $p)))))))
    ;; fill: len bytes of a pattern at p, seeded.
    (func $fill (param $p i32) (param $len i32) (param $seed i32) (local $i i32)
      (block $done (loop $next
        (br_if $done (i32.ge_u (local.get $i) (local.get $len)))
        (i32.store8 (i32.add (local.get $p) (local.get $i))
          (i32.add (i32.mul (local.get $i) (i32.const 7)) (local.get $seed)))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $next))))
    (func $eqbytes (param $p i32) (param $q i32) (param $len i32) (result i32) (local $i i32)
      (block $done (loop $next
        (br_if $done (i32.ge_u (local.get $i) (local.get $len)))
        (if (i32.ne (i32.load8_u (i32.add (local.get $p) (local.get $i))) (i32.load8_u (i32.add (local.get $q) (local.get $i))))
          (then (return (i32.const 0))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $next)))
      (i32.const 1))
    ;; got: the last receive's datagram is the len bytes at src.
    (func $got (param $src i32) (param $len i32) (result i32)
      (i32.and (i32.eq (i32.load (i32.const 264)) (local.get $len))
        (call $eqbytes (i32.load (i32.const 260)) (local.get $src) (local.get $len))))
    ;; from: the last receive's sender is the address at p.
    (func $from (param $p i32) (result i32) (call $addreq (i32.const 268) (local.get $p)))

    ;; ---- the methods ----

    ;; create: a fresh socket of the family, its handle; its result's byte is kept in $cres.
    (global $cres (mut i32) (i32.const 0))
    (func $create (param $fam i32) (result i32)
      (call $create-low (local.get $fam) (i32.const 0))
      (global.set $cres (call $res (i32.const 0)))
      (i32.load (i32.const 4)))
    (func $bindp (param $s i32) (param $p i32) (result i32)
      (if (i32.eqz (i32.load8_u (local.get $p)))
        (then (call $bind-low (local.get $s) (i32.const 0) (i32.load16_u offset=4 (local.get $p))
          (i32.load8_u offset=6 (local.get $p)) (i32.load8_u offset=7 (local.get $p))
          (i32.load8_u offset=8 (local.get $p)) (i32.load8_u offset=9 (local.get $p))
          (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 16)))
        (else (call $bind-low (local.get $s) (i32.const 1) (i32.load16_u offset=4 (local.get $p))
          (i32.load offset=8 (local.get $p))
          (i32.load16_u offset=12 (local.get $p)) (i32.load16_u offset=14 (local.get $p))
          (i32.load16_u offset=16 (local.get $p)) (i32.load16_u offset=18 (local.get $p))
          (i32.load16_u offset=20 (local.get $p)) (i32.load16_u offset=22 (local.get $p))
          (i32.load16_u offset=24 (local.get $p)) (i32.load16_u offset=26 (local.get $p))
          (i32.load offset=28 (local.get $p)) (i32.const 16))))
      (call $res (i32.const 16)))
    (func $connectp (param $s i32) (param $p i32) (result i32)
      (if (i32.eqz (i32.load8_u (local.get $p)))
        (then (call $connect-low (local.get $s) (i32.const 0) (i32.load16_u offset=4 (local.get $p))
          (i32.load8_u offset=6 (local.get $p)) (i32.load8_u offset=7 (local.get $p))
          (i32.load8_u offset=8 (local.get $p)) (i32.load8_u offset=9 (local.get $p))
          (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 16)))
        (else (call $connect-low (local.get $s) (i32.const 1) (i32.load16_u offset=4 (local.get $p))
          (i32.load offset=8 (local.get $p))
          (i32.load16_u offset=12 (local.get $p)) (i32.load16_u offset=14 (local.get $p))
          (i32.load16_u offset=16 (local.get $p)) (i32.load16_u offset=18 (local.get $p))
          (i32.load16_u offset=20 (local.get $p)) (i32.load16_u offset=22 (local.get $p))
          (i32.load16_u offset=24 (local.get $p)) (i32.load16_u offset=26 (local.get $p))
          (i32.load offset=28 (local.get $p)) (i32.const 16))))
      (call $res (i32.const 16)))
    (func $disconnect (param $s i32) (result i32)
      (call $disconnect-low (local.get $s) (i32.const 16))
      (call $res (i32.const 16)))
    ;; await: wait for the subtask a packed async-lower status names, unless it returned inline; then drop it.
    (func $await (param $st i32) (local $set i32)
      (if (i32.ne (i32.and (local.get $st) (i32.const 0xf)) (i32.const 2))
        (then
          (local.set $set (call $wsnew))
          (call $wsjoin (i32.shr_u (local.get $st) (i32.const 4)) (local.get $set))
          (drop (call $wswait (local.get $set) (i32.const 384)))
          (call $stdrop (i32.shr_u (local.get $st) (i32.const 4))))))
    ;; cancel: subtask.cancel of the subtask i, waited for in waitable-set.wait when it returns BLOCKED; the state it
    ;; resolved in, with 0x10 set when the cancel blocked. The subtask is dropped.
    (func $cancel (param $i i32) (result i32) (local $r i32) (local $set i32)
      (local.set $r (call $stcancel (local.get $i)))
      (if (i32.eq (local.get $r) (i32.const -1))
        (then
          (local.set $set (call $wsnew))
          (call $wsjoin (local.get $i) (local.get $set))
          (drop (call $wswait (local.get $set) (i32.const 384)))
          (local.set $r (i32.or (i32.const 0x10) (i32.load (i32.const 388))))))
      (call $stdrop (local.get $i))
      (local.get $r))
    ;; send: len bytes at data, to the address at p (0 for none), through the async lower with the tuple at 160.
    (func $send (param $s i32) (param $data i32) (param $len i32) (param $p i32) (result i32)
      (memory.fill (i32.const 160) (i32.const 0) (i32.const 48))
      (i32.store (i32.const 160) (local.get $s))
      (i32.store (i32.const 164) (local.get $data))
      (i32.store (i32.const 168) (local.get $len))
      (if (local.get $p)
        (then (i32.store8 (i32.const 172) (i32.const 1)) (memory.copy (i32.const 176) (local.get $p) (i32.const 32))))
      (call $await (call $send-low (i32.const 160) (i32.const 224)))
      (call $res (i32.const 224)))
    ;; send-sync: the same through the blocking sync lower, its 16 flat parameters unspilled; p must be some.
    (func $send-sync (param $s i32) (param $data i32) (param $len i32) (param $p i32) (result i32)
      (if (i32.eqz (i32.load8_u (local.get $p)))
        (then (call $send-sync-low (local.get $s) (local.get $data) (local.get $len) (i32.const 1) (i32.const 0)
          (i32.load16_u offset=4 (local.get $p))
          (i32.load8_u offset=6 (local.get $p)) (i32.load8_u offset=7 (local.get $p))
          (i32.load8_u offset=8 (local.get $p)) (i32.load8_u offset=9 (local.get $p))
          (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 224)))
        (else (call $send-sync-low (local.get $s) (local.get $data) (local.get $len) (i32.const 1) (i32.const 1)
          (i32.load16_u offset=4 (local.get $p)) (i32.load offset=8 (local.get $p))
          (i32.load16_u offset=12 (local.get $p)) (i32.load16_u offset=14 (local.get $p))
          (i32.load16_u offset=16 (local.get $p)) (i32.load16_u offset=18 (local.get $p))
          (i32.load16_u offset=20 (local.get $p)) (i32.load16_u offset=22 (local.get $p))
          (i32.load16_u offset=24 (local.get $p)) (i32.load16_u offset=26 (local.get $p))
          (i32.load offset=28 (local.get $p)) (i32.const 224))))
      (call $res (i32.const 224)))
    (func $recv (param $s i32) (result i32)
      (call $await (call $recv-low (local.get $s) (i32.const 256)))
      (call $res (i32.const 256)))
    (func $recv-sync (param $s i32) (result i32)
      (call $recv-sync-low (local.get $s) (i32.const 256))
      (call $res (i32.const 256)))
    ;; local and remote: the getter's result; when it is ok and dst is not 0, the address is copied to dst.
    (func $local (param $s i32) (param $dst i32) (result i32) (local $r i32)
      (call $local-low (local.get $s) (i32.const 32))
      (local.set $r (call $res (i32.const 32)))
      (if (i32.and (i32.eqz (local.get $r)) (i32.ne (local.get $dst) (i32.const 0)))
        (then (memory.copy (local.get $dst) (i32.const 36) (i32.const 32))))
      (local.get $r))
    (func $remote (param $s i32) (param $dst i32) (result i32) (local $r i32)
      (call $remote-low (local.get $s) (i32.const 96))
      (local.set $r (call $res (i32.const 96)))
      (if (i32.and (i32.eqz (local.get $r)) (i32.ne (local.get $dst) (i32.const 0)))
        (then (memory.copy (local.get $dst) (i32.const 100) (i32.const 32))))
      (local.get $r))
    (func $hopget (param $s i32) (result i32)
      (call $hopget-low (local.get $s) (i32.const 320))
      (call $res (i32.const 320)))
    (func $hopset (param $s i32) (param $v i32) (result i32)
      (call $hopset-low (local.get $s) (local.get $v) (i32.const 16))
      (call $res (i32.const 16)))
    (func $rbufget (param $s i32) (result i32)
      (call $rbufget-low (local.get $s) (i32.const 336))
      (call $res (i32.const 336)))
    (func $rbufset (param $s i32) (param $v i64) (result i32)
      (call $rbufset-low (local.get $s) (local.get $v) (i32.const 16))
      (call $res (i32.const 16)))
    (func $sbufget (param $s i32) (result i32)
      (call $sbufget-low (local.get $s) (i32.const 336))
      (call $res (i32.const 336)))
    (func $sbufset (param $s i32) (param $v i64) (result i32)
      (call $sbufset-low (local.get $s) (local.get $v) (i32.const 16))
      (call $res (i32.const 16)))
    (func $size (result i64) (i64.load (i32.const 344)))
    ;; bound4 and bound6: a fresh socket bound to 127.0.0.1:0 or [::1]:0, its local address copied to dst.
    (func $bound4 (param $dst i32) (result i32) (local $s i32)
      (local.set $s (call $create (i32.const 0)))
      (call $lo4 (i32.const 600) (i32.const 0))
      (drop (call $bindp (local.get $s) (i32.const 600)))
      (drop (call $local (local.get $s) (local.get $dst)))
      (local.get $s))
    (func $bound6 (param $dst i32) (result i32) (local $s i32)
      (local.set $s (call $create (i32.const 1)))
      (call $lo6 (i32.const 600) (i32.const 0))
      (drop (call $bindp (local.get $s) (i32.const 600)))
      (drop (call $local (local.get $s) (local.get $dst)))
      (local.get $s))

    ;; ---- the rows ----

    (func (export "run") (param $row i32) (result i32)
      (local $a i32) (local $b i32) (local $c i32) (local $r0 i32) (local $r1 i32) (local $r2 i32) (local $r3 i32)
      (local $st i32)
      (call $fill (i32.const 0x20000) (i32.const 0x10001) (i32.const 3))
      (call $fill (i32.const 0x30000) (i32.const 64) (i32.const 101))

      ;; 0 bind-v4: bind 127.0.0.1:0; get-local-address; its port nonzero; it is ipv4 127.0.0.1.
      (if (i32.eq (local.get $row) (i32.const 0)) (then
        (local.set $a (call $create (i32.const 0)))
        (call $lo4 (i32.const 600) (i32.const 0))
        (return (call $pack (call $bindp (local.get $a) (i32.const 600)) (call $local (local.get $a) (i32.const 400))
          (i32.ne (call $port (i32.const 400)) (i32.const 0))
          (i32.and (i32.eqz (i32.load8_u (i32.const 400))) (call $islo (i32.const 400)))))))
      ;; 1 bind-v6: bind [::1]:0; get-local-address; its port nonzero; it is ipv6 ::1.
      (if (i32.eq (local.get $row) (i32.const 1)) (then
        (local.set $a (call $create (i32.const 1)))
        (call $lo6 (i32.const 600) (i32.const 0))
        (return (call $pack (call $bindp (local.get $a) (i32.const 600)) (call $local (local.get $a) (i32.const 400))
          (i32.ne (call $port (i32.const 400)) (i32.const 0))
          (i32.and (i32.eq (i32.load8_u (i32.const 400)) (i32.const 1)) (call $islo (i32.const 400)))))))
      ;; 2 fresh-getters: on a fresh ipv4 socket get-local-address and get-remote-address; get-address-family of a fresh
      ;; ipv4 and a fresh ipv6 socket.
      (if (i32.eq (local.get $row) (i32.const 2)) (then
        (local.set $a (call $create (i32.const 0)))
        (local.set $b (call $create (i32.const 1)))
        (return (call $pack (call $local (local.get $a) (i32.const 0)) (call $remote (local.get $a) (i32.const 0))
          (call $family-low (local.get $a)) (call $family-low (local.get $b))))))
      ;; 3 bind-refusals: bind 127.0.0.1:0, then again; an ipv4 socket bound to [::1]:0; an ipv6 socket to 127.0.0.1:0.
      (if (i32.eq (local.get $row) (i32.const 3)) (then
        (local.set $a (call $create (i32.const 0)))
        (call $lo4 (i32.const 600) (i32.const 0))
        (local.set $r0 (call $bindp (local.get $a) (i32.const 600)))
        (local.set $r1 (call $bindp (local.get $a) (i32.const 600)))
        (call $lo6 (i32.const 600) (i32.const 0))
        (local.set $r2 (call $bindp (call $create (i32.const 0)) (i32.const 600)))
        (call $lo4 (i32.const 600) (i32.const 0))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (call $bindp (call $create (i32.const 1)) (i32.const 600))))))
      ;; 4 bind-more: an ipv6 socket bound to ::ffff:127.0.0.1:0 (ipv4-mapped); an ipv4 socket to 192.0.2.1:0 (no such
      ;; interface); a second socket to the port the first holds; an ipv4 socket to 0.0.0.0:0 and its local address the
      ;; unspecified one.
      (if (i32.eq (local.get $row) (i32.const 4)) (then
        (call $v6 (i32.const 600) (i32.const 0xffff) (i32.const 0x7f00) (i32.const 1) (i32.const 0))
        (local.set $r0 (call $bindp (call $create (i32.const 1)) (i32.const 600)))
        (call $v4 (i32.const 600) (i32.const 192) (i32.const 0) (i32.const 2) (i32.const 1) (i32.const 0))
        (local.set $r1 (call $bindp (call $create (i32.const 0)) (i32.const 600)))
        (drop (call $bound4 (i32.const 400)))
        (local.set $r2 (call $bindp (call $create (i32.const 0)) (i32.const 400)))
        (local.set $a (call $create (i32.const 0)))
        (call $v4 (i32.const 600) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0))
        (local.set $r3 (call $bindp (local.get $a) (i32.const 600)))
        (drop (call $local (local.get $a) (i32.const 432)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.or (local.get $r3) (i32.eqz (call $isany (i32.const 432))))))))
      ;; 5 sendto-v4: A and B bound to 127.0.0.1:0; A sends 5 bytes to B; B receives; the bytes; the sender is A.
      (if (i32.eq (local.get $row) (i32.const 5)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (return (call $pack (call $send (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 432))
          (call $recv (local.get $b)) (call $got (i32.const 0x20000) (i32.const 5)) (call $from (i32.const 400))))))
      ;; 6 sendto-v6: row 5 over [::1].
      (if (i32.eq (local.get $row) (i32.const 6)) (then
        (local.set $a (call $bound6 (i32.const 400)))
        (local.set $b (call $bound6 (i32.const 432)))
        (return (call $pack (call $send (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 432))
          (call $recv (local.get $b)) (call $got (i32.const 0x20000) (i32.const 5)) (call $from (i32.const 400))))))
      ;; 7 connect-send: A and B bound; A connects to B; A's remote address is B; A sends with no address; B receives
      ;; A's bytes from A.
      (if (i32.eq (local.get $row) (i32.const 7)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $r0 (call $connectp (local.get $a) (i32.const 432)))
        (local.set $r1 (call $remote (local.get $a) (i32.const 464)))
        (local.set $r1 (i32.or (local.get $r1) (i32.eqz (call $addreq (i32.const 464) (i32.const 432)))))
        (local.set $r2 (call $send (local.get $a) (i32.const 0x20000) (i32.const 7) (i32.const 0)))
        (local.set $r3 (call $recv (local.get $b)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.or (local.get $r3) (i32.eqz (i32.and (call $got (i32.const 0x20000) (i32.const 7)) (call $from (i32.const 400)))))))))
      ;; 8 connect-filter: A, B and C bound; B connects to A; C sends to B, then A does; B's receive is A's datagram.
      (if (i32.eq (local.get $row) (i32.const 8)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $c (call $bound4 (i32.const 464)))
        (local.set $r0 (call $connectp (local.get $b) (i32.const 400)))
        (local.set $r1 (call $send (local.get $c) (i32.const 0x30000) (i32.const 9) (i32.const 432)))
        (local.set $r2 (call $send (local.get $a) (i32.const 0x20000) (i32.const 11) (i32.const 432)))
        (local.set $r3 (call $recv (local.get $b)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.or (local.get $r3) (i32.eqz (i32.and (call $got (i32.const 0x20000) (i32.const 11)) (call $from (i32.const 400)))))))))
      ;; 9 send-none: a bound, unconnected socket sends with no address; so does a fresh one.
      (if (i32.eq (local.get $row) (i32.const 9)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (return (call $pack (call $send (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 0))
          (call $send (call $create (i32.const 0)) (i32.const 0x20000) (i32.const 5) (i32.const 0))
          (call $local (local.get $a) (i32.const 0)) (i32.const 0)))))
      ;; 10 send-mismatch: A connected to B sends to C; then to B; B receives it.
      (if (i32.eq (local.get $row) (i32.const 10)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $c (call $bound4 (i32.const 464)))
        (local.set $r0 (call $connectp (local.get $a) (i32.const 432)))
        (local.set $r1 (call $send (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 464)))
        (local.set $r2 (call $send (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 432)))
        (local.set $r3 (call $recv (local.get $b)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.or (local.get $r3) (i32.eqz (call $from (i32.const 400))))))))
      ;; 11 send-implicit: a fresh socket sends to B (the implicit bind); its local address; it is unspecified with a
      ;; nonzero port; B receives from its port.
      (if (i32.eq (local.get $row) (i32.const 11)) (then
        (local.set $a (call $create (i32.const 0)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $r0 (call $send (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 432)))
        (local.set $r1 (call $local (local.get $a) (i32.const 400)))
        (local.set $r2 (i32.and (call $isany (i32.const 400)) (i32.ne (call $port (i32.const 400)) (i32.const 0))))
        (local.set $r3 (call $recv (local.get $b)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.or (local.get $r3) (i32.ne (i32.load16_u (i32.const 272)) (call $port (i32.const 400))))))))
      ;; 12 connect-implicit: a fresh socket connects to B (the implicit bind); its local address; it is 127.0.0.1 with
      ;; a nonzero port; get-remote-address is B.
      (if (i32.eq (local.get $row) (i32.const 12)) (then
        (local.set $a (call $create (i32.const 0)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $r0 (call $connectp (local.get $a) (i32.const 432)))
        (local.set $r1 (call $local (local.get $a) (i32.const 400)))
        (local.set $r2 (i32.and (call $islo (i32.const 400)) (i32.ne (call $port (i32.const 400)) (i32.const 0))))
        (local.set $r3 (call $remote (local.get $a) (i32.const 464)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.or (local.get $r3) (i32.eqz (call $addreq (i32.const 464) (i32.const 432))))))))
      ;; 13 receive-unbound: receive on a fresh socket.
      (if (i32.eq (local.get $row) (i32.const 13)) (then
        (return (call $pack (call $recv (call $create (i32.const 0))) (i32.const 0) (i32.const 0) (i32.const 0)))))
      ;; 14 disconnect: A connected to B disconnects; get-remote-address; disconnects again; sends with no address.
      (if (i32.eq (local.get $row) (i32.const 14)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (drop (call $connectp (local.get $a) (i32.const 432)))
        (return (call $pack (call $disconnect (local.get $a)) (call $remote (local.get $a) (i32.const 0))
          (call $disconnect (local.get $a)) (call $send (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 0))))))
      ;; 15 disconnect-unconnected: a fresh socket disconnects; a bound one does.
      (if (i32.eq (local.get $row) (i32.const 15)) (then
        (return (call $pack (call $disconnect (call $create (i32.const 0)))
          (call $disconnect (call $bound4 (i32.const 400))) (i32.const 0) (i32.const 0)))))
      ;; 16 disconnect-local: A bound to 127.0.0.1:0 connects to B and disconnects; its local address; is it still
      ;; 127.0.0.1; is its port the bound one.
      (if (i32.eq (local.get $row) (i32.const 16)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (drop (call $connectp (local.get $a) (i32.const 432)))
        (local.set $r0 (call $disconnect (local.get $a)))
        (local.set $r1 (call $local (local.get $a) (i32.const 464)))
        (return (call $pack (local.get $r0) (local.get $r1) (call $islo (i32.const 464))
          (i32.eq (call $port (i32.const 464)) (call $port (i32.const 400)))))))
      ;; 17 connect-refusals: an ipv4 socket connects to 0.0.0.0:B's port, to 127.0.0.1:0, to [::1]:B's port; an ipv6
      ;; socket to ::ffff:127.0.0.1:B's port.
      (if (i32.eq (local.get $row) (i32.const 17)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (call $v4 (i32.const 600) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (call $port (i32.const 432)))
        (local.set $r0 (call $connectp (local.get $a) (i32.const 600)))
        (call $lo4 (i32.const 600) (i32.const 0))
        (local.set $r1 (call $connectp (local.get $a) (i32.const 600)))
        (call $lo6 (i32.const 600) (call $port (i32.const 432)))
        (local.set $r2 (call $connectp (local.get $a) (i32.const 600)))
        (call $v6 (i32.const 600) (i32.const 0xffff) (i32.const 0x7f00) (i32.const 1) (call $port (i32.const 432)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (call $connectp (call $bound6 (i32.const 464)) (i32.const 600))))))
      ;; 18 reconnect: A connects to B, then to C; its remote address is C; it sends with no address; C receives from A.
      (if (i32.eq (local.get $row) (i32.const 18)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $c (call $bound4 (i32.const 464)))
        (drop (call $connectp (local.get $a) (i32.const 432)))
        (local.set $r0 (call $connectp (local.get $a) (i32.const 464)))
        (local.set $r1 (call $remote (local.get $a) (i32.const 496)))
        (local.set $r1 (i32.or (local.get $r1) (i32.eqz (call $addreq (i32.const 496) (i32.const 464)))))
        (local.set $r2 (call $send (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 0)))
        (local.set $r3 (call $recv (local.get $c)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.or (local.get $r3) (i32.eqz (call $from (i32.const 400))))))))
      ;; 19 empty: A sends an empty datagram to B; B receives; it is empty; the sender is A.
      (if (i32.eq (local.get $row) (i32.const 19)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (return (call $pack (call $send (local.get $a) (i32.const 0x20000) (i32.const 0) (i32.const 432))
          (call $recv (local.get $b)) (i32.eqz (i32.load (i32.const 264))) (call $from (i32.const 400))))))
      ;; 20 big: A sends 8192 bytes to B; B receives all of them.
      (if (i32.eq (local.get $row) (i32.const 20)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (return (call $pack (call $send (local.get $a) (i32.const 0x20000) (i32.const 8192) (i32.const 432))
          (call $recv (local.get $b)) (call $got (i32.const 0x20000) (i32.const 8192)) (call $from (i32.const 400))))))
      ;; 21 sizes-64k: an ipv6 socket whose send buffer is 1 MiB sends 65536 bytes and 65537 to an ipv6 peer, then an
      ;; ipv4 socket sends 65536 and 65537 to B: each past the 65535 bytes a datagram may carry. (darwin's kernel takes
      ;; the ipv6 two from such a socket, measured with Go's net, so the refusal is the host's.)
      (if (i32.eq (local.get $row) (i32.const 21)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $c (call $bound6 (i32.const 464)))
        (drop (call $sbufset (local.get $c) (i64.const 0x100000)))
        (drop (call $bound6 (i32.const 496)))
        (return (call $pack (call $send (local.get $c) (i32.const 0x20000) (i32.const 65536) (i32.const 496))
          (call $send (local.get $c) (i32.const 0x20000) (i32.const 65537) (i32.const 496))
          (call $send (local.get $a) (i32.const 0x20000) (i32.const 65536) (i32.const 432))
          (call $send (local.get $a) (i32.const 0x20000) (i32.const 65537) (i32.const 432))))))
      ;; 22 rcvbuf-small: B's receive buffer set to 2048; A sends 8192 bytes, then 4; B receives; is it the 4.
      (if (i32.eq (local.get $row) (i32.const 22)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $r0 (call $rbufset (local.get $b) (i64.const 2048)))
        (local.set $r1 (call $send (local.get $a) (i32.const 0x20000) (i32.const 8192) (i32.const 432)))
        (drop (call $send (local.get $a) (i32.const 0x30000) (i32.const 4) (i32.const 432)))
        (local.set $r2 (call $recv (local.get $b)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2) (call $got (i32.const 0x30000) (i32.const 4))))))
      ;; 23 refused: A bound; C bound and dropped, its port now closed; A connects to it, sends, and receives. A is bound
      ;; first, so the kernel cannot hand it the port C gave back, which would have A connect to itself and receive its
      ;; own datagram: the first draft's order, which a -race run of Burroughs' package caught once.
      (if (i32.eq (local.get $row) (i32.const 23)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (call $drop-low (call $bound4 (i32.const 464)))
        (return (call $pack (call $connectp (local.get $a) (i32.const 464))
          (call $send (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 0))
          (call $recv (local.get $a)) (i32.const 0)))))
      ;; 24 hop-limit: on a fresh ipv4 socket get-unicast-hop-limit; set it to 0; to 42; get it, and is it 42.
      (if (i32.eq (local.get $row) (i32.const 24)) (then
        (local.set $a (call $create (i32.const 0)))
        (local.set $r0 (call $hopget (local.get $a)))
        (local.set $r1 (call $hopset (local.get $a) (i32.const 0)))
        (local.set $r2 (call $hopset (local.get $a) (i32.const 42)))
        (local.set $r3 (call $hopget (local.get $a)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.or (local.get $r3) (i32.eq (i32.load8_u (i32.const 324)) (i32.const 42)))))))
      ;; 25 hop-limit-default: get-unicast-hop-limit's value on a fresh ipv4, a bound ipv4, a fresh ipv6 and a bound ipv6
      ;; socket.
      (if (i32.eq (local.get $row) (i32.const 25)) (then
        (drop (call $hopget (call $create (i32.const 0))))
        (local.set $r0 (i32.load8_u (i32.const 324)))
        (drop (call $hopget (call $bound4 (i32.const 400))))
        (local.set $r1 (i32.load8_u (i32.const 324)))
        (drop (call $hopget (call $create (i32.const 1))))
        (local.set $r2 (i32.load8_u (i32.const 324)))
        (drop (call $hopget (call $bound6 (i32.const 400))))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2) (i32.load8_u (i32.const 324))))))
      ;; 26 hop-limit-v6: a bound ipv6 socket's hop limit set to 255, read back, is it 255; set to 0.
      (if (i32.eq (local.get $row) (i32.const 26)) (then
        (local.set $a (call $bound6 (i32.const 400)))
        (local.set $r0 (call $hopset (local.get $a) (i32.const 255)))
        (local.set $r1 (call $hopget (local.get $a)))
        (return (call $pack (local.get $r0) (local.get $r1) (i32.eq (i32.load8_u (i32.const 324)) (i32.const 255))
          (call $hopset (local.get $a) (i32.const 0))))))
      ;; 27 rbuf: on a fresh ipv4 socket get-receive-buffer-size, nonzero; set it to 0; to 65536; read back, at least
      ;; 65536.
      (if (i32.eq (local.get $row) (i32.const 27)) (then
        (local.set $a (call $create (i32.const 0)))
        (local.set $r0 (call $rbufget (local.get $a)))
        (local.set $r0 (i32.or (local.get $r0) (i64.eqz (call $size))))
        (local.set $r1 (call $rbufset (local.get $a) (i64.const 0)))
        (local.set $r2 (call $rbufset (local.get $a) (i64.const 65536)))
        (local.set $r3 (call $rbufget (local.get $a)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.or (local.get $r3) (i64.lt_u (call $size) (i64.const 65536)))))))
      ;; 28 sbuf: row 27 for the send buffer.
      (if (i32.eq (local.get $row) (i32.const 28)) (then
        (local.set $a (call $create (i32.const 0)))
        (local.set $r0 (call $sbufget (local.get $a)))
        (local.set $r0 (i32.or (local.get $r0) (i64.eqz (call $size))))
        (local.set $r1 (call $sbufset (local.get $a) (i64.const 0)))
        (local.set $r2 (call $sbufset (local.get $a) (i64.const 65536)))
        (local.set $r3 (call $sbufget (local.get $a)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.or (local.get $r3) (i64.lt_u (call $size) (i64.const 65536)))))))
      ;; 29 buf-huge: a bound socket's receive buffer and send buffer set to 2^40, each; then each read back, nonzero.
      (if (i32.eq (local.get $row) (i32.const 29)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $r0 (call $rbufset (local.get $a) (i64.const 0x10000000000)))
        (local.set $r1 (call $sbufset (local.get $a) (i64.const 0x10000000000)))
        (local.set $r2 (call $rbufget (local.get $a)))
        (local.set $r2 (i32.or (local.get $r2) (i64.eqz (call $size))))
        (local.set $r3 (call $sbufget (local.get $a)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2) (i32.or (local.get $r3) (i64.eqz (call $size)))))))
      ;; 30 options-carry: on a fresh socket the hop limit set to 42 and the receive buffer to 65536; bind; each read
      ;; back.
      (if (i32.eq (local.get $row) (i32.const 30)) (then
        (local.set $a (call $create (i32.const 0)))
        (local.set $r0 (i32.or (call $hopset (local.get $a) (i32.const 42)) (call $rbufset (local.get $a) (i64.const 65536))))
        (call $lo4 (i32.const 600) (i32.const 0))
        (local.set $r1 (call $bindp (local.get $a) (i32.const 600)))
        (local.set $r2 (call $hopget (local.get $a)))
        (local.set $r2 (i32.or (local.get $r2) (i32.ne (i32.load8_u (i32.const 324)) (i32.const 42))))
        (local.set $r3 (call $rbufget (local.get $a)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.or (local.get $r3) (i64.lt_u (call $size) (i64.const 65536)))))))
      ;; 31 cancel: B's receive with nothing sent, its status; the subtask cancelled, the cancel's result; A sends to B;
      ;; B receives it: the cancelled receive took nothing.
      (if (i32.eq (local.get $row) (i32.const 31)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $st (call $recv-low (local.get $b) (i32.const 256)))
        (local.set $r0 (i32.and (local.get $st) (i32.const 0xf)))
        (local.set $r1 (call $cancel (i32.shr_u (local.get $st) (i32.const 4))))
        (local.set $r2 (call $send (local.get $a) (i32.const 0x20000) (i32.const 6) (i32.const 432)))
        (local.set $r3 (call $recv (local.get $b)))
        (local.set $r3 (i32.or (local.get $r3) (i32.eqz (i32.and (call $got (i32.const 0x20000) (i32.const 6)) (call $from (i32.const 400))))))
        ;; B is dropped: neither receive's lend of it may be outstanding.
        (call $drop-low (local.get $b))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2) (local.get $r3)))))
      ;; 32 blocked: B's receive with nothing sent, its status; A sends to B; B's receive, waited for, is A's datagram.
      (if (i32.eq (local.get $row) (i32.const 32)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $st (call $recv-low (local.get $b) (i32.const 256)))
        (local.set $r0 (i32.and (local.get $st) (i32.const 0xf)))
        (local.set $r1 (call $send (local.get $a) (i32.const 0x20000) (i32.const 6) (i32.const 432)))
        (call $await (local.get $st))
        (local.set $r2 (call $res (i32.const 256)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.and (call $got (i32.const 0x20000) (i32.const 6)) (call $from (i32.const 400)))))))
      ;; 33 sync-lowers: A sends to B through the blocking sync lower; B receives through it; the bytes; the sender is A.
      (if (i32.eq (local.get $row) (i32.const 33)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (return (call $pack (call $send-sync (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 432))
          (call $recv-sync (local.get $b)) (call $got (i32.const 0x20000) (i32.const 5)) (call $from (i32.const 400))))))
      ;; 34 two-datagrams: A sends two datagrams to B; B receives them in order.
      (if (i32.eq (local.get $row) (i32.const 34)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $r0 (i32.or (call $send (local.get $a) (i32.const 0x20000) (i32.const 3) (i32.const 432))
          (call $send (local.get $a) (i32.const 0x30000) (i32.const 4) (i32.const 432))))
        (local.set $r1 (call $recv (local.get $b)))
        (local.set $r2 (call $got (i32.const 0x20000) (i32.const 3)))
        (local.set $r3 (call $recv (local.get $b)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.or (local.get $r3) (i32.eqz (call $got (i32.const 0x30000) (i32.const 4))))))))
      ;; 35 dial-v4: Go's DialUDP and ListenUDP. S bound to 127.0.0.1:0; a fresh C connects to S; C's local address is
      ;; 127.0.0.1 and nonzero; C sends with no address (Write); S receives from C (ReadFrom); S sends to that sender
      ;; (WriteTo); C receives it from S (Read).
      (if (i32.eq (local.get $row) (i32.const 35)) (then
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $a (call $create (i32.const 0)))
        (local.set $r0 (i32.or (call $connectp (local.get $a) (i32.const 432)) (call $local (local.get $a) (i32.const 400))))
        (local.set $r0 (i32.or (local.get $r0) (i32.eqz (call $islo (i32.const 400)))))
        (local.set $r1 (call $send (local.get $a) (i32.const 0x20000) (i32.const 12) (i32.const 0)))
        (local.set $r2 (call $recv (local.get $b)))
        (local.set $r2 (i32.or (local.get $r2) (i32.eqz (i32.and (call $got (i32.const 0x20000) (i32.const 12)) (call $from (i32.const 400))))))
        (memory.copy (i32.const 464) (i32.const 268) (i32.const 32))
        (drop (call $send (local.get $b) (i32.const 0x30000) (i32.const 13) (i32.const 464)))
        (local.set $r3 (call $recv (local.get $a)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.or (local.get $r3) (i32.eqz (i32.and (call $got (i32.const 0x30000) (i32.const 13)) (call $from (i32.const 432)))))))))
      ;; 36 dial-v6: row 35 over [::1].
      (if (i32.eq (local.get $row) (i32.const 36)) (then
        (local.set $b (call $bound6 (i32.const 432)))
        (local.set $a (call $create (i32.const 1)))
        (local.set $r0 (i32.or (call $connectp (local.get $a) (i32.const 432)) (call $local (local.get $a) (i32.const 400))))
        (local.set $r0 (i32.or (local.get $r0) (i32.eqz (call $islo (i32.const 400)))))
        (local.set $r1 (call $send (local.get $a) (i32.const 0x20000) (i32.const 12) (i32.const 0)))
        (local.set $r2 (call $recv (local.get $b)))
        (local.set $r2 (i32.or (local.get $r2) (i32.eqz (i32.and (call $got (i32.const 0x20000) (i32.const 12)) (call $from (i32.const 400))))))
        (memory.copy (i32.const 464) (i32.const 268) (i32.const 32))
        (drop (call $send (local.get $b) (i32.const 0x30000) (i32.const 13) (i32.const 464)))
        (local.set $r3 (call $recv (local.get $a)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.or (local.get $r3) (i32.eqz (i32.and (call $got (i32.const 0x30000) (i32.const 13)) (call $from (i32.const 432)))))))))
      ;; 37 send-refusals: an ipv4 socket sends to 0.0.0.0:B's port, to 127.0.0.1:0 and to [::1]:B's port; an ipv6
      ;; socket to ::ffff:127.0.0.1:B's port.
      (if (i32.eq (local.get $row) (i32.const 37)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (call $v4 (i32.const 600) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (call $port (i32.const 432)))
        (local.set $r0 (call $send (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 600)))
        (call $lo4 (i32.const 600) (i32.const 0))
        (local.set $r1 (call $send (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 600)))
        (call $lo6 (i32.const 600) (call $port (i32.const 432)))
        (local.set $r2 (call $send (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 600)))
        (call $v6 (i32.const 600) (i32.const 0xffff) (i32.const 0x7f00) (i32.const 1) (call $port (i32.const 432)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (call $send (call $bound6 (i32.const 464)) (i32.const 0x20000) (i32.const 5) (i32.const 600))))))
      ;; 38 broadcast: an ipv4 socket bound to 0.0.0.0:0 sends to 255.255.255.255:9, which no WASI option permits.
      (if (i32.eq (local.get $row) (i32.const 38)) (then
        (local.set $a (call $create (i32.const 0)))
        (call $v4 (i32.const 600) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0))
        (local.set $r0 (call $bindp (local.get $a) (i32.const 600)))
        (call $v4 (i32.const 600) (i32.const 255) (i32.const 255) (i32.const 255) (i32.const 255) (i32.const 9))
        (return (call $pack (local.get $r0) (call $send (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 600))
          (i32.const 0) (i32.const 0)))))
      ;; 39 denied: create of an ipv4 and of an ipv6 socket (read with no network flag).
      (if (i32.eq (local.get $row) (i32.const 39)) (then
        (drop (call $create (i32.const 0)))
        (local.set $r0 (global.get $cres))
        (drop (call $create (i32.const 1)))
        (return (call $pack (local.get $r0) (global.get $cres) (i32.const 0) (i32.const 0)))))
      ;; 40 park: B's receive with nothing ever sent, waited for: a harness row, parked until its instance closes.
      (if (i32.eq (local.get $row) (i32.const 40)) (then
        (return (call $recv (call $bound4 (i32.const 432))))))
      ;; 41 max-v4: A's send buffer set to 1 MiB; A sends 65507 bytes (IPv4's most) to B, then 65508; B receives all of
      ;; the first.
      (if (i32.eq (local.get $row) (i32.const 41)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (drop (call $sbufset (local.get $a) (i64.const 0x100000)))
        (local.set $r0 (call $send (local.get $a) (i32.const 0x20000) (i32.const 65507) (i32.const 432)))
        (local.set $r1 (call $send (local.get $a) (i32.const 0x20000) (i32.const 65508) (i32.const 432)))
        (local.set $r2 (call $recv (local.get $b)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2) (call $got (i32.const 0x20000) (i32.const 65507))))))
      ;; 42 max-v6: A's send buffer set to 1 MiB; A sends 65527 bytes (IPv6's most without a jumbogram) to B; B receives
      ;; all of them.
      (if (i32.eq (local.get $row) (i32.const 42)) (then
        (local.set $a (call $bound6 (i32.const 400)))
        (local.set $b (call $bound6 (i32.const 432)))
        (drop (call $sbufset (local.get $a) (i64.const 0x100000)))
        (local.set $r0 (call $send (local.get $a) (i32.const 0x20000) (i32.const 65527) (i32.const 432)))
        (local.set $r1 (call $recv (local.get $b)))
        (return (call $pack (local.get $r0) (local.get $r1) (call $got (i32.const 0x20000) (i32.const 65527))
          (call $from (i32.const 400))))))
      ;; 43 send-oob: A sends 16 bytes from 0x7ffffff8, past the end of memory: the list's lift traps.
      (if (i32.eq (local.get $row) (i32.const 43)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (return (call $send (local.get $a) (i32.const 0x7ffffff8) (i32.const 16) (i32.const 432)))))
      ;; 44 connect-any: A bound to 0.0.0.0:0 connects to B; its local address; it is 127.0.0.1 with the bound port: the
      ;; kernel chose the address the path to B leaves from.
      (if (i32.eq (local.get $row) (i32.const 44)) (then
        (local.set $a (call $create (i32.const 0)))
        (call $v4 (i32.const 600) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0))
        (drop (call $bindp (local.get $a) (i32.const 600)))
        (drop (call $local (local.get $a) (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $r0 (call $connectp (local.get $a) (i32.const 432)))
        (local.set $r1 (call $local (local.get $a) (i32.const 464)))
        (return (call $pack (local.get $r0) (local.get $r1) (call $islo (i32.const 464))
          (i32.eq (call $port (i32.const 464)) (call $port (i32.const 400)))))))
      ;; 45 disconnect-receive: A connected to B disconnects; C sends to A; A receives it from C.
      (if (i32.eq (local.get $row) (i32.const 45)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $c (call $bound4 (i32.const 464)))
        (drop (call $connectp (local.get $a) (i32.const 432)))
        (local.set $r0 (call $disconnect (local.get $a)))
        (local.set $r1 (call $send (local.get $c) (i32.const 0x20000) (i32.const 5) (i32.const 400)))
        (local.set $r2 (call $recv (local.get $a)))
        (return (call $pack (local.get $r0) (local.get $r1) (local.get $r2)
          (i32.and (call $got (i32.const 0x20000) (i32.const 5)) (call $from (i32.const 464)))))))
      ;; 46 queued-status: A sends to B; B's receive, whose datagram has arrived, its status; B's datagram.
      (if (i32.eq (local.get $row) (i32.const 46)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $r0 (call $send (local.get $a) (i32.const 0x20000) (i32.const 5) (i32.const 432)))
        (local.set $st (call $recv-low (local.get $b) (i32.const 256)))
        (local.set $r1 (i32.and (local.get $st) (i32.const 0xf)))
        (call $await (local.get $st))
        (return (call $pack (local.get $r0) (local.get $r1) (call $res (i32.const 256)) (call $got (i32.const 0x20000) (i32.const 5))))))
      ;; 47 connect-after-arrival: C sends to B while B is unconnected; C sends to D and D receives it, so C's datagram
      ;; to B, sent first, has been through the loopback too; B connects to A; A sends to B; B receives: is it from A,
      ;; is it from C.
      (if (i32.eq (local.get $row) (i32.const 47)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (local.set $c (call $bound4 (i32.const 464)))
        (local.set $r0 (call $send (local.get $c) (i32.const 0x30000) (i32.const 9) (i32.const 432)))
        (local.set $st (call $bound4 (i32.const 496)))
        (drop (call $send (local.get $c) (i32.const 0x30000) (i32.const 2) (i32.const 496)))
        (local.set $r0 (i32.or (local.get $r0) (call $recv (local.get $st))))
        (drop (call $connectp (local.get $b) (i32.const 400)))
        (drop (call $send (local.get $a) (i32.const 0x20000) (i32.const 11) (i32.const 432)))
        (local.set $r1 (call $recv (local.get $b)))
        (return (call $pack (local.get $r0) (local.get $r1) (call $from (i32.const 400)) (call $from (i32.const 464))))))
      ;; 48 kernel-maxdgram: A, its send buffer the kernel's default, sends 9216 bytes to B, then 9217. darwin's default
      ;; is net.inet.udp.maxdgram, 9216, so its kernel refuses the second; a kernel with a larger default takes both.
      (if (i32.eq (local.get $row) (i32.const 48)) (then
        (local.set $a (call $bound4 (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (return (call $pack (call $send (local.get $a) (i32.const 0x20000) (i32.const 9216) (i32.const 432))
          (call $send (local.get $a) (i32.const 0x20000) (i32.const 9217) (i32.const 432)) (i32.const 0) (i32.const 0)))))
      ;; 49 disconnect-any: A bound to 0.0.0.0:0, connected to B, disconnects; its local address; it is 0.0.0.0; its port
      ;; is the bound one. darwin keeps the port; a kernel that unbinds a port nobody chose resets it.
      (if (i32.eq (local.get $row) (i32.const 49)) (then
        (local.set $a (call $create (i32.const 0)))
        (call $v4 (i32.const 600) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0))
        (drop (call $bindp (local.get $a) (i32.const 600)))
        (drop (call $local (local.get $a) (i32.const 400)))
        (local.set $b (call $bound4 (i32.const 432)))
        (drop (call $connectp (local.get $a) (i32.const 432)))
        (local.set $r0 (call $disconnect (local.get $a)))
        (local.set $r1 (call $local (local.get $a) (i32.const 464)))
        (return (call $pack (local.get $r0) (local.get $r1) (call $isany (i32.const 464))
          (i32.eq (call $port (i32.const 464)) (call $port (i32.const 400)))))))
      (unreachable)))
  (core instance $gi (instantiate $gm (with "" (instance (export "m" (memory $m))
    (export "create" (func $create-low)) (export "bind" (func $bind-low)) (export "connect" (func $connect-low))
    (export "disconnect" (func $disconnect-low)) (export "send" (func $send-low)) (export "send-sync" (func $send-sync-low))
    (export "recv" (func $recv-low)) (export "recv-sync" (func $recv-sync-low)) (export "local" (func $local-low))
    (export "remote" (func $remote-low)) (export "family" (func $family-low)) (export "hopget" (func $hopget-low))
    (export "hopset" (func $hopset-low)) (export "rbufget" (func $rbufget-low)) (export "rbufset" (func $rbufset-low))
    (export "sbufget" (func $sbufget-low)) (export "sbufset" (func $sbufset-low)) (export "drop" (func $drop-low))
    (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "wswait" (func $wswait))
    (export "stcancel" (func $stcancel)) (export "stdrop" (func $stdrop))))))
  (type $rt (func async (param "row" u32) (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $gi "run"))))
