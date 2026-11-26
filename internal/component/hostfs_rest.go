// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io/fs"
	"math"
	"os"
	"runtime"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// E1d (#1374, ADR 0138): the rest of `wasi:filesystem/types@0.3`'s descriptor, `readlink-at`, `symlink-at`,
// `link-at`, `set-size`, `set-times`, `set-times-at`, `sync`, `sync-data`, `advise`, `metadata-hash`,
// `metadata-hash-at` and `is-same-object`, each an async method resolved inline (fsMethod, ADR 0133). The one stream
// method among them, `append-via-stream`, is write-via-stream's writer with its offset taken at each write
// (fsWriteViaStream). After this nothing of `descriptor` meets the refusing stub.

// labelOf is err's error-code case, or "" for none.
func labelOf(err error) string {
	if err != nil {
		return fsErrorLabel(err)
	}
	return ""
}

// readlinkAt is `readlink-at`: os.Root's Readlink, whose content is returned. **Content that is an absolute path is
// `not-permitted`**, as the WIT says; relative content is returned even when it climbs out of the grant, wasmtime's
// reading, since following it is what os.Root refuses.
func (d *fsDescriptor) readlinkAt(p string) (target, label string) {
	if l := d.checkBase(false, p); l != "" {
		return "", l
	}
	// NEUTER, run: os.Readlink of d.dir.Name()+"/"+p instead, path arithmetic for os.Root, and
	// TestFSRestMatchesWasmtime's readlink-errors row reads 0x93938b9e: ../x is looked up beside the tree, where nothing
	// is, and answers no-entry where the WIT says not-permitted.
	target, err := d.dir.Readlink(p)
	if err != nil {
		return "", fsErrorLabel(err)
	}
	// NEUTER, run: drop this refusal, and TestFSRestMatchesWasmtime's readlink-errors row reads 0x9e938b00: out-link's
	// absolute content, the host's own path to outside/, is handed to the guest.
	if fsAbsolute(target) {
		return "", "not-permitted"
	}
	return target, ""
}

// symlinkAt is `symlink-at`: os.Root's Symlink of p to target. **An absolute target is `not-permitted`**, as the WIT
// says; a relative one that climbs out is made, wasmtime's reading, since os.Root's Symlink does not validate its
// target and following it is refused.
func (d *fsDescriptor) symlinkAt(target, p string) string {
	// NEUTER, run: checkBase(false, p), and TestFSRestMatchesWasmtime's read-only row reads 0xa0a0a000 with l made on a
	// read-only grant, and child-read reads 0xa0a0a000 with sub/l made through a child opened without mutate-directory.
	if l := d.checkBase(true, p); l != "" {
		return l
	}
	// NEUTER, run: drop this refusal, and TestFSRestMatchesWasmtime's symlink-errors row reads 0x939e8600: x -> /x is
	// made, a link to an absolute path, which os.Root's Symlink does not validate.
	if fsAbsolute(target) {
		return "not-permitted"
	}
	// NEUTER, run: os.Symlink to d.dir.Name()+"/"+p instead, path arithmetic for os.Root, and TestFSRestMatchesWasmtime's
	// symlink-errors row reads 0x9300869e: the link at ../x is made beside the tree.
	return labelOf(d.dir.Symlink(target, p))
}

// linkIn is link-at within one root r: an old path that is a directory is `not-permitted`, the WIT's "if the old
// path is not a file", asked of Lstat first, and anything else is os.Root's Link, which links a symlink as itself.
func linkIn(r *os.Root, oldp, newp string) string {
	fi, err := r.Lstat(oldp)
	if err != nil {
		return fsErrorLabel(err)
	}
	// NEUTER, run: drop this check, and TestFSRestMatchesWasmtime passes on darwin: linkat of a directory is EPERM there,
	// the same not-permitted (link-errors' sub). **Vacuous where the platform's errno already says it**; the check is
	// what makes a platform with another errno for it answer alike.
	if fi.IsDir() {
		return "not-permitted"
	}
	// NEUTER, run: os.Link of r.Name()+"/"+oldp to r.Name()+"/"+newp instead, path arithmetic for os.Root, and
	// TestFSRestMatchesWasmtime's link-escapes row reads 0x869e009e, hello.txt linked out as outside/leaked (the old paths
	// that climb out are still refused, by the Lstat above); link-symlink reads 0x058b0500, darwin's link following
	// in-link to hello.txt where os.Root's links the symlink itself.
	return labelOf(r.Link(oldp, newp))
}

// setSize is `set-size`: a directory is `bad-descriptor`; a descriptor without `write` is `invalid`, ftruncate's errno
// for an fd not open for writing and wasmtime's reading, checked first so every platform answers alike; a size past
// 2^63 - 1 is `invalid`.
func (d *fsDescriptor) setSize(size uint64) string {
	switch {
	case d.file == nil:
		return "bad-descriptor"
	// NEUTER, run: drop this arm, and TestFSRestMatchesWasmtime passes on darwin: ftruncate of an fd not open for
	// writing is EINVAL there, the same invalid (set-size-errors' first byte, read-only-file's set-size). **Vacuous where
	// the platform's errno already says it**; the arm is what makes Windows, whose Truncate would be access-denied,
	// answer alike.
	case d.flags&fsFlagWrite == 0:
		return "invalid"
	case size > math.MaxInt64:
		return "invalid"
	}
	return labelOf(d.file.f.Truncate(int64(size)))
}

// setTimes is `set-times`, a zero time leaving that time unchanged. **A directory needs its own `mutate-directory`**
// and is os.Root's Chtimes("."). **A file needs its base's** (baseMutates, ADR 0138's provenance rule), since
// `mutate-directory` is never a file's, and is set through its fd at nanosecond precision (fsFileTimes).
func (d *fsDescriptor) setTimes(atime, mtime time.Time) string {
	if d.dir != nil {
		// NEUTER, run: drop this rule, and TestFSRestMatchesWasmtime's read-only row reads 0x00a0a0a0 with the grant's
		// times set on a read-only grant, and child-read reads 0x00a0a0a0 with sub's set through a child opened without
		// mutate-directory.
		if !d.mutates() {
			return "read-only"
		}
		return labelOf(d.dir.Chtimes(".", atime, mtime))
	}
	// NEUTER, run: drop this rule, and TestFSRestMatchesWasmtime's read-only-file row reads 0x828b0000, wasmtime's
	// reading on its writable grant: hello.txt, opened from a read-only grant, has its times set.
	if !d.baseMutates {
		return "read-only"
	}
	return labelOf(fsFileTimes(d.file.f, atime, mtime))
}

// setTimesAt is `set-times-at`, a zero time leaving that time unchanged: os.Root's Chtimes, at nanosecond precision.
// Following, a target swapped to a symlink mid-call has the link's times set (os.Root's documented Unix race), never
// anything outside the grant. **Not following, a final symlink is `unsupported`** (ADR 0138): os.Root has no Lchtimes.
// That is asked of Lstat first, ADR 0133's open-at shape, so a swap in the window can make an in-grant symlink be
// followed, never one outside the grant reachable.
func (d *fsDescriptor) setTimesAt(pathFlags uint32, p string, atime, mtime time.Time) string {
	// NEUTER, run: checkBase(false, p), and TestFSRestMatchesWasmtime's read-only row reads 0xa000a0a0 with hello.txt's
	// times set on a read-only grant, and child-read reads 0xa000a0a0 with sub/inner.txt's set through a child opened
	// without mutate-directory.
	if l := d.checkBase(true, p); l != "" {
		return l
	}
	if pathFlags&pathFollow == 0 {
		fi, err := d.dir.Lstat(p)
		if err != nil {
			return fsErrorLabel(err)
		}
		// NEUTER, run: drop this arm, and TestFSRestMatchesWasmtime's set-times-at-link row reads 0x04000000: the
		// unfollowed call answers ok and sets hello.txt's times through in-link, the wrong object, and the link's own
		// are untouched.
		if fi.Mode()&fs.ModeSymlink != 0 {
			return "unsupported"
		}
	}
	// NEUTER, run: os.Chtimes of d.dir.Name()+"/"+p instead, path arithmetic for os.Root, and TestFSRestMatchesWasmtime's
	// set-times-at-errors row reads 0x93000093: outside/victim.txt's times are set through ../outside/victim.txt and
	// out-link/victim.txt, and the after-check finds its modification time at 2001.
	return labelOf(d.dir.Chtimes(p, atime, mtime))
}

// sync is `sync` and `sync-data`, one call, since an fsync is at least an fdatasync. A file without `write` is ok with
// no effect, as the WIT says; a directory is an fsync of "." on darwin and linux and no effect on Windows, whose flush
// needs write access to the handle.
func (d *fsDescriptor) sync() string {
	if d.dir != nil {
		if runtime.GOOS == "windows" {
			return ""
		}
		f, err := d.dir.Open(".")
		if err != nil {
			return fsErrorLabel(err)
		}
		defer f.Close() // the probe's own handle; nobody reads its close's error
		return labelOf(f.Sync())
	}
	// NEUTER, run: drop this arm, and TestFSRestMatchesWasmtime passes on darwin, whose fsync of a read-only fd is ok
	// (sync's and sync-more's hello.txt). **Vacuous where the platform syncs a read-only fd**; the arm is the WIT's "no
	// effect", and what keeps Windows, whose flush needs write access, from failing it.
	if d.flags&fsFlagWrite == 0 {
		return ""
	}
	return labelOf(d.file.f.Sync())
}

// fsMinTime and fsMaxTime bound an instant this host sets: Go's int64-nanosecond range, 1677 to 2262, which is also
// darwin's own clamp (ADR 0138).
var (
	fsMinTime = time.Unix(0, math.MinInt64)
	fsMaxTime = time.Unix(0, math.MaxInt64)
)

// fsInstantTime is an instant's time (ADR 0138): nanoseconds of 10^9 or more carry into the seconds, darwin's reading,
// and the result is clamped to [fsMinTime, fsMaxTime], so it is never the zero time, which means "unchanged".
//
// NEUTER, run: return time.Unix(sec, ns) unclamped, and TestAnInstantIsNormalisedAndClamped fails five rows: 2^62 s
// and MaxInt64 s are far past 2262, MinInt64 s wraps to the year 292277026596, and 0001-01-01 is the zero time, read
// as "unchanged". **TestFSRestMatchesWasmtime passes under it**: its timestamps row asserts only the results, which
// darwin's own clamp keeps ok. The unit test is the witness.
func fsInstantTime(sec int64, ns uint32) time.Time {
	carry, rest := int64(ns/1e9), int64(ns%1e9)
	switch {
	case sec > fsMaxTime.Unix()-carry:
		return fsMaxTime
	case sec < fsMinTime.Unix()-carry:
		return fsMinTime
	}
	t := time.Unix(sec+carry, rest)
	switch {
	case t.After(fsMaxTime):
		return fsMaxTime
	case t.Before(fsMinTime):
		return fsMinTime
	}
	return t
}

// fsNewTime is a `new-timestamp`'s three flat words, its case and its instant's seconds and nanoseconds: `no-change`
// the zero time, `now` the call's one reading, and `timestamp` its instant. A case past `timestamp` is the guest's trap,
// the Canonical ABI's discriminant check, which the sync lower's flat parameters reach unchecked.
func fsNewTime(op string, words []interp.Value, now time.Time) (time.Time, error) {
	switch c := uint32(words[0].Bits); c {
	case 0:
		return time.Time{}, nil
	case 1:
		return now, nil
	case 2:
		return fsInstantTime(int64(words[1].Bits), uint32(words[2].Bits)), nil
	default:
		return time.Time{}, &interp.Trap{Reason: fmt.Sprintf("descriptor.%s: new-timestamp case %d is out of range", op, c)}
	}
}

// fsMetadataHash is a stat's `metadata-hash-value` (ADR 0138): SHA-256 of the device and inode numbers, wasmtime's
// inputs, as two little-endian u64s, `lower` and `upper` its first two little-endian words. It is an identity, so a
// write, set-times or set-size leaves it unchanged, and it is fixed rather than keyed, so it is stable across runs.
// **Where Go gives no device and inode (fileIdentity), it hashes the type, size and modification time**, the WIT's
// baseline, which a write changes and two files alike in all three share.
func fsMetadataHash(fi os.FileInfo) (lower, upper uint64) {
	var in []byte
	// NEUTER, run: take the fallback here too, and TestFSRestMatchesWasmtime's mhash-change row reads 0x08: the hash
	// changes at the write, the set-times and the set-size, where wasmtime's, an identity, does not. The other hash rows
	// pass under it, their files differing in type or size, so this row is the identity's witness and the reason the
	// row is not asked off darwin and linux, where the fallback is what runs.
	if dev, ino, ok := fileIdentity(fi); ok {
		in = binary.LittleEndian.AppendUint64(binary.LittleEndian.AppendUint64(nil, dev), ino)
	} else {
		in = append([]byte{byte(fi.Mode().Type() >> 24)}, binary.LittleEndian.AppendUint64(
			binary.LittleEndian.AppendUint64(nil, uint64(fi.Size())), uint64(fi.ModTime().UnixNano()))...)
	}
	sum := sha256.Sum256(in)
	return binary.LittleEndian.Uint64(sum[0:8]), binary.LittleEndian.Uint64(sum[8:16])
}

// fsMetadataHashType is `metadata-hash-value`: `record { lower: u64, upper: u64 }`.
func fsMetadataHashType() canon.Type {
	u64 := canon.Type{Kind: canon.KindU64}
	return canon.RecordType(canon.Field{Name: "lower", Type: u64}, canon.Field{Name: "upper", Type: u64})
}

// fsMetadataHashResult is a stat's `result<metadata-hash-value, error-code>`.
func fsMetadataHashResult(fi os.FileInfo, label string) (canon.Value, error) {
	mt := fsMetadataHashType()
	if label != "" {
		return fsErr(&mt, label), nil
	}
	lower, upper := fsMetadataHash(fi)
	v, err := canon.Record(mt, map[string]canon.Value{"lower": canon.U64(lower), "upper": canon.U64(upper)})
	if err != nil {
		return canon.Value{}, fmt.Errorf("component: descriptor.metadata-hash: %w", err)
	}
	return fsOK(&mt, &v), nil
}

// fsReadlinkAt is `[method]descriptor.readlink-at: async func(path: string) -> result<string, error-code>`: self and
// the path's two words. **Its string is lowered through the caller's realloc** (ADR 0138): resolved inline, the store
// is queued in the caller's own in-host window and applied by leaveHost on the caller's goroutine with no engine lock
// held, through guestHeap, under every gate; a sync lower stores it after the wait through the same heap (ADR 0126).
func (h *Host) fsReadlinkAt() asyncImplFor {
	return fsMethod("readlink-at", 3, func(c *interp.CanonCaller, _ *hostResources, d *fsDescriptor, rest []interp.Value) (canon.Value, error) {
		p, err := liftPath(c, "readlink-at", rest[0], rest[1])
		if err != nil {
			return canon.Value{}, err
		}
		st := canon.Type{Kind: canon.KindString}
		target, label := d.readlinkAt(p)
		if label != "" {
			return fsErr(&st, label), nil
		}
		v := canon.Str(target)
		return fsOK(&st, &v), nil
	})
}

// fsSymlinkAt is `[method]descriptor.symlink-at: async func(old-path: string, new-path: string) -> result<_,
// error-code>`, its five flat parameters spilled and loaded flat (ADR 0123): self, the target's two words, the new
// path's two words.
func (h *Host) fsSymlinkAt() asyncImplFor {
	return fsMethod("symlink-at", 5, func(c *interp.CanonCaller, _ *hostResources, d *fsDescriptor, rest []interp.Value) (canon.Value, error) {
		target, err := liftPath(c, "symlink-at", rest[0], rest[1])
		if err != nil {
			return canon.Value{}, err
		}
		p, err := liftPath(c, "symlink-at", rest[2], rest[3])
		if err != nil {
			return canon.Value{}, err
		}
		return unitResult(d.symlinkAt(target, p)), nil
	})
}

// fsLinkAt is `[method]descriptor.link-at: async func(old-path-flags: path-flags, old-path: string, new-descriptor:
// borrow<descriptor>, new-path: string) -> result<_, error-code>`, its seven flat parameters spilled and loaded flat:
// self, the old path's flags and two words, new-descriptor, the new path's two words. After rename-at's checks
// (checkPair), `symlink-follow` is `invalid`, wasmtime's reading, and the link is fsTwoDescriptors', as a rename is.
func (h *Host) fsLinkAt() asyncImplFor {
	return fsMethod("link-at", 7, func(c *interp.CanonCaller, res *hostResources, d *fsDescriptor, rest []interp.Value) (canon.Value, error) {
		oldPath, err := liftPath(c, "link-at", rest[1], rest[2])
		if err != nil {
			return canon.Value{}, err
		}
		newPath, err := liftPath(c, "link-at", rest[4], rest[5])
		if err != nil {
			return canon.Value{}, err
		}
		n, release, err := lendAs[*fsDescriptor](res, descriptorResource, rest[3].Int32())
		if err != nil {
			return canon.Value{}, err
		}
		defer release()
		// NEUTER, run: let checkPair's read-only through here alone, and TestFSRestMatchesWasmtime's read-only row reads
		// 0xa0a000a0 with hard linked on a read-only grant, and child-read reads 0xa0a000a0 with sub/inner-hard linked
		// through a child opened without mutate-directory.
		if l := checkPair(d, oldPath, n, newPath); l != "" {
			return unitResult(l), nil
		}
		// NEUTER, run: drop this rule, and TestFSRestMatchesWasmtime's link-errors row reads 0x009e8693: in-link with
		// symlink-follow is linked, and as itself, a second symlink x, where wasmtime refuses it invalid.
		if uint32(rest[0].Bits)&pathFollow != 0 {
			return unitResult("invalid"), nil
		}
		return unitResult(h.fsTwoDescriptors(d, oldPath, n, newPath, linkIn)), nil
	})
}

// fsSetSize is `[method]descriptor.set-size: async func(size: filesize) -> result<_, error-code>`: self and the size.
func (h *Host) fsSetSize() asyncImplFor {
	return fsMethod("set-size", 2, func(_ *interp.CanonCaller, _ *hostResources, d *fsDescriptor, rest []interp.Value) (canon.Value, error) {
		return unitResult(d.setSize(rest[0].Bits)), nil
	})
}

// fsSetTimes is `[method]descriptor.set-times: async func(data-access-timestamp: new-timestamp,
// data-modification-timestamp: new-timestamp) -> result<_, error-code>`, its seven flat parameters spilled for the
// async lower and loaded flat: self, and each new-timestamp's case, seconds and nanoseconds.
func (h *Host) fsSetTimes() asyncImplFor {
	return fsMethod("set-times", 7, func(_ *interp.CanonCaller, _ *hostResources, d *fsDescriptor, rest []interp.Value) (canon.Value, error) {
		now := time.Now()
		atime, err := fsNewTime("set-times", rest[0:3], now)
		if err != nil {
			return canon.Value{}, err
		}
		mtime, err := fsNewTime("set-times", rest[3:6], now)
		if err != nil {
			return canon.Value{}, err
		}
		return unitResult(d.setTimes(atime, mtime)), nil
	})
}

// fsSetTimesAt is `[method]descriptor.set-times-at: async func(path-flags, path: string, data-access-timestamp:
// new-timestamp, data-modification-timestamp: new-timestamp) -> result<_, error-code>`, its ten flat parameters
// spilled and loaded flat: self, the path's flags and two words, and each new-timestamp's three words.
func (h *Host) fsSetTimesAt() asyncImplFor {
	return fsMethod("set-times-at", 10, func(c *interp.CanonCaller, _ *hostResources, d *fsDescriptor, rest []interp.Value) (canon.Value, error) {
		p, err := liftPath(c, "set-times-at", rest[1], rest[2])
		if err != nil {
			return canon.Value{}, err
		}
		now := time.Now()
		atime, err := fsNewTime("set-times-at", rest[3:6], now)
		if err != nil {
			return canon.Value{}, err
		}
		mtime, err := fsNewTime("set-times-at", rest[6:9], now)
		if err != nil {
			return canon.Value{}, err
		}
		return unitResult(d.setTimesAt(uint32(rest[0].Bits), p, atime, mtime)), nil
	})
}

// fsSync is `[method]descriptor.sync` and `sync-data: async func() -> result<_, error-code>`, one implementation.
func (h *Host) fsSync(name string) asyncImplFor {
	return fsMethod(name, 1, func(_ *interp.CanonCaller, _ *hostResources, d *fsDescriptor, _ []interp.Value) (canon.Value, error) {
		return unitResult(d.sync()), nil
	})
}

// fsAdviceCases is `advice`'s case count: normal, sequential, random, will-need, dont-need and no-reuse.
const fsAdviceCases = 6

// fsAdvise is `[method]descriptor.advise: async func(offset: filesize, length: filesize, advice: advice) -> result<_,
// error-code>`: self, the offset, the length and the advice's case. On a file it is ok and changes nothing, at any
// offset and length (ADR 0138); a directory is `bad-descriptor`. **An advice past no-reuse is the guest's trap**, the
// Canonical ABI's discriminant check, which reaches the host unchecked since advise's four flat parameters never spill.
func (h *Host) fsAdvise() asyncImplFor {
	return fsMethod("advise", 4, func(_ *interp.CanonCaller, _ *hostResources, d *fsDescriptor, rest []interp.Value) (canon.Value, error) {
		// NEUTER, run: drop this trap, and TestFSRestMatchesWasmtime's advise-bad row returns 130 (0x82, bad-descriptor
		// for the grant) where wasmtime traps on the discriminant.
		if a := uint32(rest[2].Bits); a >= fsAdviceCases {
			return canon.Value{}, &interp.Trap{Reason: fmt.Sprintf("descriptor.advise: advice case %d is out of range", a)}
		}
		// NEUTER, run: drop this refusal, and TestFSRestMatchesWasmtime's advise row reads 0, the grant advised ok where
		// wasmtime answers bad-descriptor.
		if d.file == nil {
			return unitResult("bad-descriptor"), nil
		}
		return unitResult(""), nil
	})
}

// fsMetadataHashOf is `[method]descriptor.metadata-hash: async func() -> result<metadata-hash-value, error-code>`.
func (h *Host) fsMetadataHashOf() asyncImplFor {
	return fsMethod("metadata-hash", 1, func(_ *interp.CanonCaller, _ *hostResources, d *fsDescriptor, _ []interp.Value) (canon.Value, error) {
		fi, err := d.stat()
		return fsMetadataHashResult(fi, labelOf(err))
	})
}

// fsMetadataHashAt is `[method]descriptor.metadata-hash-at: async func(path-flags, path: string) ->
// result<metadata-hash-value, error-code>`: self, the path's flags and two words, through stat-at's resolution.
func (h *Host) fsMetadataHashAt() asyncImplFor {
	return fsMethod("metadata-hash-at", 4, func(c *interp.CanonCaller, _ *hostResources, d *fsDescriptor, rest []interp.Value) (canon.Value, error) {
		p, err := liftPath(c, "metadata-hash-at", rest[1], rest[2])
		if err != nil {
			return canon.Value{}, err
		}
		if l := d.checkBase(false, p); l != "" {
			return fsMetadataHashResult(nil, l)
		}
		fi, label := d.statAt(uint32(rest[0].Bits), p)
		return fsMetadataHashResult(fi, label)
	})
}

// fsIsSameObject is `[method]descriptor.is-same-object: async func(other: borrow<descriptor>) -> bool`: os.SameFile of
// the two descriptors' stats, exact on every platform. A stat that fails answers false, since the result has no error
// case.
func (h *Host) fsIsSameObject() asyncImplFor {
	return fsMethod("is-same-object", 2, func(_ *interp.CanonCaller, res *hostResources, d *fsDescriptor, rest []interp.Value) (canon.Value, error) {
		o, release, err := lendAs[*fsDescriptor](res, descriptorResource, rest[0].Int32())
		if err != nil {
			return canon.Value{}, err
		}
		defer release()
		a, aerr := d.stat()
		b, berr := o.stat()
		// NEUTER, run: answer true for any two that stat, and TestFSRestMatchesWasmtime's same-object row reads
		// 0x01010101 and same-object-more 0x01010101: the grant and sub, hello.txt and w.txt, and the two grants are each
		// one object.
		return canon.Bool(aerr == nil && berr == nil && os.SameFile(a, b)), nil
	})
}
