;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; tcp-connect-park (ADR 0123): a wasi:sockets connect, waited for by a callback-lifted task, so the wait is the
;; lift's park and its bound applies. `run(port)` creates an ipv4 tcp-socket, connects it to 127.0.0.1:port with
;; the async lower, whose 13 flat parameters spill to the tuple at 64, joins the subtask to a set and WAITs. The
;; callback returns task.return(code << 16 | state << 8 | the result's discriminant), so a connect that succeeds
;; reads SUBTASK (1) << 16 | RETURNED (2) << 8 | ok (0) = 66048.
;; Assembled with `wasm-tools parse tcp-connect-park.wat -o tcp-connect-park.wasm` (wasm-tools 1.258.0).
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
    (export "[method]tcp-socket.connect" (func async (param "self" (borrow 0)) (param "remote-address" $addr2) (result $r)))))
  (import "wasi:sockets/types@0.3.0" (instance $s (type $sock-t)))
  (alias export $s "[static]tcp-socket.create" (func $create))
  (alias export $s "[method]tcp-socket.connect" (func $connect))
  ;; The realloc the option requires, for error-code's `other` string. Nothing here is ever given one.
  (core module $mm (memory (export "m") 1)
    (func (export "realloc") (param i32 i32 i32 i32) (result i32) (i32.const 1024)))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (alias core export $mi "realloc" (core func $realloc))
  (core func $create-low (canon lower (func $create) (memory $m) (realloc $realloc)))
  (core func $connect-low (canon lower (func $connect) async (memory $m) (realloc $realloc)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $ret (canon task.return (result u32)))
  (core module $gm
    (import "" "m" (memory 1))
    (import "" "create" (func $create (param i32 i32)))
    (import "" "connect" (func $connect (param i32 i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "run") (param $port i32) (result i32) (local $set i32)
      (call $create (i32.const 0) (i32.const 0)) ;; result<own, error-code> at 0: the handle at 4
      ;; tuple<borrow, ip-socket-address> at 64: the borrow at 64, the variant at 68 (ipv4), its record at 72
      (i32.store (i32.const 64) (i32.load (i32.const 4)))
      (i32.store8 (i32.const 68) (i32.const 0))
      (i32.store16 (i32.const 72) (local.get $port))
      (i32.store8 (i32.const 74) (i32.const 127))
      (i32.store8 (i32.const 75) (i32.const 0))
      (i32.store8 (i32.const 76) (i32.const 0))
      (i32.store8 (i32.const 77) (i32.const 1))
      (local.set $set (call $wsnew))
      (call $wsjoin (i32.shr_u (call $connect (i32.const 64) (i32.const 128)) (i32.const 4)) (local.get $set))
      (i32.or (i32.const 2) (i32.shl (local.get $set) (i32.const 4)))) ;; WAIT on the set
    (func (export "cb") (param $code i32) (param i32) (param $state i32) (result i32)
      (call $ret (i32.or (i32.or (i32.shl (local.get $code) (i32.const 16)) (i32.shl (local.get $state) (i32.const 8)))
        (i32.load8_u (i32.const 128))))
      (i32.const 0))) ;; EXIT
  (core instance $gi (instantiate $gm (with "" (instance (export "m" (memory $m)) (export "create" (func $create-low))
    (export "connect" (func $connect-low)) (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
    (export "ret" (func $ret))))))
  (type $at (func async (param "port" u32) (result u32)))
  (func (export "run") (type $at) (canon lift (core func $gi "run") async (callback (func $gi "cb")))))
