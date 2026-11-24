;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; fs-descriptor (#1374 E1a, ADR 0133): wasi:filesystem@0.3's preopen, descriptor, open-at, stat, stat-at,
;; get-flags, get-type, read-via-stream and write-via-stream at the ABI, below any std. `run(row)` returns four
;; result bytes packed into a u32, byte 0 the first. An operation's byte is 0 for ok and 0x80 | its error-code case
;; for an error. A stream read's or write's byte is its event payload, progress << 4 | the copy result (COMPLETED 0,
;; DROPPED 1), so a read of 11 bytes is 0xb0.
;;
;; The grant is the first preopen get-directories returns. Its directory holds hello.txt ("hello world", 11 bytes),
;; w.txt ("0123456789"), sub/inner.txt ("inner"), in-link -> hello.txt (a relative symlink) and out-link -> /etc (an
;; absolute one). The harness makes the tree before each run, so a row that writes sees a fresh one.
;;
;; open-at is async-lowered with its six flat parameters spilled to the tuple at 16 (borrow @16, path-flags @20, the
;; path @24/@28, open-flags @32, descriptor-flags @33), and its result at 48 (the handle or the error case at 52).
;; Every async lower that does not return RETURNED (2) is waited for on a waitable set. stat's result is at 64, with
;; its record at 72: the type @72, link-count @88, size @96, and the three option<instant> discriminants @104, @128
;; and @152; its error case is at 72 too. read-via-stream's two handles land at 200 and 204, a read's bytes at 1024,
;; and a future's result at 208. `run` is async-typed and lifted with no async option, so it may block.
;;
;; Readings: fs-descriptor.reading (wasmtime 49.0.3, and the WIT's text for the read-only rows).
;; Assembled with `wasm-tools parse fs-descriptor.wat -o fs-descriptor.wasm` (wasm-tools 1.258.0).
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
    (type $bytes (stream u8))
    (type $r (result (error $ec2)))
    (type $fut (future $r))
    (type $rvs (tuple $bytes $fut))
    (export "[method]descriptor.read-via-stream" (func (param "self" $self) (param "offset" $fsz2) (result $rvs)))
    (export "[method]descriptor.write-via-stream"
      (func (param "self" $self) (param "data" $bytes) (param "offset" $fsz2) (result $fut)))
    (type $rf (result $df2 (error $ec2)))
    (export "[method]descriptor.get-flags" (func async (param "self" $self) (result $rf)))
    (type $rt (result $dt2 (error $ec2)))
    (export "[method]descriptor.get-type" (func async (param "self" $self) (result $rt)))
    (type $rs (result $stat2 (error $ec2)))
    (export "[method]descriptor.stat" (func async (param "self" $self) (result $rs)))
    (export "[method]descriptor.stat-at"
      (func async (param "self" $self) (param "path-flags" $pf2) (param "path" string) (result $rs)))
    (type $ro (result (own 0) (error $ec2)))
    (export "[method]descriptor.open-at"
      (func async (param "self" $self) (param "path-flags" $pf2) (param "path" string) (param "open-flags" $of2)
        (param "flags" $df2) (result $ro)))))
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
  (alias export $types "[method]descriptor.get-flags" (func $getflags))
  (alias export $types "[method]descriptor.get-type" (func $gettype))
  (alias export $types "[method]descriptor.stat" (func $stat))
  (alias export $types "[method]descriptor.stat-at" (func $statat))
  (alias export $types "[method]descriptor.open-at" (func $openat))
  (type $bytes (stream u8))
  (type $r (result (error $error-code)))
  (type $fut (future $r))
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
  (core func $rvs-low (canon lower (func $rvs) (memory $m) (realloc $realloc)))
  (core func $wvs-low (canon lower (func $wvs) (memory $m) (realloc $realloc)))
  (core func $getflags-low (canon lower (func $getflags) async (memory $m) (realloc $realloc)))
  (core func $gettype-low (canon lower (func $gettype) async (memory $m) (realloc $realloc)))
  (core func $stat-low (canon lower (func $stat) async (memory $m) (realloc $realloc)))
  (core func $statat-low (canon lower (func $statat) async (memory $m) (realloc $realloc)))
  (core func $openat-low (canon lower (func $openat) async (memory $m) (realloc $realloc)))
  ;; The sync lowers of open-at and stat (ADR 0126), for the sync-lower row.
  (core func $openat-sync (canon lower (func $openat) (memory $m) (realloc $realloc)))
  (core func $stat-sync (canon lower (func $stat) (memory $m) (realloc $realloc)))
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
    (import "" "getflags" (func $getflags (param i32 i32) (result i32)))
    (import "" "gettype" (func $gettype (param i32 i32) (result i32)))
    (import "" "stat" (func $statlow (param i32 i32) (result i32)))
    (import "" "statat" (func $statatlow (param i32 i32 i32 i32 i32) (result i32)))
    (import "" "openat" (func $openat (param i32 i32) (result i32)))
    (import "" "openat-sync" (func $openat-sync (param i32 i32 i32 i32 i32 i32 i32)))
    (import "" "stat-sync" (func $stat-sync (param i32 i32)))
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
    (data (i32.const 512) "hello.txt")
    (data (i32.const 528) "../x")
    (data (i32.const 536) "/etc")
    (data (i32.const 544) "sub/../../x")
    (data (i32.const 560) "out-link")
    (data (i32.const 572) "in-link")
    (data (i32.const 580) "sub/../hello.txt")
    (data (i32.const 600) "sub/inner.txt")
    (data (i32.const 616) "missing.txt")
    (data (i32.const 632) "hello.txt/x")
    (data (i32.const 648) "sub")
    (data (i32.const 652) "inner.txt")
    (data (i32.const 664) "../hello.txt")
    (data (i32.const 680) "w.txt")
    (data (i32.const 688) "new.txt")
    (data (i32.const 1536) "ABCDEFGHIJKLMNO")
    (global $set (mut i32) (i32.const 0))
    ;; st: the last async lower's packed status. h: the last open's handle.
    (global $st (mut i32) (i32.const 0))
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
      (global.set $st (local.get $s))
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
    ;; dir: the first preopen's descriptor.
    (func $dir (result i32)
      (call $getdirs (i32.const 0))
      (i32.load (i32.load (i32.const 0))))
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
    ;; openr: open-at with symlink-follow, no open flags and read, which is os.ReadFile's open.
    (func $openr (param $base i32) (param $p i32) (param $n i32) (result i32)
      (call $open (local.get $base) (i32.const 1) (local.get $p) (local.get $n) (i32.const 0) (i32.const 1)))
    (func $stat (param $d i32) (result i32)
      (call $settle (call $statlow (local.get $d) (i32.const 64)))
      (call $res8 (i32.const 64)))
    (func $statat (param $d i32) (param $pf i32) (param $p i32) (param $n i32) (result i32)
      (call $settle (call $statatlow (local.get $d) (local.get $pf) (local.get $p) (local.get $n) (i32.const 64)))
      (call $res8 (i32.const 64)))
    (func $flags (param $d i32) (result i32)
      (call $settle (call $getflags (local.get $d) (i32.const 64)))
      (if (result i32) (i32.eqz (i32.load8_u (i32.const 64)))
        (then (i32.load8_u (i32.const 68)))
        (else (call $res4 (i32.const 64)))))
    (func $type (param $d i32) (result i32)
      (call $settle (call $gettype (local.get $d) (i32.const 64)))
      (if (result i32) (i32.eqz (i32.load8_u (i32.const 64)))
        (then (i32.load8_u (i32.const 68)))
        (else (call $res4 (i32.const 64)))))
    ;; read: one async stream.read of up to n bytes into 1024 + at, its payload.
    (func $read (param $s i32) (param $at i32) (param $n i32) (result i32)
      (call $await (local.get $s)
        (call $sread (local.get $s) (i32.add (i32.const 1024) (local.get $at)) (local.get $n))))
    ;; future: read a future<result<_, error-code>> at 208, drop it, and its result byte.
    (func $future (param $f i32) (result i32)
      (drop (call $await (local.get $f) (call $fread (local.get $f) (i32.const 208))))
      (call $fdropr (local.get $f))
      (call $res4 (i32.const 208)))
    ;; slurp: read-via-stream(d, off) and reads of 64 until one is not COMPLETED, the bytes from 1024; the future's
    ;; result byte, and the count read in $h.
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
    ;; write: write-via-stream(d, off) of n bytes from 1536 in one stream.write, then the writer's drop. Returns
    ;; the write's payload in the low byte and the future's result byte in the next.
    (func $write (param $d i32) (param $off i64) (param $n i32) (result i32) (local $p i64) (local $w i32)
      (local $f i32) (local $r i32)
      (local.set $p (call $snew))
      (local.set $w (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
      (local.set $f (call $wvs (local.get $d) (i32.wrap_i64 (local.get $p)) (local.get $off)))
      (local.set $r (call $await (local.get $w) (call $swrite (local.get $w) (i32.const 1536) (local.get $n))))
      (call $sdropw (local.get $w))
      (i32.or (i32.and (local.get $r) (i32.const 0xff)) (i32.shl (call $future (local.get $f)) (i32.const 8))))
    (func $pack (param $b0 i32) (param $b1 i32) (param $b2 i32) (param $b3 i32) (result i32)
      (i32.or (i32.or (i32.and (local.get $b0) (i32.const 0xff)) (i32.shl (i32.and (local.get $b1) (i32.const 0xff)) (i32.const 8)))
        (i32.or (i32.shl (i32.and (local.get $b2) (i32.const 0xff)) (i32.const 16)) (i32.shl (local.get $b3) (i32.const 24)))))
    (func (export "run") (param $row i32) (result i32) (local $d i32) (local $a i32) (local $b i32) (local $c i32)
      (local $f i32)
      (local.set $d (call $dir))
      ;; 0 preopens: get-directories' count, its first name's length and first byte, and whether the handle is nonzero.
      (if (i32.eq (local.get $row) (i32.const 0)) (then
        (return (call $pack (i32.load (i32.const 4)) (i32.load (i32.add (i32.load (i32.const 0)) (i32.const 8)))
          (i32.load8_u (i32.load (i32.add (i32.load (i32.const 0)) (i32.const 4)))) (i32.ne (local.get $d) (i32.const 0))))))
      ;; 1 open-read: open hello.txt; read-via-stream(0) and one read of 64; a second read; the future.
      (if (i32.eq (local.get $row) (i32.const 1)) (then
        (local.set $a (call $openr (local.get $d) (i32.const 512) (i32.const 9)))
        (call $rvs (global.get $h) (i64.const 0) (i32.const 200))
        (local.set $b (call $read (i32.load (i32.const 200)) (i32.const 0) (i32.const 64)))
        (local.set $c (call $read (i32.load (i32.const 200)) (i32.const 64) (i32.const 64)))
        (call $sdropr (i32.load (i32.const 200)))
        (return (call $pack (local.get $a) (local.get $b) (local.get $c) (call $future (i32.load (i32.const 204)))))))
      ;; 2 content: hello.txt read to its end, its first four bytes.
      (if (i32.eq (local.get $row) (i32.const 2)) (then
        (drop (call $openr (local.get $d) (i32.const 512) (i32.const 9)))
        (drop (call $slurp (global.get $h) (i64.const 0)))
        (return (i32.load (i32.const 1024)))))
      ;; 3 offset: read-via-stream(7) of hello.txt to its end, its four bytes.
      (if (i32.eq (local.get $row) (i32.const 3)) (then
        (drop (call $openr (local.get $d) (i32.const 512) (i32.const 9)))
        (drop (call $slurp (global.get $h) (i64.const 7)))
        (return (i32.load (i32.const 1024)))))
      ;; 4 stat: the async lower's status, the result, the type and the size of hello.txt's stat.
      (if (i32.eq (local.get $row) (i32.const 4)) (then
        (drop (call $openr (local.get $d) (i32.const 512) (i32.const 9)))
        (local.set $a (call $stat (global.get $h)))
        (return (call $pack (global.get $st) (local.get $a) (i32.load8_u (i32.const 72)) (i32.load (i32.const 96))))))
      ;; 5 stat-at: the same for stat-at(follow, hello.txt) on the grant.
      (if (i32.eq (local.get $row) (i32.const 5)) (then
        (local.set $a (call $statat (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9)))
        (return (call $pack (global.get $st) (local.get $a) (i32.load8_u (i32.const 72)) (i32.load (i32.const 96))))))
      ;; 6 stat-at kinds: sub's result and type; in-link unfollowed, its result and type.
      (if (i32.eq (local.get $row) (i32.const 6)) (then
        (local.set $a (call $statat (local.get $d) (i32.const 1) (i32.const 648) (i32.const 3)))
        (local.set $b (i32.load8_u (i32.const 72)))
        (local.set $c (call $statat (local.get $d) (i32.const 0) (i32.const 572) (i32.const 7)))
        (return (call $pack (local.get $a) (local.get $b) (local.get $c) (i32.load8_u (i32.const 72))))))
      ;; 7 stat-fields: hello.txt's link-count and whether each of its three timestamps is some.
      (if (i32.eq (local.get $row) (i32.const 7)) (then
        (drop (call $openr (local.get $d) (i32.const 512) (i32.const 9)))
        (drop (call $stat (global.get $h)))
        (return (call $pack (i32.load (i32.const 88)) (i32.load8_u (i32.const 104)) (i32.load8_u (i32.const 128))
          (i32.load8_u (i32.const 152))))))
      ;; 8 get-flags: the grant's flags, then hello.txt's opened read.
      (if (i32.eq (local.get $row) (i32.const 8)) (then
        (local.set $a (call $flags (local.get $d)))
        (drop (call $openr (local.get $d) (i32.const 512) (i32.const 9)))
        (return (call $pack (local.get $a) (call $flags (global.get $h)) (i32.const 0) (i32.const 0)))))
      ;; 9 get-type: the async lower's status, then the grant's type and hello.txt's.
      (if (i32.eq (local.get $row) (i32.const 9)) (then
        (local.set $a (call $type (local.get $d)))
        (local.set $b (global.get $st))
        (drop (call $openr (local.get $d) (i32.const 512) (i32.const 9)))
        (return (call $pack (local.get $b) (local.get $a) (call $type (global.get $h)) (i32.const 0)))))
      ;; 10 write-at: w.txt opened read|write; write-via-stream(3) of 15 bytes; then read back to its end: the open,
      ;; the write's payload, the future, the count read back.
      (if (i32.eq (local.get $row) (i32.const 10)) (then
        (local.set $a (call $open (local.get $d) (i32.const 1) (i32.const 680) (i32.const 5) (i32.const 0) (i32.const 3)))
        (local.set $f (global.get $h))
        (local.set $b (call $write (local.get $f) (i64.const 3) (i32.const 15)))
        (drop (call $slurp (local.get $f) (i64.const 0)))
        (return (call $pack (local.get $a) (local.get $b) (i32.shr_u (local.get $b) (i32.const 8)) (global.get $h)))))
      ;; 11 write-content: the same write; the read-back's bytes 0..3 and the future.
      (if (i32.eq (local.get $row) (i32.const 11)) (then
        (drop (call $open (local.get $d) (i32.const 1) (i32.const 680) (i32.const 5) (i32.const 0) (i32.const 3)))
        (local.set $f (global.get $h))
        (drop (call $write (local.get $f) (i64.const 3) (i32.const 15)))
        (drop (call $slurp (local.get $f) (i64.const 0)))
        (return (i32.load (i32.const 1024)))))
      ;; 12 write-read-only-fd: hello.txt opened read only; write-via-stream(0) of 5 bytes: the open, the write's
      ;; payload, the future.
      (if (i32.eq (local.get $row) (i32.const 12)) (then
        (local.set $a (call $openr (local.get $d) (i32.const 512) (i32.const 9)))
        (local.set $b (call $write (global.get $h) (i64.const 0) (i32.const 5)))
        (return (call $pack (local.get $a) (local.get $b) (i32.shr_u (local.get $b) (i32.const 8)) (i32.const 0)))))
      ;; 13 escapes: open-at of ../x, /etc, sub/../../x, and out-link followed.
      (if (i32.eq (local.get $row) (i32.const 13)) (then
        (return (call $pack (call $openr (local.get $d) (i32.const 528) (i32.const 4))
          (call $openr (local.get $d) (i32.const 536) (i32.const 4))
          (call $openr (local.get $d) (i32.const 544) (i32.const 11))
          (call $openr (local.get $d) (i32.const 560) (i32.const 8))))))
      ;; 14 symlinks: open-at out-link unfollowed; stat-at out-link unfollowed, its result and type; open-at in-link
      ;; followed.
      (if (i32.eq (local.get $row) (i32.const 14)) (then
        (local.set $a (call $open (local.get $d) (i32.const 0) (i32.const 560) (i32.const 8) (i32.const 0) (i32.const 1)))
        (local.set $b (call $statat (local.get $d) (i32.const 0) (i32.const 560) (i32.const 8)))
        (local.set $c (i32.load8_u (i32.const 72)))
        (return (call $pack (local.get $a) (local.get $b) (local.get $c)
          (call $openr (local.get $d) (i32.const 572) (i32.const 7))))))
      ;; 15 in-grant: open-at sub/../hello.txt and sub/inner.txt; stat-at in-link followed, its result and size.
      (if (i32.eq (local.get $row) (i32.const 15)) (then
        (local.set $a (call $openr (local.get $d) (i32.const 580) (i32.const 16)))
        (local.set $b (call $openr (local.get $d) (i32.const 600) (i32.const 13)))
        (local.set $c (call $statat (local.get $d) (i32.const 1) (i32.const 572) (i32.const 7)))
        (return (call $pack (local.get $a) (local.get $b) (local.get $c) (i32.load (i32.const 96))))))
      ;; 16 errors: open-at missing.txt and hello.txt/x; stat-at missing.txt; open-at hello.txt with open-flags
      ;; directory.
      (if (i32.eq (local.get $row) (i32.const 16)) (then
        (return (call $pack (call $openr (local.get $d) (i32.const 616) (i32.const 11))
          (call $openr (local.get $d) (i32.const 632) (i32.const 11))
          (call $statat (local.get $d) (i32.const 1) (i32.const 616) (i32.const 11))
          (call $open (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9) (i32.const 2) (i32.const 1))))))
      ;; 17 nested: open-at sub with open-flags directory; from it, inner.txt and ../hello.txt; sub's type.
      (if (i32.eq (local.get $row) (i32.const 17)) (then
        (local.set $a (call $open (local.get $d) (i32.const 1) (i32.const 648) (i32.const 3) (i32.const 2) (i32.const 1)))
        (local.set $f (global.get $h))
        (local.set $b (call $openr (local.get $f) (i32.const 652) (i32.const 9)))
        (local.set $c (call $openr (local.get $f) (i32.const 664) (i32.const 12)))
        (return (call $pack (local.get $a) (local.get $b) (local.get $c) (call $type (local.get $f))))))
      ;; 18 sync-lower: open-at and stat through their sync lowers: the open, the stat, its type and size.
      (if (i32.eq (local.get $row) (i32.const 18)) (then
        (call $openat-sync (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9) (i32.const 0) (i32.const 1)
          (i32.const 48))
        (local.set $a (call $res4 (i32.const 48)))
        (call $stat-sync (i32.load (i32.const 52)) (i32.const 64))
        (return (call $pack (local.get $a) (call $res8 (i32.const 64)) (i32.load8_u (i32.const 72))
          (i32.load (i32.const 96))))))
      ;; 19 create: new.txt opened create|truncate, read|write; a write of 2 bytes at 0; its stat's size.
      (if (i32.eq (local.get $row) (i32.const 19)) (then
        (local.set $a (call $open (local.get $d) (i32.const 1) (i32.const 688) (i32.const 7) (i32.const 9) (i32.const 3)))
        (local.set $f (global.get $h))
        (local.set $b (call $write (local.get $f) (i64.const 0) (i32.const 2)))
        (drop (call $stat (local.get $f)))
        (return (call $pack (local.get $a) (local.get $b) (i32.shr_u (local.get $b) (i32.const 8)) (i32.load (i32.const 96))))))
      ;; 20 read-only-opens: the grant's flags; open-at hello.txt with write, new.txt with create, sub with
      ;; mutate-directory.
      (if (i32.eq (local.get $row) (i32.const 20)) (then
        (return (call $pack (call $flags (local.get $d))
          (call $open (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9) (i32.const 0) (i32.const 3))
          (call $open (local.get $d) (i32.const 1) (i32.const 688) (i32.const 7) (i32.const 1) (i32.const 1))
          (call $open (local.get $d) (i32.const 1) (i32.const 648) (i32.const 3) (i32.const 2) (i32.const 33))))))
      ;; 21 read-only-truncate: open-at hello.txt with truncate; then hello.txt read to its end, the future and the
      ;; count.
      (if (i32.eq (local.get $row) (i32.const 21)) (then
        (local.set $a (call $open (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9) (i32.const 8) (i32.const 1)))
        (drop (call $openr (local.get $d) (i32.const 512) (i32.const 9)))
        (local.set $b (call $slurp (global.get $h) (i64.const 0)))
        (return (call $pack (local.get $a) (local.get $b) (global.get $h) (i32.const 0)))))
      (unreachable)))
  (core instance $gi (instantiate $gm (with "" (instance (export "m" (memory $m)) (export "getdirs" (func $getdirs-low))
    (export "rvs" (func $rvs-low)) (export "wvs" (func $wvs-low)) (export "getflags" (func $getflags-low))
    (export "gettype" (func $gettype-low)) (export "stat" (func $stat-low)) (export "statat" (func $statat-low))
    (export "openat" (func $openat-low)) (export "openat-sync" (func $openat-sync)) (export "stat-sync" (func $stat-sync))
    (export "sread" (func $sread)) (export "swrite" (func $swrite)) (export "snew" (func $snew))
    (export "sdropw" (func $sdropw)) (export "sdropr" (func $sdropr)) (export "fread" (func $fread))
    (export "fdropr" (func $fdropr)) (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
    (export "wswait" (func $wswait))))))
  (type $runt (func async (param "row" u32) (result u32)))
  (func (export "run") (type $runt) (canon lift (core func $gi "run"))))
