;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; fs-mutate (#1374 E1c, ADR 0133, ADR 0138): wasi:filesystem@0.3's four mutations at the ABI, below any std:
;; create-directory-at, unlink-file-at, remove-directory-at and rename-at, with the confinement escapes, the error
;; cases, rename-at's second descriptor, and a directory opened without mutate-directory. `run(row)` returns four
;; result bytes packed into a u32, byte 0 the first. An operation's byte is 0 for ok and 0x80 | its error-code case
;; for an error; a "kind" byte is stat-at's descriptor-type case (directory 2, regular-file 5) when it is ok, and its
;; error byte when it is not.
;;
;; The grants are the two preopens get-directories returns: the tree at "/" and a second tree at "/b". The tree holds
;; hello.txt ("hello world", 11 bytes), w.txt ("0123456789"), sub/inner.txt ("inner"), empty/ (empty), full/f ("x"),
;; in-link -> hello.txt, dir-link -> sub, up-link -> .., and out-link -> the absolute path of outside/, a sibling of
;; the tree that holds victim.txt ("v") and victim/ (empty). The second tree holds other.txt ("other"). The harness
;; makes all three before each run, so a row that writes sees a fresh one, and an escape that was not refused would
;; reach outside/.
;;
;; Every method is async-lowered, but for rename-at's sync lower in the sync-lower row. create-directory-at,
;; unlink-file-at and remove-directory-at take (self, path) flat with their result at 64; rename-at's six flat
;; parameters spill to the tuple at 256 (self @256, old-path @260/@264, new-descriptor @268, new-path @272/@276).
;; open-at's tuple is at 16 and its result at 48, stat-at's result at 64 with its record at 72 (type @72, size @96).
;; Every async lower that does not return RETURNED (2) is waited for on a waitable set. `run` is async-typed and lifted
;; with no async option, so it may block. The paths are 32-byte slots from 512, listed at the data segment.
;;
;; Readings: fs-mutate.reading (wasmtime 49.0.3; the read-only-grant row from the WIT's text, and the rows where
;; Burroughs departs by ADR 0138, with wasmtime's reading recorded beside them).
;; Assembled with `wasm-tools parse fs-mutate.wat -o fs-mutate.wasm` (wasm-tools 1.258.0).
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
    (type $fsz u64)
    (export "filesize" (type $fsz2 (eq $fsz)))
    (type $df (flags "read" "write" "file-integrity-sync" "data-integrity-sync" "requested-write-sync" "mutate-directory"))
    (export "descriptor-flags" (type $df2 (eq $df)))
    (type $pf (flags "symlink-follow"))
    (export "path-flags" (type $pf2 (eq $pf)))
    (type $of (flags "create" "directory" "exclusive" "truncate"))
    (export "open-flags" (type $of2 (eq $of)))
    (type $dt (variant (case "block-device") (case "character-device") (case "directory") (case "fifo")
      (case "symbolic-link") (case "regular-file") (case "socket") (case "other" (option string))))
    (export "descriptor-type" (type $dt2 (eq $dt)))
    (type $lc u64)
    (export "link-count" (type $lc2 (eq $lc)))
    (type $inst (record (field "seconds" s64) (field "nanoseconds" u32)))
    (export "instant" (type $inst2 (eq $inst)))
    (type $oinst (option $inst2))
    (type $stat (record (field "type" $dt2) (field "link-count" $lc2) (field "size" $fsz2)
      (field "data-access-timestamp" $oinst) (field "data-modification-timestamp" $oinst)
      (field "status-change-timestamp" $oinst)))
    (export "descriptor-stat" (type $stat2 (eq $stat)))
    (type $self (borrow 0))
    (type $r (result (error $ec2)))
    (type $rs (result $stat2 (error $ec2)))
    (export "[method]descriptor.stat-at"
      (func async (param "self" $self) (param "path-flags" $pf2) (param "path" string) (result $rs)))
    (type $ro (result (own 0) (error $ec2)))
    (export "[method]descriptor.open-at"
      (func async (param "self" $self) (param "path-flags" $pf2) (param "path" string) (param "open-flags" $of2)
        (param "flags" $df2) (result $ro)))
    (export "[method]descriptor.create-directory-at" (func async (param "self" $self) (param "path" string) (result $r)))
    (export "[method]descriptor.unlink-file-at" (func async (param "self" $self) (param "path" string) (result $r)))
    (export "[method]descriptor.remove-directory-at" (func async (param "self" $self) (param "path" string) (result $r)))
    (export "[method]descriptor.rename-at"
      (func async (param "self" $self) (param "old-path" string) (param "new-descriptor" $self) (param "new-path" string)
        (result $r)))))
  (import "wasi:filesystem/types@0.3.0" (instance $types (type $types-t)))
  (alias export $types "descriptor" (type $descriptor))
  (type $pre-t (instance
    (alias outer 1 $descriptor (type $d))
    (export "descriptor" (type $d2 (eq $d)))
    (type $own (own $d2))
    (type $pair (tuple $own string))
    (type $list (list $pair))
    (export "get-directories" (func (result $list)))))
  (import "wasi:filesystem/preopens@0.3.0" (instance $pre (type $pre-t)))
  (alias export $pre "get-directories" (func $getdirs))
  (alias export $types "[method]descriptor.stat-at" (func $statat))
  (alias export $types "[method]descriptor.open-at" (func $openat))
  (alias export $types "[method]descriptor.create-directory-at" (func $mkdir))
  (alias export $types "[method]descriptor.unlink-file-at" (func $unlink))
  (alias export $types "[method]descriptor.remove-directory-at" (func $rmdir))
  (alias export $types "[method]descriptor.rename-at" (func $rename))
  ;; A bump allocator from 4096, for get-directories' list and its strings. Nothing else is ever given one.
  (core module $mm
    (memory (export "m") 1)
    (global $heap (mut i32) (i32.const 4096))
    (func (export "realloc") (param i32 i32) (param $align i32) (param $size i32) (result i32) (local $p i32)
      (local.set $p (i32.and (i32.add (global.get $heap) (i32.sub (local.get $align) (i32.const 1)))
        (i32.sub (i32.const 0) (local.get $align))))
      (global.set $heap (i32.add (local.get $p) (local.get $size)))
      (local.get $p)))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (alias core export $mi "realloc" (core func $realloc))
  (core func $getdirs-low (canon lower (func $getdirs) (memory $m) (realloc $realloc)))
  (core func $statat-low (canon lower (func $statat) async (memory $m) (realloc $realloc)))
  (core func $openat-low (canon lower (func $openat) async (memory $m) (realloc $realloc)))
  (core func $mkdir-low (canon lower (func $mkdir) async (memory $m) (realloc $realloc)))
  (core func $unlink-low (canon lower (func $unlink) async (memory $m) (realloc $realloc)))
  (core func $rmdir-low (canon lower (func $rmdir) async (memory $m) (realloc $realloc)))
  (core func $rename-low (canon lower (func $rename) async (memory $m) (realloc $realloc)))
  ;; The sync lower of rename-at (ADR 0126), for the sync-lower row.
  (core func $rename-sync (canon lower (func $rename) (memory $m) (realloc $realloc)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $m)))
  (core module $gm
    (import "" "m" (memory 1))
    (import "" "getdirs" (func $getdirs (param i32)))
    (import "" "statat" (func $statatlow (param i32 i32 i32 i32 i32) (result i32)))
    (import "" "openat" (func $openat (param i32 i32) (result i32)))
    (import "" "mkdir" (func $mkdirlow (param i32 i32 i32 i32) (result i32)))
    (import "" "unlink" (func $unlinklow (param i32 i32 i32 i32) (result i32)))
    (import "" "rmdir" (func $rmdirlow (param i32 i32 i32 i32) (result i32)))
    (import "" "rename" (func $renamelow (param i32 i32) (result i32)))
    (import "" "rename-sync" (func $rename-sync (param i32 i32 i32 i32 i32 i32 i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    ;; The path slots, 32 bytes apart from 512: slot k is at 512 + 32k.
    (data (i32.const 512) "hello.txt")                    ;; s0  9
    (data (i32.const 544) "newdir")                       ;; s1  6
    (data (i32.const 576) "missing/x")                    ;; s2  9
    (data (i32.const 608) "hello.txt/x")                  ;; s3  11
    (data (i32.const 640) "sub")                          ;; s4  3
    (data (i32.const 672) "../x")                         ;; s5  4
    (data (i32.const 704) "/x")                           ;; s6  2
    (data (i32.const 736) "up-link/x")                    ;; s7  9
    (data (i32.const 768) "out-link/x")                   ;; s8  10
    (data (i32.const 800) "deeper")                       ;; s9  6
    (data (i32.const 832) "sub/deeper")                   ;; s10 10
    (data (i32.const 864) "w.txt")                        ;; s11 5
    (data (i32.const 896) "missing.txt")                  ;; s12 11
    (data (i32.const 928) "in-link")                      ;; s13 7
    (data (i32.const 960) "empty")                        ;; s14 5
    (data (i32.const 992) "dir-link")                     ;; s15 8
    (data (i32.const 1024) "../outside/victim.txt")       ;; s16 21
    (data (i32.const 1056) "up-link/outside/victim.txt")  ;; s17 26
    (data (i32.const 1088) "out-link/victim.txt")         ;; s18 19
    (data (i32.const 1120) "full")                        ;; s19 4
    (data (i32.const 1152) "missing")                     ;; s20 7
    (data (i32.const 1184) ".")                           ;; s21 1
    (data (i32.const 1216) "sub/inner.txt")               ;; s22 13
    (data (i32.const 1248) "../outside/victim")           ;; s23 17
    (data (i32.const 1280) "up-link/outside/victim")      ;; s24 22
    (data (i32.const 1312) "out-link/victim")             ;; s25 15
    (data (i32.const 1344) "moved.txt")                   ;; s26 9
    (data (i32.const 1376) "sub/inner")                   ;; s27 9
    (data (i32.const 1408) "stolen")                      ;; s28 6
    (data (i32.const 1440) "../outside/leaked")           ;; s29 17
    (data (i32.const 1472) "out-link/leaked")             ;; s30 15
    (data (i32.const 1504) "inner.txt")                   ;; s31 9
    (data (i32.const 1536) "inner-up.txt")                ;; s32 12
    (data (i32.const 1568) "sub/moved.txt")               ;; s33 13
    (data (i32.const 1600) "../hello.txt")                ;; s34 12
    (data (i32.const 1632) "sub2")                        ;; s35 4
    (data (i32.const 1664) "sub2/moved.txt")              ;; s36 14
    (data (i32.const 1696) "x")                           ;; s37 1
    (data (i32.const 1728) "other.txt")                   ;; s38 9
    (data (i32.const 1760) "empty/inner.txt")             ;; s39 15
    (global $set (mut i32) (i32.const 0))
    ;; h: the last open's handle.
    (global $h (mut i32) (i32.const 0))
    ;; await: a subtask's event once it has resolved; BLOCKED and STARTED are waited for on the set.
    (func $await (param $w i32)
      (if (i32.eqz (global.get $set)) (then (global.set $set (call $wsnew))))
      (call $wsjoin (local.get $w) (global.get $set))
      (drop (call $wswait (global.get $set) (i32.const 192)))
      (call $wsjoin (local.get $w) (i32.const 0)))
    ;; settle: an async lower's subtask, waited for unless it returned RETURNED (2) inline.
    (func $settle (param $s i32)
      (if (i32.ne (i32.and (local.get $s) (i32.const 0xf)) (i32.const 2))
        (then (call $await (i32.shr_u (local.get $s) (i32.const 4))))))
    ;; res4 and res8: the result at ret, its payload at +4 or +8.
    (func $res4 (param $ret i32) (result i32)
      (if (result i32) (i32.eqz (i32.load8_u (local.get $ret)))
        (then (i32.const 0))
        (else (i32.or (i32.const 0x80) (i32.load8_u (i32.add (local.get $ret) (i32.const 4)))))))
    (func $res8 (param $ret i32) (result i32)
      (if (result i32) (i32.eqz (i32.load8_u (local.get $ret)))
        (then (i32.const 0))
        (else (i32.or (i32.const 0x80) (i32.load8_u (i32.add (local.get $ret) (i32.const 8)))))))
    ;; dirs: get-directories, its list at 0; dir and dir2 are its first and second preopens' descriptors.
    (func $dirs (call $getdirs (i32.const 0)))
    (func $dir (result i32) (i32.load (i32.load (i32.const 0))))
    (func $dir2 (result i32) (i32.load offset=12 (i32.load (i32.const 0))))
    ;; open: open-at(base, path-flags, path, open-flags, flags), its result byte; the handle in $h.
    (func $open (param $base i32) (param $pf i32) (param $p i32) (param $n i32) (param $of i32) (param $df i32)
      (result i32)
      (i32.store (i32.const 16) (local.get $base))
      (i32.store8 (i32.const 20) (local.get $pf))
      (i32.store (i32.const 24) (local.get $p))
      (i32.store (i32.const 28) (local.get $n))
      (i32.store8 (i32.const 32) (local.get $of))
      (i32.store8 (i32.const 33) (local.get $df))
      (call $settle (call $openat (i32.const 16) (i32.const 48)))
      (global.set $h (i32.load (i32.const 52)))
      (call $res4 (i32.const 48)))
    ;; subdir: open-at sub, symlink-follow, open-flags directory, with the descriptor-flags df; the handle in $h.
    (func $subdir (param $d i32) (param $df i32) (result i32)
      (call $open (local.get $d) (i32.const 1) (i32.const 640) (i32.const 3) (i32.const 2) (local.get $df)))
    ;; kind: stat-at(d, pf, path): its type case when ok, its error byte when not; the size stays at 96.
    (func $kind (param $d i32) (param $pf i32) (param $p i32) (param $n i32) (result i32) (local $r i32)
      (call $settle (call $statatlow (local.get $d) (local.get $pf) (local.get $p) (local.get $n) (i32.const 64)))
      (local.set $r (call $res8 (i32.const 64)))
      (if (result i32) (i32.eqz (local.get $r))
        (then (i32.load8_u (i32.const 72)))
        (else (local.get $r))))
    (func $mk (param $d i32) (param $p i32) (param $n i32) (result i32)
      (call $settle (call $mkdirlow (local.get $d) (local.get $p) (local.get $n) (i32.const 64)))
      (call $res4 (i32.const 64)))
    (func $ul (param $d i32) (param $p i32) (param $n i32) (result i32)
      (call $settle (call $unlinklow (local.get $d) (local.get $p) (local.get $n) (i32.const 64)))
      (call $res4 (i32.const 64)))
    (func $rd (param $d i32) (param $p i32) (param $n i32) (result i32)
      (call $settle (call $rmdirlow (local.get $d) (local.get $p) (local.get $n) (i32.const 64)))
      (call $res4 (i32.const 64)))
    ;; mv: rename-at(d, old, nd, new), its six parameters spilled to 256.
    (func $mv (param $d i32) (param $op i32) (param $on i32) (param $nd i32) (param $np i32) (param $nn i32)
      (result i32)
      (i32.store (i32.const 256) (local.get $d))
      (i32.store (i32.const 260) (local.get $op))
      (i32.store (i32.const 264) (local.get $on))
      (i32.store (i32.const 268) (local.get $nd))
      (i32.store (i32.const 272) (local.get $np))
      (i32.store (i32.const 276) (local.get $nn))
      (call $settle (call $renamelow (i32.const 256) (i32.const 64)))
      (call $res4 (i32.const 64)))
    (func $pack (param $b0 i32) (param $b1 i32) (param $b2 i32) (param $b3 i32) (result i32)
      (i32.or (i32.or (i32.and (local.get $b0) (i32.const 0xff)) (i32.shl (i32.and (local.get $b1) (i32.const 0xff)) (i32.const 8)))
        (i32.or (i32.shl (i32.and (local.get $b2) (i32.const 0xff)) (i32.const 16)) (i32.shl (local.get $b3) (i32.const 24)))))
    (func (export "run") (param $row i32) (result i32) (local $d i32) (local $b i32) (local $s i32) (local $x i32)
      (local $y i32) (local $z i32)
      (call $dirs)
      (local.set $d (call $dir))
      (local.set $b (call $dir2))
      ;; 0 mkdir: create-directory-at newdir; stat-at newdir's kind; create-directory-at newdir again.
      (if (i32.eq (local.get $row) (i32.const 0)) (then
        (local.set $x (call $mk (local.get $d) (i32.const 544) (i32.const 6)))
        (local.set $y (call $kind (local.get $d) (i32.const 1) (i32.const 544) (i32.const 6)))
        (return (call $pack (local.get $x) (local.get $y) (call $mk (local.get $d) (i32.const 544) (i32.const 6))
          (i32.const 0)))))
      ;; 1 mkdir-errors: missing/x, hello.txt/x, sub, and the empty path.
      (if (i32.eq (local.get $row) (i32.const 1)) (then
        (return (call $pack (call $mk (local.get $d) (i32.const 576) (i32.const 9))
          (call $mk (local.get $d) (i32.const 608) (i32.const 11))
          (call $mk (local.get $d) (i32.const 640) (i32.const 3))
          (call $mk (local.get $d) (i32.const 512) (i32.const 0))))))
      ;; 2 mkdir-escapes: ../x, /x, up-link/x, out-link/x.
      (if (i32.eq (local.get $row) (i32.const 2)) (then
        (return (call $pack (call $mk (local.get $d) (i32.const 672) (i32.const 4))
          (call $mk (local.get $d) (i32.const 704) (i32.const 2))
          (call $mk (local.get $d) (i32.const 736) (i32.const 9))
          (call $mk (local.get $d) (i32.const 768) (i32.const 10))))))
      ;; 3 mkdir-nested: sub opened read|mutate-directory; deeper from it; ../x from it; the grant's sub/deeper kind.
      (if (i32.eq (local.get $row) (i32.const 3)) (then
        (local.set $x (call $subdir (local.get $d) (i32.const 33)))
        (local.set $s (global.get $h))
        (local.set $y (call $mk (local.get $s) (i32.const 800) (i32.const 6)))
        (local.set $z (call $mk (local.get $s) (i32.const 672) (i32.const 4)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z)
          (call $kind (local.get $d) (i32.const 1) (i32.const 832) (i32.const 10))))))
      ;; 4 unlink: w.txt; w.txt's kind after; missing.txt; in-link (the link, not hello.txt).
      (if (i32.eq (local.get $row) (i32.const 4)) (then
        (local.set $x (call $ul (local.get $d) (i32.const 864) (i32.const 5)))
        (local.set $y (call $kind (local.get $d) (i32.const 0) (i32.const 864) (i32.const 5)))
        (return (call $pack (local.get $x) (local.get $y) (call $ul (local.get $d) (i32.const 896) (i32.const 11))
          (call $ul (local.get $d) (i32.const 928) (i32.const 7))))))
      ;; 5 unlink-kinds: sub (a directory), empty (an empty one), dir-link (a link to one), hello.txt/x.
      (if (i32.eq (local.get $row) (i32.const 5)) (then
        (return (call $pack (call $ul (local.get $d) (i32.const 640) (i32.const 3))
          (call $ul (local.get $d) (i32.const 960) (i32.const 5))
          (call $ul (local.get $d) (i32.const 992) (i32.const 8))
          (call $ul (local.get $d) (i32.const 608) (i32.const 11))))))
      ;; 6 unlink-escapes: ../outside/victim.txt, /x, up-link/outside/victim.txt, out-link/victim.txt.
      (if (i32.eq (local.get $row) (i32.const 6)) (then
        (return (call $pack (call $ul (local.get $d) (i32.const 1024) (i32.const 21))
          (call $ul (local.get $d) (i32.const 704) (i32.const 2))
          (call $ul (local.get $d) (i32.const 1056) (i32.const 26))
          (call $ul (local.get $d) (i32.const 1088) (i32.const 19))))))
      ;; 7 rmdir: empty; empty's kind after; full; missing.
      (if (i32.eq (local.get $row) (i32.const 7)) (then
        (local.set $x (call $rd (local.get $d) (i32.const 960) (i32.const 5)))
        (local.set $y (call $kind (local.get $d) (i32.const 0) (i32.const 960) (i32.const 5)))
        (return (call $pack (local.get $x) (local.get $y) (call $rd (local.get $d) (i32.const 1120) (i32.const 4))
          (call $rd (local.get $d) (i32.const 1152) (i32.const 7))))))
      ;; 8 rmdir-kinds: hello.txt, dir-link, ".", sub/inner.txt.
      (if (i32.eq (local.get $row) (i32.const 8)) (then
        (return (call $pack (call $rd (local.get $d) (i32.const 512) (i32.const 9))
          (call $rd (local.get $d) (i32.const 992) (i32.const 8))
          (call $rd (local.get $d) (i32.const 1184) (i32.const 1))
          (call $rd (local.get $d) (i32.const 1216) (i32.const 13))))))
      ;; 9 rmdir-escapes: ../outside/victim, /x, up-link/outside/victim, out-link/victim.
      (if (i32.eq (local.get $row) (i32.const 9)) (then
        (return (call $pack (call $rd (local.get $d) (i32.const 1248) (i32.const 17))
          (call $rd (local.get $d) (i32.const 704) (i32.const 2))
          (call $rd (local.get $d) (i32.const 1280) (i32.const 22))
          (call $rd (local.get $d) (i32.const 1312) (i32.const 15))))))
      ;; 10 rename: hello.txt to moved.txt; moved.txt's kind and size; hello.txt's kind after.
      (if (i32.eq (local.get $row) (i32.const 10)) (then
        (local.set $x (call $mv (local.get $d) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 1344) (i32.const 9)))
        (local.set $y (call $kind (local.get $d) (i32.const 1) (i32.const 1344) (i32.const 9)))
        (local.set $z (i32.load (i32.const 96)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z)
          (call $kind (local.get $d) (i32.const 0) (i32.const 512) (i32.const 9))))))
      ;; 11 rename-over: w.txt over hello.txt; empty over full; hello.txt over sub; empty over hello.txt.
      (if (i32.eq (local.get $row) (i32.const 11)) (then
        (return (call $pack
          (call $mv (local.get $d) (i32.const 864) (i32.const 5) (local.get $d) (i32.const 512) (i32.const 9))
          (call $mv (local.get $d) (i32.const 960) (i32.const 5) (local.get $d) (i32.const 1120) (i32.const 4))
          (call $mv (local.get $d) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 640) (i32.const 3))
          (call $mv (local.get $d) (i32.const 960) (i32.const 5) (local.get $d) (i32.const 512) (i32.const 9))))))
      ;; 12 rename-errors: missing to x; hello.txt to missing/x; hello.txt to hello.txt/x; sub into sub/inner.
      (if (i32.eq (local.get $row) (i32.const 12)) (then
        (return (call $pack
          (call $mv (local.get $d) (i32.const 1152) (i32.const 7) (local.get $d) (i32.const 1696) (i32.const 1))
          (call $mv (local.get $d) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 576) (i32.const 9))
          (call $mv (local.get $d) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 608) (i32.const 11))
          (call $mv (local.get $d) (i32.const 640) (i32.const 3) (local.get $d) (i32.const 1376) (i32.const 9))))))
      ;; 13 rename-escapes: ../outside/victim.txt to stolen; hello.txt to ../outside/leaked; hello.txt to
      ;; out-link/leaked; /x to x.
      (if (i32.eq (local.get $row) (i32.const 13)) (then
        (return (call $pack
          (call $mv (local.get $d) (i32.const 1024) (i32.const 21) (local.get $d) (i32.const 1408) (i32.const 6))
          (call $mv (local.get $d) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 1440) (i32.const 17))
          (call $mv (local.get $d) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 1472) (i32.const 15))
          (call $mv (local.get $d) (i32.const 704) (i32.const 2) (local.get $d) (i32.const 1696) (i32.const 1))))))
      ;; 14 rename-two: sub opened read|mutate-directory; hello.txt from the grant to sub's moved.txt; sub's inner.txt to
      ;; the grant's inner-up.txt; the grant's sub/moved.txt kind.
      (if (i32.eq (local.get $row) (i32.const 14)) (then
        (local.set $x (call $subdir (local.get $d) (i32.const 33)))
        (local.set $s (global.get $h))
        (local.set $y (call $mv (local.get $d) (i32.const 512) (i32.const 9) (local.get $s) (i32.const 1344) (i32.const 9)))
        (local.set $z (call $mv (local.get $s) (i32.const 1504) (i32.const 9) (local.get $d) (i32.const 1536) (i32.const 12)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z)
          (call $kind (local.get $d) (i32.const 1) (i32.const 1568) (i32.const 13))))))
      ;; 15 rename-nested-escape: sub opened read|mutate-directory; from sub ../hello.txt to x; sub's inner.txt to ../x;
      ;; the grant's hello.txt to sub's ../x.
      (if (i32.eq (local.get $row) (i32.const 15)) (then
        (local.set $x (call $subdir (local.get $d) (i32.const 33)))
        (local.set $s (global.get $h))
        (return (call $pack (local.get $x)
          (call $mv (local.get $s) (i32.const 1600) (i32.const 12) (local.get $s) (i32.const 1696) (i32.const 1))
          (call $mv (local.get $s) (i32.const 1504) (i32.const 9) (local.get $s) (i32.const 672) (i32.const 4))
          (call $mv (local.get $d) (i32.const 512) (i32.const 9) (local.get $s) (i32.const 672) (i32.const 4))))))
      ;; 16 rename-two-calls: a second get-directories' grant; hello.txt from the first call's to the second's
      ;; moved.txt; moved.txt's kind through the second; hello.txt's through the first; whether the handles differ.
      (if (i32.eq (local.get $row) (i32.const 16)) (then
        (call $dirs)
        (local.set $s (call $dir))
        (local.set $x (call $mv (local.get $d) (i32.const 512) (i32.const 9) (local.get $s) (i32.const 1344) (i32.const 9)))
        (local.set $y (call $kind (local.get $s) (i32.const 1) (i32.const 1344) (i32.const 9)))
        (return (call $pack (local.get $x) (local.get $y)
          (call $kind (local.get $d) (i32.const 0) (i32.const 512) (i32.const 9))
          (i32.ne (local.get $s) (local.get $d))))))
      ;; 17 rename-cross-grant: hello.txt from the first grant to the second's moved.txt; hello.txt's kind; moved.txt's
      ;; kind through the second; the second's other.txt to the first's x.
      (if (i32.eq (local.get $row) (i32.const 17)) (then
        (local.set $x (call $mv (local.get $d) (i32.const 512) (i32.const 9) (local.get $b) (i32.const 1344) (i32.const 9)))
        (local.set $y (call $kind (local.get $d) (i32.const 0) (i32.const 512) (i32.const 9)))
        (local.set $z (call $kind (local.get $b) (i32.const 0) (i32.const 1344) (i32.const 9)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z)
          (call $mv (local.get $b) (i32.const 1728) (i32.const 9) (local.get $d) (i32.const 1696) (i32.const 1))))))
      ;; 18 rename-stale: sub opened read|mutate-directory; the grant's sub renamed to sub2; hello.txt to the open sub's
      ;; moved.txt; sub2/moved.txt's kind.
      (if (i32.eq (local.get $row) (i32.const 18)) (then
        (drop (call $subdir (local.get $d) (i32.const 33)))
        (local.set $s (global.get $h))
        (local.set $x (call $mv (local.get $d) (i32.const 640) (i32.const 3) (local.get $d) (i32.const 1632) (i32.const 4)))
        (local.set $y (call $mv (local.get $d) (i32.const 512) (i32.const 9) (local.get $s) (i32.const 1344) (i32.const 9)))
        (return (call $pack (local.get $x) (local.get $y)
          (call $kind (local.get $d) (i32.const 1) (i32.const 1664) (i32.const 14))
          (call $kind (local.get $d) (i32.const 0) (i32.const 512) (i32.const 9))))))
      ;; 19 base-file: hello.txt opened read; create-directory-at, unlink-file-at, remove-directory-at and rename-at with
      ;; it as the base.
      (if (i32.eq (local.get $row) (i32.const 19)) (then
        (drop (call $open (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9) (i32.const 0) (i32.const 1)))
        (local.set $s (global.get $h))
        (return (call $pack (call $mk (local.get $s) (i32.const 544) (i32.const 6))
          (call $ul (local.get $s) (i32.const 512) (i32.const 9))
          (call $rd (local.get $s) (i32.const 640) (i32.const 3))
          (call $mv (local.get $s) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 1696) (i32.const 1))))))
      ;; 20 child-read: sub opened read only (no mutate-directory); from it create-directory-at deeper, unlink-file-at
      ;; inner.txt, unlink-file-at missing.txt, and rename-at inner.txt to the grant's x.
      (if (i32.eq (local.get $row) (i32.const 20)) (then
        (drop (call $subdir (local.get $d) (i32.const 1)))
        (local.set $s (global.get $h))
        (return (call $pack (call $mk (local.get $s) (i32.const 800) (i32.const 6))
          (call $ul (local.get $s) (i32.const 1504) (i32.const 9))
          (call $ul (local.get $s) (i32.const 896) (i32.const 11))
          (call $mv (local.get $s) (i32.const 1504) (i32.const 9) (local.get $d) (i32.const 1696) (i32.const 1))))))
      ;; 21 child-read-more: sub opened read only; the grant's hello.txt to its x; remove-directory-at missing from it;
      ;; create-directory-at ../x from it; create-directory-at inner.txt (which exists) from it.
      (if (i32.eq (local.get $row) (i32.const 21)) (then
        (drop (call $subdir (local.get $d) (i32.const 1)))
        (local.set $s (global.get $h))
        (return (call $pack
          (call $mv (local.get $d) (i32.const 512) (i32.const 9) (local.get $s) (i32.const 1696) (i32.const 1))
          (call $rd (local.get $s) (i32.const 1152) (i32.const 7))
          (call $mk (local.get $s) (i32.const 672) (i32.const 4))
          (call $mk (local.get $s) (i32.const 1504) (i32.const 9))))))
      ;; 22 sync-lower: rename-at through its sync lower, hello.txt to moved.txt; moved.txt's kind.
      (if (i32.eq (local.get $row) (i32.const 22)) (then
        (call $rename-sync (local.get $d) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 1344) (i32.const 9)
          (i32.const 64))
        (local.set $x (call $res4 (i32.const 64)))
        (return (call $pack (local.get $x) (call $kind (local.get $d) (i32.const 1) (i32.const 1344) (i32.const 9))
          (i32.const 0) (i32.const 0)))))
      ;; 23 read-only: on a read-only grant, create-directory-at newdir, unlink-file-at hello.txt, remove-directory-at
      ;; empty, and rename-at hello.txt to moved.txt.
      (if (i32.eq (local.get $row) (i32.const 23)) (then
        (return (call $pack (call $mk (local.get $d) (i32.const 544) (i32.const 6))
          (call $ul (local.get $d) (i32.const 512) (i32.const 9))
          (call $rd (local.get $d) (i32.const 960) (i32.const 5))
          (call $mv (local.get $d) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 1344) (i32.const 9))))))
      ;; 24 rename-dir-over-empty: sub over empty (an empty directory); empty/inner.txt's kind; sub's kind; hello.txt
      ;; over itself.
      (if (i32.eq (local.get $row) (i32.const 24)) (then
        (local.set $x (call $mv (local.get $d) (i32.const 640) (i32.const 3) (local.get $d) (i32.const 960) (i32.const 5)))
        (local.set $y (call $kind (local.get $d) (i32.const 1) (i32.const 1760) (i32.const 15)))
        (local.set $z (call $kind (local.get $d) (i32.const 0) (i32.const 640) (i32.const 3)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z)
          (call $mv (local.get $d) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 512) (i32.const 9))))))
      (unreachable)))
  (core instance $gi (instantiate $gm (with "" (instance (export "m" (memory $m)) (export "getdirs" (func $getdirs-low))
    (export "statat" (func $statat-low)) (export "openat" (func $openat-low)) (export "mkdir" (func $mkdir-low))
    (export "unlink" (func $unlink-low)) (export "rmdir" (func $rmdir-low)) (export "rename" (func $rename-low))
    (export "rename-sync" (func $rename-sync)) (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
    (export "wswait" (func $wswait))))))
  (type $runt (func async (param "row" u32) (result u32)))
  (func (export "run") (type $runt) (canon lift (core func $gi "run"))))
