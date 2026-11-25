;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; fs-readdir (#1374 E1b, ADR 0134): wasi:filesystem@0.3's `descriptor.read-directory` at the ABI, below any std: a
;; host-produced stream<directory-entry>, whose element is a record carrying a string, read by the guest's async
;; stream.read and lowered through the reader's cabi_realloc. `run(row)` returns four result bytes packed into a u32,
;; byte 0 the first. An operation's byte is 0 for ok and 0x80 | its error-code case for an error. A read's byte is its
;; payload, progress << 4 | the copy result (COMPLETED 0, DROPPED 1), and a read's immediate byte is its return's low
;; byte: the payload when it completed inline, 0xff when it returned BLOCKED.
;;
;; The grant is the first preopen get-directories returns. Its directory holds hello.txt and, per row, a directory
;; the row opens: empty/ (nothing), one/a.txt, many/{a, bb, ccc, dddd, eeeee}, three/{p, q, r},
;; kinds/{f, d/, l -> f} and names/{x, é.txt, 40 n's, 100 L's}. **No row depends on the host's directory order**: a
;; row reads a one-entry directory, or folds every entry into sums, or keys an entry by its name.
;;
;; A directory-entry is 24 bytes, aligned to 4: `type` (descriptor-type, a variant: its case @0, `other`'s
;; option<string> @4) and `name` (a string: its pointer @16, its length @20). A read's entries land at 1024. The
;; realloc is a bump allocator from 8192 that logs every call at 2048: the count @2048, then (orig-ptr, orig-size,
;; align, new-size) in 16 bytes each from 2052, the first 64 kept.
;;
;; open-at is async-lowered with its six flat parameters spilled to the tuple at 16, its result at 48 (the handle or
;; the error case at 52). read-directory's two handles land at 200 and 204, and a future's result at 208. `run` is
;; async-typed and lifted with no async option, so it may block.
;;
;; Readings: fs-readdir.reading (wasmtime 49.0.3, and the WIT's text for the read-only row).
;; Assembled with `wasm-tools parse fs-readdir.wat -o fs-readdir.wasm` (wasm-tools 1.258.0).
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
    (type $df (flags "read" "write" "file-integrity-sync" "data-integrity-sync" "requested-write-sync" "mutate-directory"))
    (export "descriptor-flags" (type $df2 (eq $df)))
    (type $pf (flags "symlink-follow"))
    (export "path-flags" (type $pf2 (eq $pf)))
    (type $of (flags "create" "directory" "exclusive" "truncate"))
    (export "open-flags" (type $of2 (eq $of)))
    (type $dt (variant (case "block-device") (case "character-device") (case "directory") (case "fifo")
      (case "symbolic-link") (case "regular-file") (case "socket") (case "other" (option string))))
    (export "descriptor-type" (type $dt2 (eq $dt)))
    (type $de (record (field "type" $dt2) (field "name" string)))
    (export "directory-entry" (type $de2 (eq $de)))
    (type $self (borrow 0))
    (type $entries (stream $de2))
    (type $r (result (error $ec2)))
    (type $fut (future $r))
    (type $rd (tuple $entries $fut))
    (export "[method]descriptor.read-directory" (func (param "self" $self) (result $rd)))
    (type $ro (result (own 0) (error $ec2)))
    (export "[method]descriptor.open-at"
      (func async (param "self" $self) (param "path-flags" $pf2) (param "path" string) (param "open-flags" $of2)
        (param "flags" $df2) (result $ro)))))
  (import "wasi:filesystem/types@0.3.0" (instance $types (type $types-t)))
  (alias export $types "descriptor" (type $descriptor))
  (alias export $types "error-code" (type $error-code))
  (alias export $types "directory-entry" (type $directory-entry))
  (type $pre-t (instance
    (alias outer 1 $descriptor (type $d))
    (export "descriptor" (type $d2 (eq $d)))
    (type $own (own $d2))
    (type $pair (tuple $own string))
    (type $list (list $pair))
    (export "get-directories" (func (result $list)))))
  (import "wasi:filesystem/preopens@0.3.0" (instance $pre (type $pre-t)))
  (alias export $pre "get-directories" (func $getdirs))
  (alias export $types "[method]descriptor.read-directory" (func $readdir))
  (alias export $types "[method]descriptor.open-at" (func $openat))
  (type $entries (stream $directory-entry))
  (type $r (result (error $error-code)))
  (type $fut (future $r))
  ;; A bump allocator from 8192 that logs each call at 2048 (the header's layout).
  (core module $mm
    (memory (export "m") 1)
    (global $heap (mut i32) (i32.const 8192))
    (func (export "realloc") (param $op i32) (param $os i32) (param $align i32) (param $size i32) (result i32)
      (local $p i32) (local $n i32) (local $at i32)
      (local.set $n (i32.load (i32.const 2048)))
      (if (i32.lt_u (local.get $n) (i32.const 64)) (then
        (local.set $at (i32.add (i32.const 2052) (i32.shl (local.get $n) (i32.const 4))))
        (i32.store (local.get $at) (local.get $op))
        (i32.store offset=4 (local.get $at) (local.get $os))
        (i32.store offset=8 (local.get $at) (local.get $align))
        (i32.store offset=12 (local.get $at) (local.get $size))))
      (i32.store (i32.const 2048) (i32.add (local.get $n) (i32.const 1)))
      (local.set $p (i32.and (i32.add (global.get $heap) (i32.sub (local.get $align) (i32.const 1)))
        (i32.sub (i32.const 0) (local.get $align))))
      (global.set $heap (i32.add (local.get $p) (local.get $size)))
      (local.get $p)))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (alias core export $mi "realloc" (core func $realloc))
  (core func $getdirs-low (canon lower (func $getdirs) (memory $m) (realloc $realloc)))
  (core func $readdir-low (canon lower (func $readdir) (memory $m) (realloc $realloc)))
  (core func $openat-low (canon lower (func $openat) async (memory $m) (realloc $realloc)))
  (core func $sread (canon stream.read $entries async (memory $m) (realloc $realloc)))
  (core func $sdropr (canon stream.drop-readable $entries))
  (core func $fread (canon future.read $fut async (memory $m) (realloc $realloc)))
  (core func $fdropr (canon future.drop-readable $fut))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $m)))
  (core module $gm
    (import "" "m" (memory 1))
    (import "" "getdirs" (func $getdirs (param i32)))
    (import "" "readdir" (func $readdirlow (param i32 i32)))
    (import "" "openat" (func $openat (param i32 i32) (result i32)))
    (import "" "sread" (func $sread (param i32 i32 i32) (result i32)))
    (import "" "sdropr" (func $sdropr (param i32)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fdropr" (func $fdropr (param i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (data (i32.const 512) "hello.txt")
    (data (i32.const 528) "empty")
    (data (i32.const 536) "one")
    (data (i32.const 544) "many")
    (data (i32.const 552) "three")
    (data (i32.const 560) "kinds")
    (data (i32.const 568) "names")
    (global $set (mut i32) (i32.const 0))
    ;; st: the last async lower's packed status. h: the last open's handle. imm: the last read's immediate return.
    (global $st (mut i32) (i32.const 0))
    (global $h (mut i32) (i32.const 0))
    (global $imm (mut i32) (i32.const 0))
    ;; The fold over every entry a drain has read: their count, the sum of their names' lengths and of their names'
    ;; bytes, whether every name lies in the realloc's heap, and the type of the entry named f, d and l.
    (global $count (mut i32) (i32.const 0))
    (global $sumlen (mut i32) (i32.const 0))
    (global $sumbytes (mut i32) (i32.const 0))
    (global $inheap (mut i32) (i32.const 1))
    (global $kf (mut i32) (i32.const 0xff))
    (global $kd (mut i32) (i32.const 0xff))
    (global $kl (mut i32) (i32.const 0xff))
    ;; await: a handle's event payload once its operation, whose return was r, has completed (fs-descriptor's).
    (func $await (param $w i32) (param $r i32) (result i32)
      (if (i32.ne (local.get $r) (i32.const -1)) (then (return (local.get $r))))
      (if (i32.eqz (global.get $set)) (then (global.set $set (call $wsnew))))
      (call $wsjoin (local.get $w) (global.get $set))
      (drop (call $wswait (global.get $set) (i32.const 192)))
      (call $wsjoin (local.get $w) (i32.const 0))
      (i32.load (i32.const 196)))
    (func $settle (param $s i32)
      (global.set $st (local.get $s))
      (if (i32.ne (i32.and (local.get $s) (i32.const 0xf)) (i32.const 2))
        (then (drop (call $await (i32.shr_u (local.get $s) (i32.const 4)) (i32.const -1))))))
    (func $res4 (param $ret i32) (result i32)
      (if (result i32) (i32.eqz (i32.load8_u (local.get $ret)))
        (then (i32.const 0))
        (else (i32.or (i32.const 0x80) (i32.load8_u (i32.add (local.get $ret) (i32.const 4)))))))
    (func $dir (result i32)
      (call $getdirs (i32.const 0))
      (i32.load (i32.load (i32.const 0))))
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
    ;; opendir: open-at(symlink-follow, p, directory, read) on base; its result byte, the handle in $h.
    (func $opendir (param $base i32) (param $p i32) (param $n i32) (result i32)
      (call $open (local.get $base) (i32.const 1) (local.get $p) (local.get $n) (i32.const 2) (i32.const 1)))
    ;; readdir: read-directory(d), its stream's handle; the future's at 204. The realloc's log is cleared first, so
    ;; it holds the stream's calls only.
    (func $readdir (param $d i32) (result i32)
      (i32.store (i32.const 2048) (i32.const 0))
      (call $readdirlow (local.get $d) (i32.const 200))
      (i32.load (i32.const 200)))
    ;; read: one async stream.read of up to n entries into 1024, its payload; its immediate return's low byte in $imm.
    (func $read (param $s i32) (param $n i32) (result i32) (local $r i32)
      (local.set $r (call $sread (local.get $s) (i32.const 1024) (local.get $n)))
      (global.set $imm (i32.and (local.get $r) (i32.const 0xff)))
      (call $await (local.get $s) (local.get $r)))
    ;; fold: the first k entries at 1024, into the fold's globals.
    (func $fold (param $k i32) (local $i i32) (local $e i32) (local $p i32) (local $n i32) (local $j i32)
      (local $c i32)
      (block $done
        (loop $each
          (br_if $done (i32.ge_u (local.get $i) (local.get $k)))
          (local.set $e (i32.add (i32.const 1024) (i32.mul (local.get $i) (i32.const 24))))
          (local.set $p (i32.load offset=16 (local.get $e)))
          (local.set $n (i32.load offset=20 (local.get $e)))
          (global.set $count (i32.add (global.get $count) (i32.const 1)))
          (global.set $sumlen (i32.add (global.get $sumlen) (local.get $n)))
          (if (i32.lt_u (local.get $p) (i32.const 8192)) (then (global.set $inheap (i32.const 0))))
          (local.set $j (i32.const 0))
          (block $bytes-done
            (loop $bytes
              (br_if $bytes-done (i32.ge_u (local.get $j) (local.get $n)))
              (global.set $sumbytes (i32.add (global.get $sumbytes)
                (i32.load8_u (i32.add (local.get $p) (local.get $j)))))
              (local.set $j (i32.add (local.get $j) (i32.const 1)))
              (br $bytes)))
          (if (i32.eq (local.get $n) (i32.const 1)) (then
            (local.set $c (i32.load8_u (local.get $p)))
            (if (i32.eq (local.get $c) (i32.const 0x66)) (then (global.set $kf (i32.load8_u (local.get $e)))))
            (if (i32.eq (local.get $c) (i32.const 0x64)) (then (global.set $kd (i32.load8_u (local.get $e)))))
            (if (i32.eq (local.get $c) (i32.const 0x6c)) (then (global.set $kl (i32.load8_u (local.get $e)))))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $each))))
    ;; drain: reads of n from s, each folded, until one is not COMPLETED or 64 have been made; the count of reads.
    ;; first is the first read's payload when the caller has made it already, or -1.
    (func $drain (param $s i32) (param $n i32) (param $first i32) (result i32) (local $r i32) (local $reads i32)
      (local.set $r (local.get $first))
      (if (i32.eq (local.get $r) (i32.const -1)) (then (local.set $r (call $read (local.get $s) (local.get $n)))))
      (block $done
        (loop $more
          (local.set $reads (i32.add (local.get $reads) (i32.const 1)))
          (call $fold (i32.shr_u (local.get $r) (i32.const 4)))
          (br_if $done (i32.ne (i32.and (local.get $r) (i32.const 0xf)) (i32.const 0)))
          (br_if $done (i32.ge_u (local.get $reads) (i32.const 64)))
          (local.set $r (call $read (local.get $s) (local.get $n)))
          (br $more)))
      (local.get $reads))
    ;; future: the read-directory's future at 204, read to 208 and dropped; its result byte.
    (func $future (result i32) (local $f i32)
      (local.set $f (i32.load (i32.const 204)))
      (drop (call $await (local.get $f) (call $fread (local.get $f) (i32.const 208))))
      (call $fdropr (local.get $f))
      (call $res4 (i32.const 208)))
    ;; close: the stream dropped, then its future's result byte.
    (func $close (param $s i32) (result i32)
      (call $sdropr (local.get $s))
      (call $future))
    (func $pack (param $b0 i32) (param $b1 i32) (param $b2 i32) (param $b3 i32) (result i32)
      (i32.or (i32.or (i32.and (local.get $b0) (i32.const 0xff)) (i32.shl (i32.and (local.get $b1) (i32.const 0xff)) (i32.const 8)))
        (i32.or (i32.shl (i32.and (local.get $b2) (i32.const 0xff)) (i32.const 16)) (i32.shl (local.get $b3) (i32.const 24)))))
    ;; the realloc log's fold: the OR of every call's align, the sum of their new sizes, and the OR of their original
    ;; pointers and sizes.
    (func $logalign (result i32) (local $i i32) (local $v i32)
      (block $done (loop $each
        (br_if $done (i32.ge_u (local.get $i) (i32.load (i32.const 2048))))
        (local.set $v (i32.or (local.get $v) (i32.load (i32.add (i32.const 2060) (i32.shl (local.get $i) (i32.const 4))))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $each)))
      (local.get $v))
    (func $logsize (result i32) (local $i i32) (local $v i32)
      (block $done (loop $each
        (br_if $done (i32.ge_u (local.get $i) (i32.load (i32.const 2048))))
        (local.set $v (i32.add (local.get $v) (i32.load (i32.add (i32.const 2064) (i32.shl (local.get $i) (i32.const 4))))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $each)))
      (local.get $v))
    (func $logorig (result i32) (local $i i32) (local $v i32) (local $at i32)
      (block $done (loop $each
        (br_if $done (i32.ge_u (local.get $i) (i32.load (i32.const 2048))))
        (local.set $at (i32.add (i32.const 2052) (i32.shl (local.get $i) (i32.const 4))))
        (local.set $v (i32.or (local.get $v) (i32.or (i32.load (local.get $at)) (i32.load offset=4 (local.get $at)))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $each)))
      (local.get $v))
    (func (export "run") (param $row i32) (result i32) (local $d i32) (local $s i32) (local $a i32) (local $b i32)
      (local $c i32) (local $e i32) (local $i i32) (local $reads i32)
      (local.set $d (call $dir))
      ;; 0 empty: open-at empty; one read of 8, drained; the open, the read's immediate byte and payload, the future.
      (if (i32.eq (local.get $row) (i32.const 0)) (then
        (local.set $a (call $opendir (local.get $d) (i32.const 528) (i32.const 5)))
        (local.set $s (call $readdir (global.get $h)))
        (local.set $b (call $read (local.get $s) (i32.const 8)))
        (local.set $c (global.get $imm))
        (drop (call $drain (local.get $s) (i32.const 8) (local.get $b)))
        (return (call $pack (local.get $a) (local.get $c) (local.get $b) (call $close (local.get $s))))))
      ;; 1 one: open-at one; one read of 8: its immediate byte and payload, the entry's type and its name's length.
      ;; 13 read-only-one: the same on a read-only grant.
      (if (i32.or (i32.eq (local.get $row) (i32.const 1)) (i32.eq (local.get $row) (i32.const 13))) (then
        (drop (call $opendir (local.get $d) (i32.const 536) (i32.const 3)))
        (local.set $s (call $readdir (global.get $h)))
        (local.set $b (call $read (local.get $s) (i32.const 8)))
        (local.set $a (call $pack (global.get $imm) (local.get $b) (i32.load8_u (i32.const 1024))
          (i32.load (i32.const 1044))))
        (if (i32.eqz (i32.and (local.get $b) (i32.const 0xf)))
          (then (drop (call $drain (local.get $s) (i32.const 8) (i32.const -1)))))
        (drop (call $close (local.get $s)))
        (return (local.get $a))))
      ;; 2 one-name: the same read; the first four bytes of the entry's name.
      (if (i32.eq (local.get $row) (i32.const 2)) (then
        (drop (call $opendir (local.get $d) (i32.const 536) (i32.const 3)))
        (local.set $s (call $readdir (global.get $h)))
        (local.set $b (call $read (local.get $s) (i32.const 8)))
        (local.set $a (i32.load (i32.load (i32.const 1040))))
        (if (i32.eqz (i32.and (local.get $b) (i32.const 0xf)))
          (then (drop (call $drain (local.get $s) (i32.const 8) (i32.const -1)))))
        (drop (call $close (local.get $s)))
        (return (local.get $a))))
      ;; 3 one-drain: one drained by reads of 8: the count of entries, the count of reads, the future, the realloc's
      ;; calls.
      (if (i32.eq (local.get $row) (i32.const 3)) (then
        (drop (call $opendir (local.get $d) (i32.const 536) (i32.const 3)))
        (local.set $s (call $readdir (global.get $h)))
        (local.set $reads (call $drain (local.get $s) (i32.const 8) (i32.const -1)))
        (return (call $pack (global.get $count) (local.get $reads) (call $close (local.get $s)) (i32.load (i32.const 2048))))))
      ;; 4 many: many drained by reads of 8: the count, the sum of the names' lengths, the reads, the future.
      (if (i32.eq (local.get $row) (i32.const 4)) (then
        (drop (call $opendir (local.get $d) (i32.const 544) (i32.const 4)))
        (local.set $s (call $readdir (global.get $h)))
        (local.set $reads (call $drain (local.get $s) (i32.const 8) (i32.const -1)))
        (return (call $pack (global.get $count) (global.get $sumlen) (local.get $reads) (call $close (local.get $s))))))
      ;; 5 many-bytes: the same drain; the sum of every name's bytes.
      (if (i32.eq (local.get $row) (i32.const 5)) (then
        (drop (call $opendir (local.get $d) (i32.const 544) (i32.const 4)))
        (local.set $s (call $readdir (global.get $h)))
        (drop (call $drain (local.get $s) (i32.const 8) (i32.const -1)))
        (drop (call $close (local.get $s)))
        (return (global.get $sumbytes))))
      ;; 6 kinds: kinds drained: the types of f, d and l, and the count.
      (if (i32.eq (local.get $row) (i32.const 6)) (then
        (drop (call $opendir (local.get $d) (i32.const 560) (i32.const 5)))
        (local.set $s (call $readdir (global.get $h)))
        (drop (call $drain (local.get $s) (i32.const 8) (i32.const -1)))
        (drop (call $close (local.get $s)))
        (return (call $pack (global.get $kf) (global.get $kd) (global.get $kl) (global.get $count)))))
      ;; 7 names-realloc: names drained: the realloc's calls, the OR of their aligns, the sum of their sizes, and the
      ;; OR of their original pointers and sizes.
      (if (i32.eq (local.get $row) (i32.const 7)) (then
        (drop (call $opendir (local.get $d) (i32.const 568) (i32.const 5)))
        (local.set $s (call $readdir (global.get $h)))
        (drop (call $drain (local.get $s) (i32.const 8) (i32.const -1)))
        (local.set $a (i32.load (i32.const 2048)))
        (local.set $b (call $logalign))
        (local.set $c (call $logsize))
        (local.set $e (call $logorig))
        (drop (call $close (local.get $s)))
        (return (call $pack (local.get $a) (local.get $b) (local.get $c) (local.get $e)))))
      ;; 8 names-content: the same drain: the sum of the names' lengths, the sum of their bytes' low 16 bits, and
      ;; whether every name lies in the realloc's heap.
      (if (i32.eq (local.get $row) (i32.const 8)) (then
        (drop (call $opendir (local.get $d) (i32.const 568) (i32.const 5)))
        (local.set $s (call $readdir (global.get $h)))
        (drop (call $drain (local.get $s) (i32.const 8) (i32.const -1)))
        (drop (call $close (local.get $s)))
        (return (call $pack (global.get $sumlen) (global.get $sumbytes) (i32.shr_u (global.get $sumbytes) (i32.const 8))
          (global.get $inheap)))))
      ;; 9 one-at-a-time: three read by reads of 1, until one is not COMPLETED: the first four payloads, 0 for a read
      ;; not made.
      ;; 10 one-at-a-time-imm: the same reads' immediate bytes.
      (if (i32.or (i32.eq (local.get $row) (i32.const 9)) (i32.eq (local.get $row) (i32.const 10))) (then
        (drop (call $opendir (local.get $d) (i32.const 552) (i32.const 5)))
        (local.set $s (call $readdir (global.get $h)))
        (local.set $e (i32.const 0))
        (block $done
          (loop $more
            (local.set $b (call $read (local.get $s) (i32.const 1)))
            (if (i32.eq (local.get $row) (i32.const 9))
              (then (local.set $c (local.get $b)))
              (else (local.set $c (global.get $imm))))
            (local.set $e (i32.or (local.get $e) (i32.shl (i32.and (local.get $c) (i32.const 0xff))
              (i32.shl (local.get $i) (i32.const 3)))))
            (local.set $i (i32.add (local.get $i) (i32.const 1)))
            (br_if $done (i32.ne (i32.and (local.get $b) (i32.const 0xf)) (i32.const 0)))
            (br_if $done (i32.ge_u (local.get $i) (i32.const 4)))
            (br $more)))
        (if (i32.eqz (i32.and (local.get $b) (i32.const 0xf)))
          (then (drop (call $drain (local.get $s) (i32.const 1) (i32.const -1)))))
        (drop (call $close (local.get $s)))
        (return (local.get $e))))
      ;; 11 batch: three by one read of 8, then a second if the first COMPLETED: each one's immediate byte and payload.
      (if (i32.eq (local.get $row) (i32.const 11)) (then
        (drop (call $opendir (local.get $d) (i32.const 552) (i32.const 5)))
        (local.set $s (call $readdir (global.get $h)))
        (local.set $a (call $read (local.get $s) (i32.const 8)))
        (local.set $b (global.get $imm))
        (if (i32.eqz (i32.and (local.get $a) (i32.const 0xf))) (then
          (local.set $c (call $read (local.get $s) (i32.const 8)))
          (local.set $e (global.get $imm))
          (if (i32.eqz (i32.and (local.get $c) (i32.const 0xf)))
            (then (drop (call $drain (local.get $s) (i32.const 8) (i32.const -1)))))))
        (drop (call $close (local.get $s)))
        (return (call $pack (local.get $b) (local.get $a) (local.get $e) (local.get $c)))))
      ;; 12 file-fd: read-directory on hello.txt opened read: the open, one read of 8's immediate byte and payload,
      ;; the future.
      (if (i32.eq (local.get $row) (i32.const 12)) (then
        (local.set $a (call $open (local.get $d) (i32.const 1) (i32.const 512) (i32.const 9) (i32.const 0) (i32.const 1)))
        (local.set $s (call $readdir (global.get $h)))
        (local.set $b (call $read (local.get $s) (i32.const 8)))
        (local.set $c (global.get $imm))
        (if (i32.eqz (i32.and (local.get $b) (i32.const 0xf)))
          (then (drop (call $drain (local.get $s) (i32.const 8) (i32.const -1)))))
        (return (call $pack (local.get $a) (local.get $c) (local.get $b) (call $close (local.get $s))))))
      ;; 14 no-read-flag: one opened with no descriptor flags at all, then row 12's bytes: the open, one read of 8's
      ;; immediate byte and payload, the future. A directory is listed whatever its flags.
      (if (i32.eq (local.get $row) (i32.const 14)) (then
        (local.set $a (call $open (local.get $d) (i32.const 1) (i32.const 536) (i32.const 3) (i32.const 2) (i32.const 0)))
        (local.set $s (call $readdir (global.get $h)))
        (local.set $b (call $read (local.get $s) (i32.const 8)))
        (local.set $c (global.get $imm))
        (if (i32.eqz (i32.and (local.get $b) (i32.const 0xf)))
          (then (drop (call $drain (local.get $s) (i32.const 8) (i32.const -1)))))
        (return (call $pack (local.get $a) (local.get $c) (local.get $b) (call $close (local.get $s))))))
      ;; 15 read-zero: three by two reads of 0, then drained: each read of 0's immediate byte and payload.
      (if (i32.eq (local.get $row) (i32.const 15)) (then
        (drop (call $opendir (local.get $d) (i32.const 552) (i32.const 5)))
        (local.set $s (call $readdir (global.get $h)))
        (local.set $a (call $read (local.get $s) (i32.const 0)))
        (local.set $b (global.get $imm))
        (local.set $c (call $read (local.get $s) (i32.const 0)))
        (local.set $e (global.get $imm))
        (drop (call $drain (local.get $s) (i32.const 8) (i32.const -1)))
        (drop (call $close (local.get $s)))
        (return (call $pack (local.get $b) (local.get $a) (local.get $e) (local.get $c)))))
      ;; 16 early-drop: three by one read of 1, then the stream dropped before its end: the read's immediate byte and
      ;; payload, and the future.
      (if (i32.eq (local.get $row) (i32.const 16)) (then
        (drop (call $opendir (local.get $d) (i32.const 552) (i32.const 5)))
        (local.set $s (call $readdir (global.get $h)))
        (local.set $b (call $read (local.get $s) (i32.const 1)))
        (local.set $c (global.get $imm))
        (return (call $pack (local.get $c) (local.get $b) (call $close (local.get $s)) (i32.const 0)))))
      (unreachable)))
  (core instance $gi (instantiate $gm (with "" (instance (export "m" (memory $m)) (export "getdirs" (func $getdirs-low))
    (export "readdir" (func $readdir-low)) (export "openat" (func $openat-low)) (export "sread" (func $sread))
    (export "sdropr" (func $sdropr)) (export "fread" (func $fread)) (export "fdropr" (func $fdropr))
    (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "wswait" (func $wswait))))))
  (type $runt (func async (param "row" u32) (result u32)))
  (func (export "run") (type $runt) (canon lift (core func $gi "run"))))
