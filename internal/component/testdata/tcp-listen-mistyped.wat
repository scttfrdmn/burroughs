;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; tcp-listen-mistyped (#1277 E7b-2, ADR 0125): a guest whose `tcp-socket.listen` returns
;; result<stream<u32>, error-code>, where the host's declaration is result<stream<own<tcp-socket>>, error-code>. The
;; host mints each element as an own of its tcp-socket, so a guest reading the elements as u32 would read handles
;; as numbers; it is refused at instantiate (checkListenSignature). tcp-bind-listen.wasm is the control, declared as
;; the host declares it.
;;
;; Assembled with `wasm-tools parse tcp-listen-mistyped.wat -o tcp-listen-mistyped.wasm` (wasm-tools 1.258.0).
(component
  (type $sock-t (instance
    (export "tcp-socket" (type (sub resource)))
    (type $ec (variant (case "access-denied") (case "not-supported") (case "invalid-argument")
      (case "out-of-memory") (case "timeout") (case "invalid-state") (case "address-not-bindable")
      (case "address-in-use") (case "remote-unreachable") (case "connection-refused") (case "connection-broken")
      (case "connection-reset") (case "connection-aborted") (case "datagram-too-large") (case "other" (option string))))
    (export "error-code" (type $ec2 (eq $ec)))
    (type $st (stream u32))
    (type $lr (result $st (error $ec2)))
    (export "[method]tcp-socket.listen" (func (param "self" (borrow 0)) (result $lr)))))
  (import "wasi:sockets/types@0.3.0" (instance $s (type $sock-t)))
  (alias export $s "[method]tcp-socket.listen" (func $listen))
  (core module $mm (memory (export "m") 1)
    (func (export "realloc") (param i32 i32 i32 i32) (result i32) (i32.const 1024)))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (alias core export $mi "realloc" (core func $realloc))
  (core func $listen-low (canon lower (func $listen) (memory $m) (realloc $realloc)))
  (core module $gm
    (import "" "listen" (func $listen (param i32 i32)))
    (func (export "run") (call $listen (i32.const 0) (i32.const 0))))
  (core instance $gi (instantiate $gm (with "" (instance (export "listen" (func $listen-low))))))
  (func (export "run") (canon lift (core func $gi "run"))))
