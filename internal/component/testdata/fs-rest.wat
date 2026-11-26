;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; fs-rest (#1374 E1d, ADR 0133, ADR 0138): the rest of wasi:filesystem@0.3's descriptor at the ABI, below any std:
;; readlink-at, symlink-at, link-at, set-size, set-times, set-times-at, sync, sync-data, advise, metadata-hash,
;; metadata-hash-at, is-same-object and append-via-stream, with the confinement escapes and the error cases.
;; `run(row)` returns four result bytes packed into a u32, byte 0 the first. An operation's byte is 0 for ok and 0x80 |
;; its error-code case for an error; a "kind" byte is stat-at's descriptor-type case (directory 2, symbolic-link 4,
;; regular-file 5) when it is ok, and its error byte when it is not; a stream write's byte is its event payload,
;; progress << 4 | the copy result (COMPLETED 0, DROPPED 1).
;;
;; The grants are the two preopens get-directories returns: the tree at "/" and a second tree at "/b". The tree holds
;; hello.txt ("hello world", 11 bytes), w.txt ("0123456789"), sub/inner.txt ("inner"), in-link -> hello.txt,
;; dir-link -> sub, up-link -> .., rel-out -> ../outside, and out-link -> the absolute path of outside/, a sibling of
;; the tree holding victim.txt ("v"). The second tree holds other.txt ("other"). The harness makes all three before
;; each run.
;;
;; Every async method is async-lowered, its result at 64, but for the sync lowers of readlink-at and is-same-object in
;; the sync-lower row. A stat's record is at 72: type @72, link-count @88, size @96, and the access and modification
;; instants' discriminants @104 and @128, seconds @112 and @136, nanoseconds @120 and @144. A metadata hash's lower
;; and upper are at 72 and 80, and $save copies them to 224. readlink-at's string is at (@68, @72). symlink-at,
;; link-at, set-times and set-times-at spill to the tuple at 256. open-at's tuple is at 16 and its result at 48;
;; read-via-stream's handles land at 200 and 204, its bytes at 2560, a future's result at 208; a write's bytes come
;; from 3072. `run` is async-typed and lifted with no async option, so it may block. The paths are 32-byte slots from
;; 512, listed at the data segment.
;;
;; Readings: fs-rest.reading (wasmtime 49.0.3; the read-only-grant rows from the WIT's text, and the rows where
;; Burroughs departs by ADR 0138, with wasmtime's reading recorded beside them).
;; Assembled with `wasm-tools parse fs-rest.wat -o fs-rest.wasm` (wasm-tools 1.258.0).
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
    (type $nt (variant (case "no-change") (case "now") (case "timestamp" $inst2)))
    (export "new-timestamp" (type $nt2 (eq $nt)))
    (type $adv (enum "normal" "sequential" "random" "will-need" "dont-need" "no-reuse"))
    (export "advice" (type $adv2 (eq $adv)))
    (type $mhv (record (field "lower" u64) (field "upper" u64)))
    (export "metadata-hash-value" (type $mhv2 (eq $mhv)))
    (type $self (borrow 0))
    (type $bytes (stream u8))
    (type $r (result (error $ec2)))
    (type $fut (future $r))
    (type $rvs (tuple $bytes $fut))
    (export "[method]descriptor.read-via-stream" (func (param "self" $self) (param "offset" $fsz2) (result $rvs)))
    (export "[method]descriptor.write-via-stream"
      (func (param "self" $self) (param "data" $bytes) (param "offset" $fsz2) (result $fut)))
    (export "[method]descriptor.append-via-stream" (func (param "self" $self) (param "data" $bytes) (result $fut)))
    (type $rs (result $stat2 (error $ec2)))
    (export "[method]descriptor.stat" (func async (param "self" $self) (result $rs)))
    (export "[method]descriptor.stat-at"
      (func async (param "self" $self) (param "path-flags" $pf2) (param "path" string) (result $rs)))
    (type $ro (result (own 0) (error $ec2)))
    (export "[method]descriptor.open-at"
      (func async (param "self" $self) (param "path-flags" $pf2) (param "path" string) (param "open-flags" $of2)
        (param "flags" $df2) (result $ro)))
    (type $rstr (result string (error $ec2)))
    (export "[method]descriptor.readlink-at" (func async (param "self" $self) (param "path" string) (result $rstr)))
    (export "[method]descriptor.symlink-at"
      (func async (param "self" $self) (param "old-path" string) (param "new-path" string) (result $r)))
    (export "[method]descriptor.link-at"
      (func async (param "self" $self) (param "old-path-flags" $pf2) (param "old-path" string)
        (param "new-descriptor" $self) (param "new-path" string) (result $r)))
    (export "[method]descriptor.set-size" (func async (param "self" $self) (param "size" $fsz2) (result $r)))
    (export "[method]descriptor.set-times"
      (func async (param "self" $self) (param "data-access-timestamp" $nt2) (param "data-modification-timestamp" $nt2)
        (result $r)))
    (export "[method]descriptor.set-times-at"
      (func async (param "self" $self) (param "path-flags" $pf2) (param "path" string)
        (param "data-access-timestamp" $nt2) (param "data-modification-timestamp" $nt2) (result $r)))
    (export "[method]descriptor.sync" (func async (param "self" $self) (result $r)))
    (export "[method]descriptor.sync-data" (func async (param "self" $self) (result $r)))
    (export "[method]descriptor.advise"
      (func async (param "self" $self) (param "offset" $fsz2) (param "length" $fsz2) (param "advice" $adv2) (result $r)))
    (type $rmh (result $mhv2 (error $ec2)))
    (export "[method]descriptor.metadata-hash" (func async (param "self" $self) (result $rmh)))
    (export "[method]descriptor.metadata-hash-at"
      (func async (param "self" $self) (param "path-flags" $pf2) (param "path" string) (result $rmh)))
    (export "[method]descriptor.is-same-object" (func async (param "self" $self) (param "other" $self) (result bool)))))
  (import "wasi:filesystem/types@0.3.0" (instance $types (type $types-t)))
  (alias export $types "descriptor" (type $descriptor))
  (alias export $types "error-code" (type $error-code))
  (type $pre-t (instance
    (alias outer 1 $descriptor (type $d))
    (export "descriptor" (type $d2 (eq $d)))
    (type $own (own $d2))
    (type $pair (tuple $own string))
    (type $list (list $pair))
    (export "get-directories" (func (result $list)))))
  (import "wasi:filesystem/preopens@0.3.0" (instance $pre (type $pre-t)))
  (alias export $pre "get-directories" (func $getdirs))
  (alias export $types "[method]descriptor.read-via-stream" (func $rvs))
  (alias export $types "[method]descriptor.write-via-stream" (func $wvs))
  (alias export $types "[method]descriptor.append-via-stream" (func $avs))
  (alias export $types "[method]descriptor.stat" (func $stat))
  (alias export $types "[method]descriptor.stat-at" (func $statat))
  (alias export $types "[method]descriptor.open-at" (func $openat))
  (alias export $types "[method]descriptor.readlink-at" (func $readlink))
  (alias export $types "[method]descriptor.symlink-at" (func $symlink))
  (alias export $types "[method]descriptor.link-at" (func $link))
  (alias export $types "[method]descriptor.set-size" (func $setsize))
  (alias export $types "[method]descriptor.set-times" (func $settimes))
  (alias export $types "[method]descriptor.set-times-at" (func $settimesat))
  (alias export $types "[method]descriptor.sync" (func $sync))
  (alias export $types "[method]descriptor.sync-data" (func $syncdata))
  (alias export $types "[method]descriptor.advise" (func $advise))
  (alias export $types "[method]descriptor.metadata-hash" (func $mhash))
  (alias export $types "[method]descriptor.metadata-hash-at" (func $mhashat))
  (alias export $types "[method]descriptor.is-same-object" (func $same))
  (type $bytes (stream u8))
  (type $r (result (error $error-code)))
  (type $fut (future $r))
  ;; A bump allocator from 4096, for get-directories' list and its strings and readlink-at's strings.
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
  (core func $rvs-low (canon lower (func $rvs) (memory $m) (realloc $realloc)))
  (core func $wvs-low (canon lower (func $wvs) (memory $m) (realloc $realloc)))
  (core func $avs-low (canon lower (func $avs) (memory $m) (realloc $realloc)))
  (core func $stat-low (canon lower (func $stat) async (memory $m) (realloc $realloc)))
  (core func $statat-low (canon lower (func $statat) async (memory $m) (realloc $realloc)))
  (core func $openat-low (canon lower (func $openat) async (memory $m) (realloc $realloc)))
  (core func $readlink-low (canon lower (func $readlink) async (memory $m) (realloc $realloc)))
  (core func $symlink-low (canon lower (func $symlink) async (memory $m) (realloc $realloc)))
  (core func $link-low (canon lower (func $link) async (memory $m) (realloc $realloc)))
  (core func $setsize-low (canon lower (func $setsize) async (memory $m) (realloc $realloc)))
  (core func $settimes-low (canon lower (func $settimes) async (memory $m) (realloc $realloc)))
  (core func $settimesat-low (canon lower (func $settimesat) async (memory $m) (realloc $realloc)))
  (core func $sync-low (canon lower (func $sync) async (memory $m) (realloc $realloc)))
  (core func $syncdata-low (canon lower (func $syncdata) async (memory $m) (realloc $realloc)))
  (core func $advise-low (canon lower (func $advise) async (memory $m) (realloc $realloc)))
  (core func $mhash-low (canon lower (func $mhash) async (memory $m) (realloc $realloc)))
  (core func $mhashat-low (canon lower (func $mhashat) async (memory $m) (realloc $realloc)))
  (core func $same-low (canon lower (func $same) async (memory $m) (realloc $realloc)))
  ;; The sync lowers of readlink-at and is-same-object (ADR 0126), for the sync-lower row.
  (core func $readlink-sync (canon lower (func $readlink) (memory $m) (realloc $realloc)))
  (core func $same-sync (canon lower (func $same) (memory $m) (realloc $realloc)))
  (core func $sread (canon stream.read $bytes async (memory $m)))
  (core func $swrite (canon stream.write $bytes async (memory $m)))
  (core func $snew (canon stream.new $bytes))
  (core func $sdropw (canon stream.drop-writable $bytes))
  (core func $sdropr (canon stream.drop-readable $bytes))
  (core func $fread (canon future.read $fut async (memory $m) (realloc $realloc)))
  (core func $fdropr (canon future.drop-readable $fut))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $m)))
  (core module $gm
    (import "" "m" (memory 1))
    (import "" "getdirs" (func $getdirs (param i32)))
    (import "" "rvs" (func $rvs (param i32 i64 i32)))
    (import "" "wvs" (func $wvs (param i32 i32 i64) (result i32)))
    (import "" "avs" (func $avs (param i32 i32) (result i32)))
    (import "" "stat" (func $statlow (param i32 i32) (result i32)))
    (import "" "statat" (func $statatlow (param i32 i32 i32 i32 i32) (result i32)))
    (import "" "openat" (func $openat (param i32 i32) (result i32)))
    (import "" "readlink" (func $rllow (param i32 i32 i32 i32) (result i32)))
    (import "" "symlink" (func $sllow (param i32 i32) (result i32)))
    (import "" "link" (func $lnlow (param i32 i32) (result i32)))
    (import "" "setsize" (func $sslow (param i32 i64 i32) (result i32)))
    (import "" "settimes" (func $stlow (param i32 i32) (result i32)))
    (import "" "settimesat" (func $stalow (param i32 i32) (result i32)))
    (import "" "sync" (func $synclow (param i32 i32) (result i32)))
    (import "" "syncdata" (func $sdlow (param i32 i32) (result i32)))
    (import "" "advise" (func $advlow (param i32 i64 i64 i32 i32) (result i32)))
    (import "" "mhash" (func $mhlow (param i32 i32) (result i32)))
    (import "" "mhashat" (func $mhalow (param i32 i32 i32 i32 i32) (result i32)))
    (import "" "same" (func $samelow (param i32 i32 i32) (result i32)))
    (import "" "readlink-sync" (func $readlink-sync (param i32 i32 i32 i32)))
    (import "" "same-sync" (func $same-sync (param i32 i32) (result i32)))
    (import "" "sread" (func $sread (param i32 i32 i32) (result i32)))
    (import "" "swrite" (func $swrite (param i32 i32 i32) (result i32)))
    (import "" "snew" (func $snew (result i64)))
    (import "" "sdropw" (func $sdropw (param i32)))
    (import "" "sdropr" (func $sdropr (param i32)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fdropr" (func $fdropr (param i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    ;; The path slots, 32 bytes apart from 512: slot k is at 512 + 32k.
    (data (i32.const 512) "hello.txt")              ;; s0  9
    (data (i32.const 544) "in-link")                ;; s1  7
    (data (i32.const 576) "dir-link")               ;; s2  8
    (data (i32.const 608) "rel-out")                ;; s3  7
    (data (i32.const 640) "out-link")               ;; s4  8
    (data (i32.const 672) "missing")                ;; s5  7
    (data (i32.const 704) "../x")                   ;; s6  4
    (data (i32.const 736) "up-link/x")              ;; s7  9
    (data (i32.const 768) "newlink")                ;; s8  7
    (data (i32.const 800) "/x")                     ;; s9  2
    (data (i32.const 832) "x")                      ;; s10 1
    (data (i32.const 864) "missing/x")              ;; s11 9
    (data (i32.const 896) "../outside")             ;; s12 10
    (data (i32.const 928) "esc")                    ;; s13 3
    (data (i32.const 960) "esc/victim.txt")         ;; s14 14
    (data (i32.const 992) "hard")                   ;; s15 4
    (data (i32.const 1024) "w.txt")                 ;; s16 5
    (data (i32.const 1056) "sub")                   ;; s17 3
    (data (i32.const 1088) "subhard")               ;; s18 7
    (data (i32.const 1120) "../outside/victim.txt") ;; s19 21
    (data (i32.const 1152) "stolen")                ;; s20 6
    (data (i32.const 1184) "../outside/leaked")     ;; s21 17
    (data (i32.const 1216) "out-link/victim.txt")   ;; s22 19
    (data (i32.const 1248) "out-link/leaked")       ;; s23 15
    (data (i32.const 1280) "inner.txt")             ;; s24 9
    (data (i32.const 1312) "inner-hard")            ;; s25 10
    (data (i32.const 1344) "sub/hard")              ;; s26 8
    (data (i32.const 1376) "other.txt")             ;; s27 9
    (data (i32.const 1408) "lhard")                 ;; s28 5
    (data (i32.const 1440) ".")                     ;; s29 1
    (data (i32.const 1472) "l")                     ;; s30 1
    (data (i32.const 3072) "ABCDEFGHIJKLMNO")
    (global $set (mut i32) (i32.const 0))
    ;; h: the last open's handle.
    (global $h (mut i32) (i32.const 0))
    ;; await: a handle's event payload once its operation, whose return was r, has completed. BLOCKED (-1) is
    ;; waited for on the set, the handle joined for the wait and taken out after it; anything else is the payload.
    (func $await (param $w i32) (param $r i32) (result i32)
      (if (i32.ne (local.get $r) (i32.const -1)) (then (return (local.get $r))))
      (if (i32.eqz (global.get $set)) (then (global.set $set (call $wsnew))))
      (call $wsjoin (local.get $w) (global.get $set))
      (drop (call $wswait (global.get $set) (i32.const 192)))
      (call $wsjoin (local.get $w) (i32.const 0))
      (i32.load (i32.const 196)))
    ;; settle: an async lower's subtask, waited for unless it returned RETURNED (2) inline.
    (func $settle (param $s i32)
      (if (i32.ne (i32.and (local.get $s) (i32.const 0xf)) (i32.const 2))
        (then (drop (call $await (i32.shr_u (local.get $s) (i32.const 4)) (i32.const -1))))))
    ;; res4 and res8: the result at ret, its payload at +4 or +8.
    (func $res4 (param $ret i32) (result i32)
      (if (result i32) (i32.eqz (i32.load8_u (local.get $ret)))
        (then (i32.const 0))
        (else (i32.or (i32.const 0x80) (i32.load8_u (i32.add (local.get $ret) (i32.const 4)))))))
    (func $res8 (param $ret i32) (result i32)
      (if (result i32) (i32.eqz (i32.load8_u (local.get $ret)))
        (then (i32.const 0))
        (else (i32.or (i32.const 0x80) (i32.load8_u (i32.add (local.get $ret) (i32.const 8)))))))
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
    ;; openh: hello.txt (or any path) opened symlink-follow with descriptor-flags df; the handle in $h.
    (func $openf (param $d i32) (param $p i32) (param $n i32) (param $df i32) (result i32)
      (call $open (local.get $d) (i32.const 1) (local.get $p) (local.get $n) (i32.const 0) (local.get $df)))
    (func $subdir (param $d i32) (param $df i32) (result i32)
      (call $open (local.get $d) (i32.const 1) (i32.const 1056) (i32.const 3) (i32.const 2) (local.get $df)))
    (func $stat (param $d i32) (result i32)
      (call $settle (call $statlow (local.get $d) (i32.const 64)))
      (call $res8 (i32.const 64)))
    (func $kind (param $d i32) (param $pf i32) (param $p i32) (param $n i32) (result i32) (local $r i32)
      (call $settle (call $statatlow (local.get $d) (local.get $pf) (local.get $p) (local.get $n) (i32.const 64)))
      (local.set $r (call $res8 (i32.const 64)))
      (if (result i32) (i32.eqz (local.get $r))
        (then (i32.load8_u (i32.const 72)))
        (else (local.get $r))))
    (func $rl (param $d i32) (param $p i32) (param $n i32) (result i32)
      (call $settle (call $rllow (local.get $d) (local.get $p) (local.get $n) (i32.const 64)))
      (call $res4 (i32.const 64)))
    (func $sl (param $d i32) (param $op i32) (param $on i32) (param $np i32) (param $nn i32) (result i32)
      (i32.store (i32.const 256) (local.get $d))
      (i32.store (i32.const 260) (local.get $op))
      (i32.store (i32.const 264) (local.get $on))
      (i32.store (i32.const 268) (local.get $np))
      (i32.store (i32.const 272) (local.get $nn))
      (call $settle (call $sllow (i32.const 256) (i32.const 64)))
      (call $res4 (i32.const 64)))
    (func $ln (param $d i32) (param $pf i32) (param $op i32) (param $on i32) (param $nd i32) (param $np i32)
      (param $nn i32) (result i32)
      (i32.store (i32.const 256) (local.get $d))
      (i32.store8 (i32.const 260) (local.get $pf))
      (i32.store (i32.const 264) (local.get $op))
      (i32.store (i32.const 268) (local.get $on))
      (i32.store (i32.const 272) (local.get $nd))
      (i32.store (i32.const 276) (local.get $np))
      (i32.store (i32.const 280) (local.get $nn))
      (call $settle (call $lnlow (i32.const 256) (i32.const 64)))
      (call $res4 (i32.const 64)))
    (func $ss (param $d i32) (param $size i64) (result i32)
      (call $settle (call $sslow (local.get $d) (local.get $size) (i32.const 64)))
      (call $res4 (i32.const 64)))
    ;; st: set-times(d, access, modification), each new-timestamp a case (0 no-change, 1 now, 2 timestamp) with its
    ;; seconds and nanoseconds, spilled to 256.
    (func $st (param $d i32) (param $ac i32) (param $as i64) (param $an i32) (param $mc i32) (param $ms i64)
      (param $mn i32) (result i32)
      (i32.store (i32.const 256) (local.get $d))
      (i32.store8 (i32.const 264) (local.get $ac))
      (i64.store (i32.const 272) (local.get $as))
      (i32.store (i32.const 280) (local.get $an))
      (i32.store8 (i32.const 288) (local.get $mc))
      (i64.store (i32.const 296) (local.get $ms))
      (i32.store (i32.const 304) (local.get $mn))
      (call $settle (call $stlow (i32.const 256) (i32.const 64)))
      (call $res4 (i32.const 64)))
    (func $sta (param $d i32) (param $pf i32) (param $p i32) (param $n i32) (param $ac i32) (param $as i64)
      (param $an i32) (param $mc i32) (param $ms i64) (param $mn i32) (result i32)
      (i32.store (i32.const 256) (local.get $d))
      (i32.store8 (i32.const 260) (local.get $pf))
      (i32.store (i32.const 264) (local.get $p))
      (i32.store (i32.const 268) (local.get $n))
      (i32.store8 (i32.const 272) (local.get $ac))
      (i64.store (i32.const 280) (local.get $as))
      (i32.store (i32.const 288) (local.get $an))
      (i32.store8 (i32.const 296) (local.get $mc))
      (i64.store (i32.const 304) (local.get $ms))
      (i32.store (i32.const 312) (local.get $mn))
      (call $settle (call $stalow (i32.const 256) (i32.const 64)))
      (call $res4 (i32.const 64)))
    (func $sy (param $d i32) (result i32)
      (call $settle (call $synclow (local.get $d) (i32.const 64)))
      (call $res4 (i32.const 64)))
    (func $sd (param $d i32) (result i32)
      (call $settle (call $sdlow (local.get $d) (i32.const 64)))
      (call $res4 (i32.const 64)))
    (func $adv (param $d i32) (param $off i64) (param $len i64) (param $a i32) (result i32)
      (call $settle (call $advlow (local.get $d) (local.get $off) (local.get $len) (local.get $a) (i32.const 64)))
      (call $res4 (i32.const 64)))
    (func $mh (param $d i32) (result i32)
      (call $settle (call $mhlow (local.get $d) (i32.const 64)))
      (call $res8 (i32.const 64)))
    (func $mha (param $d i32) (param $pf i32) (param $p i32) (param $n i32) (result i32)
      (call $settle (call $mhalow (local.get $d) (local.get $pf) (local.get $p) (local.get $n) (i32.const 64)))
      (call $res8 (i32.const 64)))
    ;; save copies the last hash to 224; eqh is whether the last hash equals the saved one.
    (func $save
      (i64.store (i32.const 224) (i64.load (i32.const 72)))
      (i64.store (i32.const 232) (i64.load (i32.const 80))))
    (func $eqh (result i32)
      (i32.and (i64.eq (i64.load (i32.const 224)) (i64.load (i32.const 72)))
        (i64.eq (i64.load (i32.const 232)) (i64.load (i32.const 80)))))
    (func $same (param $d i32) (param $o i32) (result i32)
      (call $settle (call $samelow (local.get $d) (local.get $o) (i32.const 64)))
      (i32.load8_u (i32.const 64)))
    ;; read: one async stream.read of up to n bytes into 2560 + at, its payload.
    (func $read (param $s i32) (param $at i32) (param $n i32) (result i32)
      (call $await (local.get $s)
        (call $sread (local.get $s) (i32.add (i32.const 2560) (local.get $at)) (local.get $n))))
    (func $future (param $f i32) (result i32)
      (drop (call $await (local.get $f) (call $fread (local.get $f) (i32.const 208))))
      (call $fdropr (local.get $f))
      (call $res4 (i32.const 208)))
    ;; slurp: read-via-stream(d, off) to its end into 2560; the future's byte, and the count read in $h.
    (func $slurp (param $d i32) (param $off i64) (result i32) (local $s i32) (local $r i32) (local $n i32)
      (call $rvs (local.get $d) (local.get $off) (i32.const 200))
      (local.set $s (i32.load (i32.const 200)))
      (block $done
        (loop $more
          (local.set $r (call $read (local.get $s) (local.get $n) (i32.const 64)))
          (local.set $n (i32.add (local.get $n) (i32.shr_u (local.get $r) (i32.const 4))))
          (br_if $done (i32.ne (i32.and (local.get $r) (i32.const 0xf)) (i32.const 0)))
          (br $more)))
      (call $sdropr (local.get $s))
      (global.set $h (local.get $n))
      (call $future (i32.load (i32.const 204))))
    ;; write: write-via-stream(d, off) of n bytes from 3072, then the writer's drop: the write's payload, and the
    ;; future's byte in the next byte. append is the same through append-via-stream.
    (func $write (param $d i32) (param $off i64) (param $n i32) (result i32) (local $p i64) (local $w i32)
      (local $f i32) (local $r i32)
      (local.set $p (call $snew))
      (local.set $w (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
      (local.set $f (call $wvs (local.get $d) (i32.wrap_i64 (local.get $p)) (local.get $off)))
      (local.set $r (call $await (local.get $w) (call $swrite (local.get $w) (i32.const 3072) (local.get $n))))
      (call $sdropw (local.get $w))
      (i32.or (i32.and (local.get $r) (i32.const 0xff)) (i32.shl (call $future (local.get $f)) (i32.const 8))))
    (func $append (param $d i32) (param $src i32) (param $n i32) (result i32) (local $p i64) (local $w i32)
      (local $f i32) (local $r i32)
      (local.set $p (call $snew))
      (local.set $w (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
      (local.set $f (call $avs (local.get $d) (i32.wrap_i64 (local.get $p))))
      (local.set $r (call $await (local.get $w) (call $swrite (local.get $w) (local.get $src) (local.get $n))))
      (call $sdropw (local.get $w))
      (i32.or (i32.and (local.get $r) (i32.const 0xff)) (i32.shl (call $future (local.get $f)) (i32.const 8))))
    ;; msec and asec: the last stat's modification and access seconds; mns and ans their nanoseconds.
    (func $msec (result i64) (i64.load (i32.const 136)))
    (func $mns (result i32) (i32.load (i32.const 144)))
    (func $asec (result i64) (i64.load (i32.const 112)))
    (func $ans (result i32) (i32.load (i32.const 120)))
    (func $pack (param $b0 i32) (param $b1 i32) (param $b2 i32) (param $b3 i32) (result i32)
      (i32.or (i32.or (i32.and (local.get $b0) (i32.const 0xff)) (i32.shl (i32.and (local.get $b1) (i32.const 0xff)) (i32.const 8)))
        (i32.or (i32.shl (i32.and (local.get $b2) (i32.const 0xff)) (i32.const 16)) (i32.shl (local.get $b3) (i32.const 24)))))
    (func (export "run") (param $row i32) (result i32) (local $d i32) (local $b i32) (local $s i32) (local $f i32)
      (local $x i32) (local $y i32) (local $z i32) (local $w i32)
      (call $dirs)
      (local.set $d (call $dir))
      (local.set $b (call $dir2))
      ;; 0 readlink: readlink-at in-link: the result, the string's length, its first byte and its last.
      (if (i32.eq (local.get $row) (i32.const 0)) (then
        (local.set $x (call $rl (local.get $d) (i32.const 544) (i32.const 7)))
        (return (call $pack (local.get $x) (i32.load (i32.const 72)) (i32.load8_u (i32.load (i32.const 68)))
          (i32.load8_u (i32.add (i32.load (i32.const 68)) (i32.sub (i32.load (i32.const 72)) (i32.const 1))))))))
      ;; 1 readlink-kinds: dir-link's result and length; rel-out's result and length.
      (if (i32.eq (local.get $row) (i32.const 1)) (then
        (local.set $x (call $rl (local.get $d) (i32.const 576) (i32.const 8)))
        (local.set $y (i32.load (i32.const 72)))
        (local.set $z (call $rl (local.get $d) (i32.const 608) (i32.const 7)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z) (i32.load (i32.const 72))))))
      ;; 2 readlink-errors: out-link (an absolute target), hello.txt (not a link), missing, ../x.
      (if (i32.eq (local.get $row) (i32.const 2)) (then
        (return (call $pack (call $rl (local.get $d) (i32.const 640) (i32.const 8))
          (call $rl (local.get $d) (i32.const 512) (i32.const 9))
          (call $rl (local.get $d) (i32.const 672) (i32.const 7))
          (call $rl (local.get $d) (i32.const 704) (i32.const 4))))))
      ;; 3 symlink: newlink -> hello.txt; newlink's kind followed and its size; readlink-at newlink's length.
      (if (i32.eq (local.get $row) (i32.const 3)) (then
        (local.set $x (call $sl (local.get $d) (i32.const 512) (i32.const 9) (i32.const 768) (i32.const 7)))
        (local.set $y (call $kind (local.get $d) (i32.const 1) (i32.const 768) (i32.const 7)))
        (local.set $z (i32.load (i32.const 96)))
        (drop (call $rl (local.get $d) (i32.const 768) (i32.const 7)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z) (i32.load (i32.const 72))))))
      ;; 4 symlink-errors: /x as the target; x at in-link (which exists); x at ../x; x at missing/x.
      (if (i32.eq (local.get $row) (i32.const 4)) (then
        (return (call $pack (call $sl (local.get $d) (i32.const 800) (i32.const 2) (i32.const 832) (i32.const 1))
          (call $sl (local.get $d) (i32.const 832) (i32.const 1) (i32.const 544) (i32.const 7))
          (call $sl (local.get $d) (i32.const 832) (i32.const 1) (i32.const 704) (i32.const 4))
          (call $sl (local.get $d) (i32.const 832) (i32.const 1) (i32.const 864) (i32.const 9))))))
      ;; 5 symlink-escape-target: esc -> ../outside; esc's kind unfollowed; esc/victim.txt's kind followed; readlink-at esc.
      (if (i32.eq (local.get $row) (i32.const 5)) (then
        (local.set $x (call $sl (local.get $d) (i32.const 896) (i32.const 10) (i32.const 928) (i32.const 3)))
        (local.set $y (call $kind (local.get $d) (i32.const 0) (i32.const 928) (i32.const 3)))
        (local.set $z (call $kind (local.get $d) (i32.const 1) (i32.const 960) (i32.const 14)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z)
          (call $rl (local.get $d) (i32.const 928) (i32.const 3))))))
      ;; 6 link: hard -> hello.txt; hard's kind; its link-count; its size.
      (if (i32.eq (local.get $row) (i32.const 6)) (then
        (local.set $x (call $ln (local.get $d) (i32.const 0) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 992) (i32.const 4)))
        (local.set $y (call $kind (local.get $d) (i32.const 0) (i32.const 992) (i32.const 4)))
        (return (call $pack (local.get $x) (local.get $y) (i32.load (i32.const 88)) (i32.load (i32.const 96))))))
      ;; 7 link-errors: missing; hello.txt at w.txt (which exists); sub (a directory); in-link with symlink-follow.
      (if (i32.eq (local.get $row) (i32.const 7)) (then
        (return (call $pack
          (call $ln (local.get $d) (i32.const 0) (i32.const 672) (i32.const 7) (local.get $d) (i32.const 832) (i32.const 1))
          (call $ln (local.get $d) (i32.const 0) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 1024) (i32.const 5))
          (call $ln (local.get $d) (i32.const 0) (i32.const 1056) (i32.const 3) (local.get $d) (i32.const 1088) (i32.const 7))
          (call $ln (local.get $d) (i32.const 1) (i32.const 544) (i32.const 7) (local.get $d) (i32.const 832) (i32.const 1))))))
      ;; 8 link-escapes: ../outside/victim.txt to stolen; hello.txt to ../outside/leaked; out-link/victim.txt to stolen;
      ;; hello.txt to out-link/leaked.
      (if (i32.eq (local.get $row) (i32.const 8)) (then
        (return (call $pack
          (call $ln (local.get $d) (i32.const 0) (i32.const 1120) (i32.const 21) (local.get $d) (i32.const 1152) (i32.const 6))
          (call $ln (local.get $d) (i32.const 0) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 1184) (i32.const 17))
          (call $ln (local.get $d) (i32.const 0) (i32.const 1216) (i32.const 19) (local.get $d) (i32.const 1152) (i32.const 6))
          (call $ln (local.get $d) (i32.const 0) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 1248) (i32.const 15))))))
      ;; 9 link-two: sub opened read|mutate-directory; the grant's hello.txt to sub's hard; sub's inner.txt to the
      ;; grant's inner-hard; the grant's sub/hard kind.
      (if (i32.eq (local.get $row) (i32.const 9)) (then
        (local.set $x (call $subdir (local.get $d) (i32.const 33)))
        (local.set $s (global.get $h))
        (local.set $y (call $ln (local.get $d) (i32.const 0) (i32.const 512) (i32.const 9) (local.get $s) (i32.const 992) (i32.const 4)))
        (local.set $z (call $ln (local.get $s) (i32.const 0) (i32.const 1280) (i32.const 9) (local.get $d) (i32.const 1312) (i32.const 10)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z)
          (call $kind (local.get $d) (i32.const 0) (i32.const 1344) (i32.const 8))))))
      ;; 10 link-cross-grant: the first grant's hello.txt to the second's hard; hard's kind through the second; the
      ;; second's other.txt to the first's x; x's kind through the first.
      (if (i32.eq (local.get $row) (i32.const 10)) (then
        (local.set $x (call $ln (local.get $d) (i32.const 0) (i32.const 512) (i32.const 9) (local.get $b) (i32.const 992) (i32.const 4)))
        (local.set $y (call $kind (local.get $b) (i32.const 0) (i32.const 992) (i32.const 4)))
        (local.set $z (call $ln (local.get $b) (i32.const 0) (i32.const 1376) (i32.const 9) (local.get $d) (i32.const 832) (i32.const 1)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z)
          (call $kind (local.get $d) (i32.const 0) (i32.const 832) (i32.const 1))))))
      ;; 11 link-symlink: in-link, unfollowed, to lhard; lhard's kind unfollowed; readlink-at lhard's result and length.
      (if (i32.eq (local.get $row) (i32.const 11)) (then
        (local.set $x (call $ln (local.get $d) (i32.const 0) (i32.const 544) (i32.const 7) (local.get $d) (i32.const 1408) (i32.const 5)))
        (local.set $y (call $kind (local.get $d) (i32.const 0) (i32.const 1408) (i32.const 5)))
        (local.set $z (call $rl (local.get $d) (i32.const 1408) (i32.const 5)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z) (i32.load (i32.const 72))))))
      ;; 12 set-size: w.txt opened read|write; set-size 4 and its stat's size; set-size 20 and its stat's size.
      (if (i32.eq (local.get $row) (i32.const 12)) (then
        (drop (call $openf (local.get $d) (i32.const 1024) (i32.const 5) (i32.const 3)))
        (local.set $f (global.get $h))
        (local.set $x (call $ss (local.get $f) (i64.const 4)))
        (drop (call $stat (local.get $f)))
        (local.set $y (i32.load (i32.const 96)))
        (local.set $z (call $ss (local.get $f) (i64.const 20)))
        (drop (call $stat (local.get $f)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z) (i32.load (i32.const 96))))))
      ;; 13 set-size-errors: hello.txt opened read, set-size 4; the grant, set-size 4; hello.txt's size after; w.txt
      ;; opened read|write, set-size 2^64-1.
      (if (i32.eq (local.get $row) (i32.const 13)) (then
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (local.set $f (global.get $h))
        (local.set $x (call $ss (local.get $f) (i64.const 4)))
        (local.set $y (call $ss (local.get $d) (i64.const 4)))
        (drop (call $stat (local.get $f)))
        (local.set $z (i32.load (i32.const 96)))
        (drop (call $openf (local.get $d) (i32.const 1024) (i32.const 5) (i32.const 3)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z) (call $ss (global.get $h) (i64.const -1))))))
      ;; 14 set-times: hello.txt opened read|write; set-times of (1e9 s, 123456789 ns) and (1.5e9 s, 987654321 ns); its
      ;; stat: bit 0 the access seconds, 1 the access nanoseconds, 2 the modification seconds, 3 its nanoseconds; the
      ;; modification nanoseconds' low byte; the access instant's discriminant.
      (if (i32.eq (local.get $row) (i32.const 14)) (then
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 3)))
        (local.set $f (global.get $h))
        (local.set $x (call $st (local.get $f) (i32.const 2) (i64.const 1000000000) (i32.const 123456789)
          (i32.const 2) (i64.const 1500000000) (i32.const 987654321)))
        (drop (call $stat (local.get $f)))
        (local.set $y (i32.or (i32.or (i64.eq (call $asec) (i64.const 1000000000))
          (i32.shl (i32.eq (call $ans) (i32.const 123456789)) (i32.const 1)))
          (i32.or (i32.shl (i64.eq (call $msec) (i64.const 1500000000)) (i32.const 2))
            (i32.shl (i32.eq (call $mns) (i32.const 987654321)) (i32.const 3)))))
        (return (call $pack (local.get $x) (local.get $y) (call $mns) (i32.load8_u (i32.const 104))))))
      ;; 15 set-times-partial: hello.txt opened read|write; set-times (1e9, 1.5e9); (no-change, 1.6e9); then (now,
      ;; no-change); bit 0 access still 1e9 after the second, 1 modification 1.6e9 after it, 2 access past 1.7e9 after
      ;; the third, 3 modification still 1.6e9 after it.
      (if (i32.eq (local.get $row) (i32.const 15)) (then
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 3)))
        (local.set $f (global.get $h))
        (local.set $x (call $st (local.get $f) (i32.const 2) (i64.const 1000000000) (i32.const 0)
          (i32.const 2) (i64.const 1500000000) (i32.const 0)))
        (local.set $y (call $st (local.get $f) (i32.const 0) (i64.const 0) (i32.const 0)
          (i32.const 2) (i64.const 1600000000) (i32.const 0)))
        (drop (call $stat (local.get $f)))
        (local.set $w (i32.or (i64.eq (call $asec) (i64.const 1000000000))
          (i32.shl (i64.eq (call $msec) (i64.const 1600000000)) (i32.const 1))))
        (local.set $z (call $st (local.get $f) (i32.const 1) (i64.const 0) (i32.const 0)
          (i32.const 0) (i64.const 0) (i32.const 0)))
        (drop (call $stat (local.get $f)))
        (local.set $w (i32.or (local.get $w) (i32.or (i32.shl (i64.gt_s (call $asec) (i64.const 1700000000)) (i32.const 2))
          (i32.shl (i64.eq (call $msec) (i64.const 1600000000)) (i32.const 3)))))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z) (local.get $w)))))
      ;; 16 set-times-kinds: set-times of the grant, (1.5e9, 1.5e9); hello.txt opened read only, the same; bit 0 the
      ;; grant's modification 1.5e9, bit 1 hello.txt's.
      (if (i32.eq (local.get $row) (i32.const 16)) (then
        (local.set $x (call $st (local.get $d) (i32.const 2) (i64.const 1500000000) (i32.const 0)
          (i32.const 2) (i64.const 1500000000) (i32.const 0)))
        (drop (call $stat (local.get $d)))
        (local.set $w (i64.eq (call $msec) (i64.const 1500000000)))
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (local.set $f (global.get $h))
        (local.set $y (call $st (local.get $f) (i32.const 2) (i64.const 1500000000) (i32.const 0)
          (i32.const 2) (i64.const 1500000000) (i32.const 0)))
        (drop (call $stat (local.get $f)))
        (local.set $w (i32.or (local.get $w) (i32.shl (i64.eq (call $msec) (i64.const 1500000000)) (i32.const 1))))
        (return (call $pack (local.get $x) (local.get $y) (local.get $w) (i32.const 0)))))
      ;; 17 set-times-at: hello.txt followed, (1e9 s 5 ns, 1.5e9 s 7 ns); in-link followed, (no-change, 1.6e9); bit 0
      ;; hello.txt's modification 1.6e9, bit 1 its nanoseconds 0, bit 3 its access nanoseconds 5; hello.txt unfollowed
      ;; (a regular file), (no-change, 1.65e9); bit 2 its modification 1.65e9 after it.
      (if (i32.eq (local.get $row) (i32.const 17)) (then
        (local.set $x (call $sta (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9) (i32.const 2)
          (i64.const 1000000000) (i32.const 5) (i32.const 2) (i64.const 1500000000) (i32.const 7)))
        (local.set $y (call $sta (local.get $d) (i32.const 1) (i32.const 544) (i32.const 7) (i32.const 0)
          (i64.const 0) (i32.const 0) (i32.const 2) (i64.const 1600000000) (i32.const 0)))
        (drop (call $kind (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9)))
        (local.set $w (i32.or (i32.or (i64.eq (call $msec) (i64.const 1600000000))
          (i32.shl (i32.eqz (call $mns)) (i32.const 1)))
          (i32.shl (i32.eq (call $ans) (i32.const 5)) (i32.const 3))))
        (local.set $z (call $sta (local.get $d) (i32.const 0) (i32.const 512) (i32.const 9) (i32.const 0)
          (i64.const 0) (i32.const 0) (i32.const 2) (i64.const 1650000000) (i32.const 0)))
        (drop (call $kind (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9)))
        (local.set $w (i32.or (local.get $w) (i32.shl (i64.eq (call $msec) (i64.const 1650000000)) (i32.const 2))))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z) (local.get $w)))))
      ;; 39 set-times-at-link: hello.txt followed, (1e9, 1.6e9); in-link unfollowed, (no-change, 1.7e9); bit 0 the link's
      ;; own modification 1.7e9, bit 1 hello.txt's still 1.6e9; in-link's kind unfollowed.
      (if (i32.eq (local.get $row) (i32.const 39)) (then
        (local.set $x (call $sta (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9) (i32.const 2)
          (i64.const 1000000000) (i32.const 0) (i32.const 2) (i64.const 1600000000) (i32.const 0)))
        (local.set $y (call $sta (local.get $d) (i32.const 0) (i32.const 544) (i32.const 7) (i32.const 0)
          (i64.const 0) (i32.const 0) (i32.const 2) (i64.const 1700000000) (i32.const 0)))
        (local.set $z (call $kind (local.get $d) (i32.const 0) (i32.const 544) (i32.const 7)))
        (local.set $w (i64.eq (call $msec) (i64.const 1700000000)))
        (drop (call $kind (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9)))
        (local.set $w (i32.or (local.get $w) (i32.shl (i64.eq (call $msec) (i64.const 1600000000)) (i32.const 1))))
        (return (call $pack (local.get $x) (local.get $y) (local.get $w) (local.get $z)))))
      ;; 18 set-times-at-errors: missing, ../outside/victim.txt, out-link/victim.txt, up-link/x, each (1e9, 1e9).
      (if (i32.eq (local.get $row) (i32.const 18)) (then
        (return (call $pack
          (call $sta (local.get $d) (i32.const 1) (i32.const 672) (i32.const 7) (i32.const 2) (i64.const 1000000000)
            (i32.const 0) (i32.const 2) (i64.const 1000000000) (i32.const 0))
          (call $sta (local.get $d) (i32.const 1) (i32.const 1120) (i32.const 21) (i32.const 2) (i64.const 1000000000)
            (i32.const 0) (i32.const 2) (i64.const 1000000000) (i32.const 0))
          (call $sta (local.get $d) (i32.const 1) (i32.const 1216) (i32.const 19) (i32.const 2) (i64.const 1000000000)
            (i32.const 0) (i32.const 2) (i64.const 1000000000) (i32.const 0))
          (call $sta (local.get $d) (i32.const 1) (i32.const 736) (i32.const 9) (i32.const 2) (i64.const 1000000000)
            (i32.const 0) (i32.const 2) (i64.const 1000000000) (i32.const 0))))))
      ;; 19 timestamps: hello.txt's modification set to 1.5e9 s and 10^9 ns; to 2^62 s; to -1 s; bit 0 its modification
      ;; -1 s after the third.
      (if (i32.eq (local.get $row) (i32.const 19)) (then
        (local.set $x (call $sta (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9) (i32.const 0) (i64.const 0)
          (i32.const 0) (i32.const 2) (i64.const 1500000000) (i32.const 1000000000)))
        (local.set $y (call $sta (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9) (i32.const 0) (i64.const 0)
          (i32.const 0) (i32.const 2) (i64.const 4611686018427387904) (i32.const 0)))
        (local.set $z (call $sta (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9) (i32.const 0) (i64.const 0)
          (i32.const 0) (i32.const 2) (i64.const -1) (i32.const 0)))
        (drop (call $kind (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z) (i64.eq (call $msec) (i64.const -1))))))
      ;; 20 sync: w.txt opened read|write, sync and sync-data; hello.txt opened read, sync; the grant, sync.
      (if (i32.eq (local.get $row) (i32.const 20)) (then
        (drop (call $openf (local.get $d) (i32.const 1024) (i32.const 5) (i32.const 3)))
        (local.set $f (global.get $h))
        (local.set $x (call $sy (local.get $f)))
        (local.set $y (call $sd (local.get $f)))
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (return (call $pack (local.get $x) (local.get $y) (call $sy (global.get $h)) (call $sy (local.get $d))))))
      ;; 21 sync-more: the grant, sync-data; hello.txt opened read, sync-data; sub opened read, sync and sync-data.
      (if (i32.eq (local.get $row) (i32.const 21)) (then
        (local.set $x (call $sd (local.get $d)))
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (local.set $y (call $sd (global.get $h)))
        (drop (call $subdir (local.get $d) (i32.const 1)))
        (local.set $s (global.get $h))
        (return (call $pack (local.get $x) (local.get $y) (call $sy (local.get $s)) (call $sd (local.get $s))))))
      ;; 22 advise: hello.txt opened read: normal (0, 0), sequential (0, 11), dont-need (0, 0); the grant, normal.
      (if (i32.eq (local.get $row) (i32.const 22)) (then
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (local.set $f (global.get $h))
        (return (call $pack (call $adv (local.get $f) (i64.const 0) (i64.const 0) (i32.const 0))
          (call $adv (local.get $f) (i64.const 0) (i64.const 11) (i32.const 1))
          (call $adv (local.get $f) (i64.const 0) (i64.const 0) (i32.const 4))
          (call $adv (local.get $d) (i64.const 0) (i64.const 0) (i32.const 0))))))
      ;; 23 advise-more: hello.txt opened read: random, will-need, no-reuse, and normal at (100, 1000), past its end.
      (if (i32.eq (local.get $row) (i32.const 23)) (then
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (local.set $f (global.get $h))
        (return (call $pack (call $adv (local.get $f) (i64.const 0) (i64.const 0) (i32.const 2))
          (call $adv (local.get $f) (i64.const 0) (i64.const 0) (i32.const 3))
          (call $adv (local.get $f) (i64.const 0) (i64.const 0) (i32.const 5))
          (call $adv (local.get $f) (i64.const 100) (i64.const 1000) (i32.const 0))))))
      ;; 24 mhash-same: hello.txt opened twice; their hashes equal; the first's and metadata-hash-at hello.txt's equal;
      ;; hello.txt's and w.txt's differ; is-same-object of the two opens.
      (if (i32.eq (local.get $row) (i32.const 24)) (then
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (local.set $f (global.get $h))
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (local.set $s (global.get $h))
        (drop (call $mh (local.get $f)))
        (call $save)
        (drop (call $mh (local.get $s)))
        (local.set $x (call $eqh))
        (drop (call $mha (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9)))
        (local.set $y (call $eqh))
        (drop (call $mha (local.get $d) (i32.const 1) (i32.const 1024) (i32.const 5)))
        (local.set $z (i32.eqz (call $eqh)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z) (call $same (local.get $f) (local.get $s))))))
      ;; 25 mhash-links: hard -> hello.txt; hard's hash and hello.txt's equal; in-link followed and hello.txt's equal;
      ;; in-link unfollowed and hello.txt's differ; is-same-object of hard and hello.txt opened.
      (if (i32.eq (local.get $row) (i32.const 25)) (then
        (drop (call $ln (local.get $d) (i32.const 0) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 992) (i32.const 4)))
        (drop (call $mha (local.get $d) (i32.const 0) (i32.const 512) (i32.const 9)))
        (call $save)
        (drop (call $mha (local.get $d) (i32.const 0) (i32.const 992) (i32.const 4)))
        (local.set $x (call $eqh))
        (drop (call $mha (local.get $d) (i32.const 1) (i32.const 544) (i32.const 7)))
        (local.set $y (call $eqh))
        (drop (call $mha (local.get $d) (i32.const 0) (i32.const 544) (i32.const 7)))
        (local.set $z (i32.eqz (call $eqh)))
        (drop (call $openf (local.get $d) (i32.const 992) (i32.const 4) (i32.const 1)))
        (local.set $f (global.get $h))
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z) (call $same (local.get $f) (global.get $h))))))
      ;; 26 mhash-change: w.txt opened read|write and hashed; after a write-via-stream the hash is unchanged (bit 0);
      ;; after set-times (bit 1); after set-size 2 (bit 2); the hash's upper half differs from its lower (bit 3).
      (if (i32.eq (local.get $row) (i32.const 26)) (then
        (drop (call $openf (local.get $d) (i32.const 1024) (i32.const 5) (i32.const 3)))
        (local.set $f (global.get $h))
        (drop (call $mh (local.get $f)))
        (call $save)
        (drop (call $write (local.get $f) (i64.const 3) (i32.const 15)))
        (drop (call $mh (local.get $f)))
        (local.set $w (call $eqh))
        (drop (call $st (local.get $f) (i32.const 2) (i64.const 1000000000) (i32.const 0)
          (i32.const 2) (i64.const 1000000000) (i32.const 0)))
        (drop (call $mh (local.get $f)))
        (local.set $w (i32.or (local.get $w) (i32.shl (call $eqh) (i32.const 1))))
        (drop (call $ss (local.get $f) (i64.const 2)))
        (drop (call $mh (local.get $f)))
        (local.set $w (i32.or (local.get $w) (i32.shl (call $eqh) (i32.const 2))))
        (local.set $w (i32.or (local.get $w) (i32.shl (i64.ne (i64.load (i32.const 72)) (i64.load (i32.const 80)))
          (i32.const 3))))
        (return (call $pack (local.get $w) (i32.const 0) (i32.const 0) (i32.const 0)))))
      ;; 27 mhash-errors: metadata-hash-at missing; ../x; metadata-hash of the grant; metadata-hash-at with a file as
      ;; the base.
      (if (i32.eq (local.get $row) (i32.const 27)) (then
        (local.set $x (call $mha (local.get $d) (i32.const 0) (i32.const 672) (i32.const 7)))
        (local.set $y (call $mha (local.get $d) (i32.const 0) (i32.const 704) (i32.const 4)))
        (local.set $z (call $mh (local.get $d)))
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z)
          (call $mha (global.get $h) (i32.const 0) (i32.const 512) (i32.const 9))))))
      ;; 28 same-object: the grant and itself; the grant and "." opened from it; the grant and sub; hello.txt and w.txt.
      (if (i32.eq (local.get $row) (i32.const 28)) (then
        (local.set $x (call $same (local.get $d) (local.get $d)))
        (drop (call $open (local.get $d) (i32.const 1) (i32.const 1440) (i32.const 1) (i32.const 2) (i32.const 1)))
        (local.set $y (call $same (local.get $d) (global.get $h)))
        (drop (call $subdir (local.get $d) (i32.const 1)))
        (local.set $z (call $same (local.get $d) (global.get $h)))
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (local.set $f (global.get $h))
        (drop (call $openf (local.get $d) (i32.const 1024) (i32.const 5) (i32.const 1)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z) (call $same (local.get $f) (global.get $h))))))
      ;; 29 same-object-more: the grant and a second get-directories' grant; the grant and the second preopen; hello.txt
      ;; opened through in-link and directly; sub opened through dir-link and directly.
      (if (i32.eq (local.get $row) (i32.const 29)) (then
        (call $dirs)
        (local.set $x (call $same (local.get $d) (call $dir)))
        (local.set $y (call $same (local.get $d) (local.get $b)))
        (drop (call $openf (local.get $d) (i32.const 544) (i32.const 7) (i32.const 1)))
        (local.set $f (global.get $h))
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (local.set $z (call $same (local.get $f) (global.get $h)))
        (drop (call $open (local.get $d) (i32.const 1) (i32.const 576) (i32.const 8) (i32.const 2) (i32.const 1)))
        (local.set $f (global.get $h))
        (drop (call $subdir (local.get $d) (i32.const 1)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z) (call $same (local.get $f) (global.get $h))))))
      ;; 30 append: w.txt opened read|write; append-via-stream of "ABC", its payload and future; of "DE", its future; the
      ;; count read back.
      (if (i32.eq (local.get $row) (i32.const 30)) (then
        (drop (call $openf (local.get $d) (i32.const 1024) (i32.const 5) (i32.const 3)))
        (local.set $f (global.get $h))
        (local.set $x (call $append (local.get $f) (i32.const 3072) (i32.const 3)))
        (local.set $y (call $append (local.get $f) (i32.const 3075) (i32.const 2)))
        (drop (call $slurp (local.get $f) (i64.const 0)))
        (return (call $pack (local.get $x) (i32.shr_u (local.get $x) (i32.const 8)) (i32.shr_u (local.get $y) (i32.const 8))
          (global.get $h)))))
      ;; 31 append-content: the same two appends; bytes 10 to 13 of the read-back.
      (if (i32.eq (local.get $row) (i32.const 31)) (then
        (drop (call $openf (local.get $d) (i32.const 1024) (i32.const 5) (i32.const 3)))
        (local.set $f (global.get $h))
        (drop (call $append (local.get $f) (i32.const 3072) (i32.const 3)))
        (drop (call $append (local.get $f) (i32.const 3075) (i32.const 2)))
        (drop (call $slurp (local.get $f) (i64.const 0)))
        (return (i32.load (i32.const 2570)))))
      ;; 32 append-errors: hello.txt opened read, an append of 5: its payload and future; the grant, the same.
      (if (i32.eq (local.get $row) (i32.const 32)) (then
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (local.set $x (call $append (global.get $h) (i32.const 3072) (i32.const 5)))
        (local.set $y (call $append (local.get $d) (i32.const 3072) (i32.const 5)))
        (return (call $pack (local.get $x) (i32.shr_u (local.get $x) (i32.const 8)) (local.get $y)
          (i32.shr_u (local.get $y) (i32.const 8))))))
      ;; 33 base-file: hello.txt opened read as the base of readlink-at, symlink-at, link-at and set-times-at.
      (if (i32.eq (local.get $row) (i32.const 33)) (then
        (drop (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (local.set $f (global.get $h))
        (return (call $pack (call $rl (local.get $f) (i32.const 544) (i32.const 7))
          (call $sl (local.get $f) (i32.const 832) (i32.const 1) (i32.const 1472) (i32.const 1))
          (call $ln (local.get $f) (i32.const 0) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 832) (i32.const 1))
          (call $sta (local.get $f) (i32.const 1) (i32.const 512) (i32.const 9) (i32.const 1) (i64.const 0) (i32.const 0)
            (i32.const 1) (i64.const 0) (i32.const 0))))))
      ;; 34 child-read: sub opened read only (no mutate-directory): symlink-at l -> x from it; link-at inner.txt to
      ;; inner-hard from it; set-times-at inner.txt (now, now) from it; set-times of it (now, now).
      (if (i32.eq (local.get $row) (i32.const 34)) (then
        (drop (call $subdir (local.get $d) (i32.const 1)))
        (local.set $s (global.get $h))
        (return (call $pack (call $sl (local.get $s) (i32.const 832) (i32.const 1) (i32.const 1472) (i32.const 1))
          (call $ln (local.get $s) (i32.const 0) (i32.const 1280) (i32.const 9) (local.get $s) (i32.const 1312) (i32.const 10))
          (call $sta (local.get $s) (i32.const 1) (i32.const 1280) (i32.const 9) (i32.const 1) (i64.const 0) (i32.const 0)
            (i32.const 1) (i64.const 0) (i32.const 0))
          (call $st (local.get $s) (i32.const 1) (i64.const 0) (i32.const 0) (i32.const 1) (i64.const 0) (i32.const 0))))))
      ;; 35 read-only: on a read-only grant, symlink-at l -> x; link-at hello.txt to hard; set-times-at hello.txt (now,
      ;; now); set-times of the grant (now, now).
      (if (i32.eq (local.get $row) (i32.const 35)) (then
        (return (call $pack (call $sl (local.get $d) (i32.const 832) (i32.const 1) (i32.const 1472) (i32.const 1))
          (call $ln (local.get $d) (i32.const 0) (i32.const 512) (i32.const 9) (local.get $d) (i32.const 992) (i32.const 4))
          (call $sta (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9) (i32.const 1) (i64.const 0) (i32.const 0)
            (i32.const 1) (i64.const 0) (i32.const 0))
          (call $st (local.get $d) (i32.const 1) (i64.const 0) (i32.const 0) (i32.const 1) (i64.const 0) (i32.const 0))))))
      ;; 36 read-only-reads: on a read-only grant, readlink-at in-link; metadata-hash of the grant; is-same-object of the
      ;; grant and itself; sync of the grant.
      (if (i32.eq (local.get $row) (i32.const 36)) (then
        (return (call $pack (call $rl (local.get $d) (i32.const 544) (i32.const 7)) (call $mh (local.get $d))
          (call $same (local.get $d) (local.get $d)) (call $sy (local.get $d))))))
      ;; 37 sync-lower: readlink-at in-link through its sync lower, its result and length; is-same-object of the grant and
      ;; itself through its sync lower.
      (if (i32.eq (local.get $row) (i32.const 37)) (then
        (call $readlink-sync (local.get $d) (i32.const 544) (i32.const 7) (i32.const 64))
        (local.set $x (call $res4 (i32.const 64)))
        (return (call $pack (local.get $x) (i32.load (i32.const 72)) (call $same-sync (local.get $d) (local.get $d))
          (i32.const 0)))))
      ;; 38 advise-bad: advise with an advice case of 6, which no case names: a trap.
      (if (i32.eq (local.get $row) (i32.const 38)) (then
        (return (call $adv (local.get $d) (i64.const 0) (i64.const 0) (i32.const 6)))))
      ;; 40 read-only-file: on a read-only grant, hello.txt opened read; set-times of it (now, now); set-size of it, 0; an
      ;; append of 5 to it, its future.
      (if (i32.eq (local.get $row) (i32.const 40)) (then
        (local.set $x (call $openf (local.get $d) (i32.const 512) (i32.const 9) (i32.const 1)))
        (local.set $f (global.get $h))
        (local.set $y (call $st (local.get $f) (i32.const 1) (i64.const 0) (i32.const 0) (i32.const 1) (i64.const 0) (i32.const 0)))
        (local.set $z (call $ss (local.get $f) (i64.const 0)))
        (return (call $pack (local.get $x) (local.get $y) (local.get $z)
          (i32.shr_u (call $append (local.get $f) (i32.const 3072) (i32.const 5)) (i32.const 8))))))
      (unreachable)))
  (core instance $gi (instantiate $gm (with "" (instance (export "m" (memory $m)) (export "getdirs" (func $getdirs-low))
    (export "rvs" (func $rvs-low)) (export "wvs" (func $wvs-low)) (export "avs" (func $avs-low))
    (export "stat" (func $stat-low)) (export "statat" (func $statat-low)) (export "openat" (func $openat-low))
    (export "readlink" (func $readlink-low)) (export "symlink" (func $symlink-low)) (export "link" (func $link-low))
    (export "setsize" (func $setsize-low)) (export "settimes" (func $settimes-low))
    (export "settimesat" (func $settimesat-low)) (export "sync" (func $sync-low)) (export "syncdata" (func $syncdata-low))
    (export "advise" (func $advise-low)) (export "mhash" (func $mhash-low)) (export "mhashat" (func $mhashat-low))
    (export "same" (func $same-low)) (export "readlink-sync" (func $readlink-sync)) (export "same-sync" (func $same-sync))
    (export "sread" (func $sread)) (export "swrite" (func $swrite)) (export "snew" (func $snew))
    (export "sdropw" (func $sdropw)) (export "sdropr" (func $sdropr)) (export "fread" (func $fread))
    (export "fdropr" (func $fdropr)) (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
    (export "wswait" (func $wswait))))))
  (type $runt (func async (param "row" u32) (result u32)))
  (func (export "run") (type $runt) (canon lift (core func $gi "run"))))
