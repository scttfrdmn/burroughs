;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; tcp-bind-listen (#1277 E7b-2, ADR 0125): wasi:sockets' bind, listen and set-listen-backlog-size at the ABI, below
;; std, each row one sequence on fresh ipv4 sockets. `run(row, port)` returns four result bytes packed into a u32,
;; byte 0 the first: an operation's result is 0 for ok and 0x80 | its error-code case for an error, and a property is
;; 0 or 1. `port` is a listening peer's, for the rows that connect. A connect is the async lower with its 13 flat
;; parameters spilled to the tuple at 64, waited for in waitable-set.wait when it does not resolve inline; `run` is
;; async-typed and lifted with no async option, as Rust std's `run` is, so it may block.
;;
;; Every result is lowered at a return pointer: result<_, error-code> and result<stream, error-code> carry their
;; error case at +4, and result<ip-socket-address, error-code> its address at +4, whose ipv4 port is at +8 and
;; octets at +10. A result<stream, error-code>'s stream handle is at +4 too.
;;
;; The accept rows read listen's stream<own<tcp-socket>> with one async stream.read of one element into 512, waited
;; for when it blocks, so the accepted handle is the i32 at 512.
;;
;; Assembled with `wasm-tools parse tcp-bind-listen.wat -o tcp-bind-listen.wasm` (wasm-tools 1.258.0).
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
    (export "[method]tcp-socket.bind" (func (param "self" (borrow 0)) (param "local-address" $addr2) (result $r)))
    (export "[method]tcp-socket.connect" (func async (param "self" (borrow 0)) (param "remote-address" $addr2) (result $r)))
    (type $st (stream (own 0)))
    (type $lr (result $st (error $ec2)))
    (export "[method]tcp-socket.listen" (func (param "self" (borrow 0)) (result $lr)))
    (export "[method]tcp-socket.set-listen-backlog-size" (func (param "self" (borrow 0)) (param "value" u64) (result $r)))
    (type $ar (result $addr2 (error $ec2)))
    (export "[method]tcp-socket.get-local-address" (func (param "self" (borrow 0)) (result $ar)))
    (export "[method]tcp-socket.get-remote-address" (func (param "self" (borrow 0)) (result $ar)))
    (export "[method]tcp-socket.get-is-listening" (func (param "self" (borrow 0)) (result bool)))))
  (import "wasi:sockets/types@0.3.0" (instance $s (type $sock-t)))
  (alias export $s "[static]tcp-socket.create" (func $create))
  (alias export $s "[method]tcp-socket.bind" (func $bind))
  (alias export $s "[method]tcp-socket.connect" (func $connect))
  (alias export $s "[method]tcp-socket.listen" (func $listen))
  (alias export $s "[method]tcp-socket.set-listen-backlog-size" (func $backlog))
  (alias export $s "[method]tcp-socket.get-local-address" (func $local))
  (alias export $s "[method]tcp-socket.get-remote-address" (func $remote))
  (alias export $s "[method]tcp-socket.get-is-listening" (func $islistening))
  (alias export $s "tcp-socket" (type $tcp))
  (type $lst (stream (own $tcp)))
  ;; The realloc the option requires, for error-code's `other` string. Nothing here is ever given one.
  (core module $mm (memory (export "m") 1)
    (func (export "realloc") (param i32 i32 i32 i32) (result i32) (i32.const 1024)))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (alias core export $mi "realloc" (core func $realloc))
  (core func $create-low (canon lower (func $create) (memory $m) (realloc $realloc)))
  (core func $bind-low (canon lower (func $bind) (memory $m) (realloc $realloc)))
  (core func $connect-low (canon lower (func $connect) async (memory $m) (realloc $realloc)))
  (core func $listen-low (canon lower (func $listen) (memory $m) (realloc $realloc)))
  (core func $backlog-low (canon lower (func $backlog) (memory $m) (realloc $realloc)))
  (core func $local-low (canon lower (func $local) (memory $m) (realloc $realloc)))
  (core func $remote-low (canon lower (func $remote) (memory $m) (realloc $realloc)))
  (core func $islistening-low (canon lower (func $islistening)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $m)))
  (core func $sread (canon stream.read $lst async (memory $m)))
  (core module $gm
    (import "" "m" (memory 1))
    (import "" "create" (func $create (param i32 i32)))
    (import "" "bind" (func $bind (param i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32)))
    (import "" "connect" (func $connect (param i32 i32) (result i32)))
    (import "" "listen" (func $listen (param i32 i32)))
    (import "" "backlog" (func $backlog (param i32 i64 i32)))
    (import "" "local" (func $local (param i32 i32)))
    (import "" "remote" (func $remote (param i32 i32)))
    (import "" "islistening" (func $islistening (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (import "" "sread" (func $sread (param i32 i32 i32) (result i32)))
    ;; accept: one stream.read of listen's stream into 512, waited for when it blocks; the read's packed result.
    (func $accept (param $ls i32) (result i32) (local $r i32) (local $set i32)
      (local.set $r (call $sread (local.get $ls) (i32.const 512) (i32.const 1)))
      (if (i32.eq (local.get $r) (i32.const -1))
        (then
          (local.set $set (call $wsnew))
          (call $wsjoin (local.get $ls) (local.get $set))
          (drop (call $wswait (local.get $set) (i32.const 192)))
          (local.set $r (i32.load (i32.const 196)))))
      (local.get $r))
    ;; res: an operation's result at ret, 0 for ok and 0x80 | the error-code case for an error.
    (func $res (param $ret i32) (result i32)
      (if (result i32) (i32.eqz (i32.load8_u (local.get $ret)))
        (then (i32.const 0))
        (else (i32.or (i32.const 0x80) (i32.load8_u (i32.add (local.get $ret) (i32.const 4)))))))
    ;; sock: a fresh ipv4 tcp-socket's handle. create's result is at 0, its handle at 4.
    (func $sock (result i32)
      (call $create (i32.const 0) (i32.const 0))
      (i32.load (i32.const 4)))
    ;; bind4: bind to a.b.c.d:port, the result at 16.
    (func $bind4 (param $s i32) (param $a i32) (param $b i32) (param $c i32) (param $d i32) (param $port i32) (result i32)
      (call $bind (local.get $s) (i32.const 0) (local.get $port) (local.get $a) (local.get $b) (local.get $c) (local.get $d)
        (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 16))
      (call $res (i32.const 16)))
    ;; connect4: connect to 127.0.0.1:port and wait, the result at 128.
    (func $connect4 (param $s i32) (param $port i32) (result i32) (local $st i32) (local $set i32)
      (i32.store (i32.const 64) (local.get $s))
      (i32.store8 (i32.const 68) (i32.const 0))
      (i32.store16 (i32.const 72) (local.get $port))
      (i32.store8 (i32.const 74) (i32.const 127))
      (i32.store8 (i32.const 75) (i32.const 0))
      (i32.store8 (i32.const 76) (i32.const 0))
      (i32.store8 (i32.const 77) (i32.const 1))
      (local.set $st (call $connect (i32.const 64) (i32.const 128)))
      ;; RETURNED (2) inline needs no wait; otherwise the subtask's index is st >> 4.
      (if (i32.ne (i32.and (local.get $st) (i32.const 0xf)) (i32.const 2))
        (then
          (local.set $set (call $wsnew))
          (call $wsjoin (i32.shr_u (local.get $st) (i32.const 4)) (local.get $set))
          (drop (call $wswait (local.get $set) (i32.const 192)))))
      (call $res (i32.const 128)))
    (func $listen1 (param $s i32) (result i32)
      (call $listen (local.get $s) (i32.const 32))
      (call $res (i32.const 32)))
    (func $backlog1 (param $s i32) (param $v i64) (result i32)
      (call $backlog (local.get $s) (local.get $v) (i32.const 48))
      (call $res (i32.const 48)))
    ;; local1: get-local-address's result at 256.
    (func $local1 (param $s i32) (result i32)
      (call $local (local.get $s) (i32.const 256))
      (call $res (i32.const 256)))
    (func $pack (param $b0 i32) (param $b1 i32) (param $b2 i32) (param $b3 i32) (result i32)
      (i32.or (i32.or (local.get $b0) (i32.shl (local.get $b1) (i32.const 8)))
        (i32.or (i32.shl (local.get $b2) (i32.const 16)) (i32.shl (local.get $b3) (i32.const 24)))))
    (func (export "run") (param $row i32) (param $port i32) (result i32) (local $s i32) (local $t i32) (local $p i32)
      (local.set $s (call $sock))
      ;; 0 local-between: bind 127.0.0.1:0, get-local-address, its port nonzero, its address 127.0.0.1.
      (if (i32.eq (local.get $row) (i32.const 0)) (then
        (return (call $pack (call $bind4 (local.get $s) (i32.const 127) (i32.const 0) (i32.const 0) (i32.const 1) (i32.const 0))
          (call $local1 (local.get $s))
          (i32.ne (i32.load16_u (i32.const 264)) (i32.const 0))
          (i32.and (i32.eqz (i32.load8_u (i32.const 260))) (i32.eq (i32.load (i32.const 266)) (i32.const 0x0100007f)))))))
      ;; 1 backlog-zero: bind, set-listen-backlog-size(0), then (1), then get-is-listening.
      (if (i32.eq (local.get $row) (i32.const 1)) (then
        (return (call $pack (call $bind4 (local.get $s) (i32.const 127) (i32.const 0) (i32.const 0) (i32.const 1) (i32.const 0))
          (call $backlog1 (local.get $s) (i64.const 0))
          (call $backlog1 (local.get $s) (i64.const 1))
          (call $islistening (local.get $s))))))
      ;; 2 listen-twice: bind, listen, listen again, get-is-listening.
      (if (i32.eq (local.get $row) (i32.const 2)) (then
        (return (call $pack (call $bind4 (local.get $s) (i32.const 127) (i32.const 0) (i32.const 0) (i32.const 1) (i32.const 0))
          (call $listen1 (local.get $s))
          (call $listen1 (local.get $s))
          (call $islistening (local.get $s))))))
      ;; 3 listen-connected: connect to the peer, listen, set-listen-backlog-size(1), get-is-listening.
      (if (i32.eq (local.get $row) (i32.const 3)) (then
        (return (call $pack (call $connect4 (local.get $s) (local.get $port))
          (call $listen1 (local.get $s))
          (call $backlog1 (local.get $s) (i64.const 1))
          (call $islistening (local.get $s))))))
      ;; 4 listen-unbound: listen with no bind (the implicit bind), get-local-address, its port nonzero,
      ;; get-is-listening.
      (if (i32.eq (local.get $row) (i32.const 4)) (then
        (return (call $pack (call $listen1 (local.get $s))
          (call $local1 (local.get $s))
          (i32.ne (i32.load16_u (i32.const 264)) (i32.const 0))
          (call $islistening (local.get $s))))))
      ;; 5 bind-refusals: bind, bind again; a fresh socket bound to 224.0.0.1 (multicast); get-local-address on an
      ;; unbound socket.
      (if (i32.eq (local.get $row) (i32.const 5)) (then
        (local.set $t (call $sock))
        (return (call $pack (call $bind4 (local.get $s) (i32.const 127) (i32.const 0) (i32.const 0) (i32.const 1) (i32.const 0))
          (call $bind4 (local.get $s) (i32.const 127) (i32.const 0) (i32.const 0) (i32.const 1) (i32.const 0))
          (call $bind4 (local.get $t) (i32.const 224) (i32.const 0) (i32.const 0) (i32.const 1) (i32.const 0))
          (call $local1 (call $sock))))))
      ;; 6 connect-bound: bind 127.0.0.1:0, connect to the peer, get-local-address, its port the bound one.
      (if (i32.eq (local.get $row) (i32.const 6)) (then
        (local.set $t (call $bind4 (local.get $s) (i32.const 127) (i32.const 0) (i32.const 0) (i32.const 1) (i32.const 0)))
        (drop (call $local1 (local.get $s)))
        (local.set $p (i32.load16_u (i32.const 264)))
        (return (call $pack (local.get $t)
          (call $connect4 (local.get $s) (local.get $port))
          (call $local1 (local.get $s))
          (i32.eq (i32.load16_u (i32.const 264)) (local.get $p))))))
      ;; 7 listening-getters: bind, listen, get-remote-address, set-listen-backlog-size(0) while listening.
      (if (i32.eq (local.get $row) (i32.const 7)) (then
        (local.set $t (call $bind4 (local.get $s) (i32.const 127) (i32.const 0) (i32.const 0) (i32.const 1) (i32.const 0)))
        (return (call $pack (local.get $t)
          (call $listen1 (local.get $s))
          (block (result i32) (call $remote (local.get $s) (i32.const 256)) (call $res (i32.const 256)))
          (call $backlog1 (local.get $s) (i64.const 0))))))
      ;; 8 backlog-connected: connect to the peer, set-listen-backlog-size(0): which refusal a connected socket's 0 gets.
      (if (i32.eq (local.get $row) (i32.const 8)) (then
        (return (call $pack (call $connect4 (local.get $s) (local.get $port))
          (call $backlog1 (local.get $s) (i64.const 0))
          (i32.const 0) (i32.const 0)))))
      ;; 9 connect-listening: bind, listen, connect to the peer, get-is-listening.
      (if (i32.eq (local.get $row) (i32.const 9)) (then
        (return (call $pack (call $bind4 (local.get $s) (i32.const 127) (i32.const 0) (i32.const 0) (i32.const 1) (i32.const 0))
          (call $listen1 (local.get $s))
          (call $connect4 (local.get $s) (local.get $port))
          (call $islistening (local.get $s))))))
      ;; 10 connect-before-listen: bind 127.0.0.1:0, get-local-address, and a second socket's connect to that port
      ;; before any listen; then listen. The peer port is not used.
      (if (i32.eq (local.get $row) (i32.const 10)) (then
        (local.set $t (call $bind4 (local.get $s) (i32.const 127) (i32.const 0) (i32.const 0) (i32.const 1) (i32.const 0)))
        (local.set $p (call $local1 (local.get $s)))
        (return (call $pack (local.get $t) (local.get $p)
          (call $connect4 (call $sock) (i32.load16_u (i32.const 264)))
          (call $listen1 (local.get $s))))))
      ;; 11 accept-one: bind, listen, a second socket's connect to the listener, one stream.read of the listen stream;
      ;; then on the accepted handle get-is-listening and get-remote-address.
      (if (i32.eq (local.get $row) (i32.const 11)) (then
        (drop (call $bind4 (local.get $s) (i32.const 127) (i32.const 0) (i32.const 0) (i32.const 1) (i32.const 0)))
        (drop (call $listen1 (local.get $s)))
        (drop (call $local1 (local.get $s)))
        (local.set $t (call $connect4 (call $sock) (i32.load16_u (i32.const 264))))
        (local.set $p (call $accept (i32.load (i32.const 36))))
        (return (call $pack (local.get $t) (local.get $p)
          (call $islistening (i32.load (i32.const 512)))
          (block (result i32) (call $remote (i32.load (i32.const 512)) (i32.const 256)) (call $res (i32.const 256)))))))
      ;; 12 accept-park: bind 127.0.0.1:0, listen, and one stream.read of the listen stream with nobody connecting, so
      ;; the task parks in waitable-set.wait until its instance closes. A harness row: no reading is taken of it.
      (if (i32.eq (local.get $row) (i32.const 12)) (then
        (drop (call $bind4 (local.get $s) (i32.const 127) (i32.const 0) (i32.const 0) (i32.const 1) (i32.const 0)))
        (drop (call $listen1 (local.get $s)))
        (return (call $accept (i32.load (i32.const 36))))))
      (unreachable)))
  (core instance $gi (instantiate $gm (with "" (instance (export "m" (memory $m)) (export "create" (func $create-low))
    (export "bind" (func $bind-low)) (export "connect" (func $connect-low)) (export "listen" (func $listen-low))
    (export "backlog" (func $backlog-low)) (export "local" (func $local-low)) (export "remote" (func $remote-low))
    (export "islistening" (func $islistening-low)) (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
    (export "wswait" (func $wswait)) (export "sread" (func $sread))))))
  (type $rt (func async (param "row" u32) (param "port" u32) (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $gi "run"))))
