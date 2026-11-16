;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; tcp-send-tail (grave #1308, ADR 0127): a guest that writes to a tcp-socket's send stream and exits at once.
;; `run(port, size, body, tail)` creates an ipv4 tcp-socket, connects it to 127.0.0.1:port with the async lower
;; (tcp-connect-park's spilled tuple at 64), calls send with a fresh stream<u8>, and writes `body` bytes to it in
;; pieces of at most `size`, then two pieces of `tail` bytes each. Every write is an async stream.write. One that
;; completes inline counts its progress, and the next is issued at once; one that blocks is waited for on the set
;; the writable end is joined to, and its event's progress counts. A piece accepted in part is finished by the
;; next write. Once all `body + 2*tail` are accepted the guest drops the writable end, task.returns the count, and
;; EXITs, so the last write it is told is complete is followed only by the drop and the exit.
;;
;; Each piece is bytes of memory from 1024, byte i being i mod 251, so what a peer reads is a count, not a pattern.
;; The result is the count accepted, or 0x80000000 | the count when a write reports DROPPED, or 0xc0000000 | the
;; connect result's discriminant (1) when the connect fails.
;; Assembled with `wasm-tools parse tcp-send-tail.wat -o tcp-send-tail.wasm` (wasm-tools 1.258.0).
(component
  (type $sock-t (instance
    (export "tcp-socket" (type (sub resource)))
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
    (export "[static]tcp-socket.create" (func (param "address-family" $fam2) (result $cr)))
    (type $r (result (error $ec2)))
    (export "[method]tcp-socket.connect" (func async (param "self" (borrow 0)) (param "remote-address" $addr2) (result $r)))
    (type $s (stream u8))
    (type $f (future $r))
    (export "[method]tcp-socket.send" (func (param "self" (borrow 0)) (param "data" $s) (result $f)))))
  (import "wasi:sockets/types@0.3.0" (instance $s (type $sock-t)))
  (alias export $s "[static]tcp-socket.create" (func $create))
  (alias export $s "[method]tcp-socket.connect" (func $connect))
  (alias export $s "[method]tcp-socket.send" (func $send))
  ;; Two pages: a piece of up to 64 KiB from 1024. The realloc the option requires, for error-code's `other` string.
  ;; Nothing here is ever given one.
  (core module $mm (memory (export "m") 2)
    (func (export "realloc") (param i32 i32 i32 i32) (result i32) (i32.const 512)))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (alias core export $mi "realloc" (core func $realloc))
  (type $st (stream u8))
  (core func $create-low (canon lower (func $create) (memory $m) (realloc $realloc)))
  (core func $connect-low (canon lower (func $connect) async (memory $m) (realloc $realloc)))
  (core func $send-low (canon lower (func $send)))
  (core func $snew (canon stream.new $st))
  (core func $swrite (canon stream.write $st async (memory $m)))
  (core func $sdropw (canon stream.drop-writable $st))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $ret (canon task.return (result u32)))
  (core module $gm
    (import "" "m" (memory 2))
    (import "" "create" (func $create (param i32 i32)))
    (import "" "connect" (func $connect (param i32 i32) (result i32)))
    (import "" "send" (func $send (param i32 i32) (result i32)))
    (import "" "snew" (func $snew (result i64)))
    (import "" "swrite" (func $swrite (param i32 i32 i32) (result i32)))
    (import "" "sdropw" (func $sdropw (param i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "ret" (func $ret (param i32)))
    (global $sock (mut i32) (i32.const 0))
    (global $ws (mut i32) (i32.const 0))
    (global $set (mut i32) (i32.const 0))
    (global $size (mut i32) (i32.const 0))
    (global $body (mut i32) (i32.const 0))
    (global $tail (mut i32) (i32.const 0))
    (global $sent (mut i32) (i32.const 0))
    ;; finish task.returns v and EXITs.
    (func $finish (param $v i32) (result i32)
      (call $ret (local.get $v))
      (i32.const 0)) ;; EXIT
    ;; left is what remains of the current piece: of the body's, at most `size`; of a tail's, the rest of it.
    (func $left (result i32) (local $n i32) (local $t i32)
      (if (i32.lt_u (global.get $sent) (global.get $body))
        (then
          (local.set $n (i32.sub (global.get $body) (global.get $sent)))
          (if (i32.gt_u (local.get $n) (global.get $size)) (then (local.set $n (global.get $size))))
          (return (local.get $n))))
      (local.set $t (i32.sub (global.get $sent) (global.get $body)))
      (if (i32.lt_u (local.get $t) (global.get $tail))
        (then (return (i32.sub (global.get $tail) (local.get $t)))))
      (i32.sub (i32.shl (global.get $tail) (i32.const 1)) (local.get $t)))
    ;; pump issues writes until one blocks (WAIT on the set) or all are accepted (drop, task.return, EXIT).
    (func $pump (result i32) (local $r i32)
      (loop $more
        (if (i32.ge_u (global.get $sent) (i32.add (global.get $body) (i32.shl (global.get $tail) (i32.const 1))))
          (then
            (call $sdropw (global.get $ws))
            (return (call $finish (global.get $sent)))))
        (local.set $r (call $swrite (global.get $ws) (i32.const 1024) (call $left)))
        (if (i32.eq (local.get $r) (i32.const -1)) ;; BLOCKED
          (then (return (i32.or (i32.const 2) (i32.shl (global.get $set) (i32.const 4)))))) ;; WAIT on the set
        (if (i32.eq (i32.and (local.get $r) (i32.const 0xf)) (i32.const 1)) ;; DROPPED
          (then (return (call $finish (i32.or (i32.const 0x80000000) (global.get $sent))))))
        (global.set $sent (i32.add (global.get $sent) (i32.shr_u (local.get $r) (i32.const 4))))
        (br $more))
      (unreachable))
    ;; start runs once the connect has resolved: a failed connect is reported, a connected socket is sent to.
    (func $start (result i32) (local $p i64) (local $i i32) (local $max i32)
      (if (i32.load8_u (i32.const 128))
        (then (return (call $finish (i32.or (i32.const 0xc0000000) (i32.load8_u (i32.const 128)))))))
      (local.set $p (call $snew))
      (global.set $ws (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
      (drop (call $send (global.get $sock) (i32.wrap_i64 (local.get $p))))
      (call $wsjoin (global.get $ws) (global.get $set))
      (local.set $max (global.get $size))
      (if (i32.gt_u (global.get $tail) (local.get $max)) (then (local.set $max (global.get $tail))))
      (block $filled
        (loop $fill
          (br_if $filled (i32.ge_u (local.get $i) (local.get $max)))
          (i32.store8 (i32.add (i32.const 1024) (local.get $i)) (i32.rem_u (local.get $i) (i32.const 251)))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $fill)))
      (call $pump))
    (func (export "run") (param $port i32) (param $size i32) (param $body i32) (param $tail i32) (result i32)
      (local $st i32)
      (global.set $size (local.get $size))
      (global.set $body (local.get $body))
      (global.set $tail (local.get $tail))
      (global.set $set (call $wsnew))
      (call $create (i32.const 0) (i32.const 0)) ;; result<own, error-code> at 0: the handle at 4
      (global.set $sock (i32.load (i32.const 4)))
      ;; tuple<borrow, ip-socket-address> at 64: the borrow at 64, the variant at 68 (ipv4), its record at 72
      (i32.store (i32.const 64) (global.get $sock))
      (i32.store8 (i32.const 68) (i32.const 0))
      (i32.store16 (i32.const 72) (local.get $port))
      (i32.store8 (i32.const 74) (i32.const 127))
      (i32.store8 (i32.const 75) (i32.const 0))
      (i32.store8 (i32.const 76) (i32.const 0))
      (i32.store8 (i32.const 77) (i32.const 1))
      (local.set $st (call $connect (i32.const 64) (i32.const 128)))
      ;; A connect that resolved before the lower returned comes back RETURNED (2) with no subtask to wait on.
      (if (i32.eq (i32.and (local.get $st) (i32.const 0xf)) (i32.const 2))
        (then (return (call $start))))
      (call $wsjoin (i32.shr_u (local.get $st) (i32.const 4)) (global.get $set))
      (i32.or (i32.const 2) (i32.shl (global.get $set) (i32.const 4)))) ;; WAIT on the set
    ;; cb: a SUBTASK (1) event is the connect's resolution; a STREAM_WRITE (3) event is a blocked write's completion.
    (func (export "cb") (param $code i32) (param $index i32) (param $payload i32) (result i32)
      (if (i32.eq (local.get $code) (i32.const 1))
        (then
          (if (i32.eq (local.get $payload) (i32.const 2)) ;; RETURNED
            (then
              (call $wsjoin (local.get $index) (i32.const 0)) ;; the subtask leaves the set
              (return (call $start))))
          (return (i32.or (i32.const 2) (i32.shl (global.get $set) (i32.const 4))))))
      (if (i32.eq (local.get $code) (i32.const 3))
        (then
          (if (i32.eq (i32.and (local.get $payload) (i32.const 0xf)) (i32.const 1)) ;; DROPPED
            (then (return (call $finish (i32.or (i32.const 0x80000000) (global.get $sent))))))
          (global.set $sent (i32.add (global.get $sent) (i32.shr_u (local.get $payload) (i32.const 4))))
          (return (call $pump))))
      (unreachable)))
  (core instance $gi (instantiate $gm (with "" (instance (export "m" (memory $m)) (export "create" (func $create-low))
    (export "connect" (func $connect-low)) (export "send" (func $send-low)) (export "snew" (func $snew))
    (export "swrite" (func $swrite)) (export "sdropw" (func $sdropw)) (export "wsnew" (func $wsnew))
    (export "wsjoin" (func $wsjoin)) (export "ret" (func $ret))))))
  (type $at (func async (param "port" u32) (param "size" u32) (param "body" u32) (param "tail" u32) (result u32)))
  (func (export "run") (type $at) (canon lift (core func $gi "run") async (callback (func $gi "cb")))))
