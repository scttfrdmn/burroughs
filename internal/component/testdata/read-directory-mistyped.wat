;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; read-directory-mistyped (#1374 E1b, ADR 0134): an import of wasi:filesystem/types@0.3.0's read-directory whose
;; directory-entry declares its two fields in the other order, `name` then `type`. The host lowers each entry at its
;; own layout, the type's case at 0 and the name at 16, and the guest would read it at this one's, so the guest's
;; declaration must be the host's, and this is refused at instantiate. fs-readdir.wasm, which declares the host's, is
;; the control that instantiates. wasmtime 49.0.3 refuses it too, at its linker's type check.
;; Assembled with `wasm-tools parse read-directory-mistyped.wat -o read-directory-mistyped.wasm` (wasm-tools 1.258.0).
(component
  (type $types-t (instance
    (export "descriptor" (type (sub resource)))
    (type $ec (variant (case "access") (case "already") (case "bad-descriptor") (case "busy") (case "deadlock")
      (case "quota") (case "exist") (case "file-too-large") (case "illegal-byte-sequence") (case "in-progress")
      (case "interrupted") (case "invalid") (case "io") (case "is-directory") (case "loop") (case "too-many-links")
      (case "message-size") (case "name-too-long") (case "no-device") (case "no-entry") (case "no-lock")
      (case "insufficient-memory") (case "insufficient-space") (case "not-directory") (case "not-empty")
      (case "not-recoverable") (case "unsupported") (case "no-tty") (case "no-such-device") (case "overflow")
      (case "not-permitted") (case "pipe") (case "read-only") (case "invalid-seek") (case "text-file-busy")
      (case "cross-device") (case "other" (option string))))
    (export "error-code" (type $ec2 (eq $ec)))
    (type $dt (variant (case "block-device") (case "character-device") (case "directory") (case "fifo")
      (case "symbolic-link") (case "regular-file") (case "socket") (case "other" (option string))))
    (export "descriptor-type" (type $dt2 (eq $dt)))
    (type $de (record (field "name" string) (field "type" $dt2)))
    (export "directory-entry" (type $de2 (eq $de)))
    (type $self (borrow 0))
    (type $entries (stream $de2))
    (type $r (result (error $ec2)))
    (type $fut (future $r))
    (type $rd (tuple $entries $fut))
    (export "[method]descriptor.read-directory" (func (param "self" $self) (result $rd)))))
  (import "wasi:filesystem/types@0.3.0" (instance $types (type $types-t)))
  (alias export $types "[method]descriptor.read-directory" (func $readdir))
  (core module $mm (memory (export "m") 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (core func $readdir-low (canon lower (func $readdir) (memory $m)))
  ;; run returns 7 and calls nothing: it is there so an engine that links the import reaches a call.
  (core module $gm (import "" "readdir" (func (param i32 i32)))
    (func (export "run") (result i32) (i32.const 7)))
  (core instance $gi (instantiate $gm (with "" (instance (export "readdir" (func $readdir-low))))))
  (func (export "run") (result u32) (canon lift (core func $gi "run"))))
