// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// E1c (#1374, ADR 0138): `wasi:filesystem/types@0.3`'s four mutations, `create-directory-at`, `unlink-file-at`,
// `remove-directory-at` and `rename-at`. Each is an async method resolved inline (fsMethod, ADR 0133) and confined
// by its descriptor's os.Root. Each is refused `read-only` on a descriptor without `mutate-directory`, before anything
// is resolved. rename-at's second descriptor resolves through fsTwoDescriptors, which E1d's link-at shares.

// mutates reports whether d may create, rename, delete or change what is beneath it: `mutate-directory` (ADR 0138).
func (d *fsDescriptor) mutates() bool { return d.flags&fsFlagMutateDir != 0 }

// checkBase is the check every path method of d makes first, in this order (ADR 0138):
//   - a base that is a file is `not-directory`, wasmtime's reading;
//   - a mutation on a base without `mutate-directory` is `read-only`, before anything is resolved, since whether the
//     call "would otherwise succeed" (the WIT's words) is known only by making it;
//   - a path the host refuses itself (pathRefusal).
func (d *fsDescriptor) checkBase(mutation bool, paths ...string) string {
	if d.dir == nil {
		return "not-directory"
	}
	if mutation && !d.mutates() {
		return "read-only"
	}
	for _, p := range paths {
		if l := pathRefusal(p); l != "" {
			return l
		}
	}
	return ""
}

// pathRefusal is the answer for a path the host refuses before os.Root is asked: an absolute one is `not-permitted`
// (ADR 0133), and **an empty one is `no-entry`**, POSIX's ENOENT for an empty pathname and wasmtime's reading.
// os.Root answers an empty path with an unexported error of its own (*"empty path"*), which would map to `other`, and
// its Chtimes takes one as the root itself.
//
// NEUTER, run: drop the empty arm, and TestFSMutationsMatchWasmtime's mkdir-errors row reads 0xa4869793, `other` for
// create-directory-at "" where wasmtime reads no-entry.
func pathRefusal(p string) string {
	switch {
	case fsAbsolute(p):
		return "not-permitted"
	case p == "":
		return "no-entry"
	}
	return ""
}

// checkPair is checkBase for a call over two descriptors, rename-at's and link-at's: both bases, then both
// `mutate-directory` flags, then both paths (ADR 0138). A rename takes an entry out of d and puts one into n, and a
// link changes the linked object's link count as well as n, so both descriptors must mutate.
func checkPair(d *fsDescriptor, oldPath string, n *fsDescriptor, newPath string) string {
	if d.dir == nil || n.dir == nil {
		return "not-directory"
	}
	// NEUTER, run: drop this rule, and TestFSMutationsMatchWasmtime's read-only row reads 0x00a0a0a0 with hello.txt
	// renamed to moved.txt on a read-only grant; child-read reads 0x00a0a0a0 with sub/inner.txt renamed out of a child
	// opened without mutate-directory; child-read-more reads 0xa0a0a000 with hello.txt renamed into it.
	if !d.mutates() || !n.mutates() {
		return "read-only"
	}
	if l := pathRefusal(oldPath); l != "" {
		return l
	}
	return pathRefusal(newPath)
}

// unitResult is a `result<_, error-code>`: ok for "", else the error-code case.
func unitResult(label string) canon.Value {
	if label == "" {
		return fsOK(nil, nil)
	}
	return fsErr(nil, label)
}

// mkdirAt is `create-directory-at`: os.Root's Mkdir, at 0777 before the umask, os.Mkdir's.
func (d *fsDescriptor) mkdirAt(p string) string {
	// NEUTER, run: checkBase(false, p), and TestFSMutationsMatchWasmtime's read-only row reads 0xa0a0a000 with newdir
	// created on a read-only grant; child-read reads 0xa0a0a000 with sub/deeper created through a child opened without
	// mutate-directory; child-read-more reads 0x869ea0a0, its ../x not-permitted and inner.txt exist.
	if l := d.checkBase(true, p); l != "" {
		return l
	}
	// NEUTER, run: os.Mkdir(d.dir.Name()+"/"+p) instead, path arithmetic for os.Root, and
	// TestFSMutationsMatchWasmtime's mkdir-escapes row reads 0x00869e00: ../x is made beside the tree and out-link/x in
	// outside/, up-link/x then exists, and only /x is refused, by pathRefusal; mkdir-nested's ../x from sub is made in
	// the grant (0x02000000).
	if err := d.dir.Mkdir(p, 0o777); err != nil {
		return fsErrorLabel(err)
	}
	return ""
}

// unlinkAt is `unlink-file-at`: os.Root's Remove of anything that is not a directory, a symlink being removed as
// itself. **A directory is asked about first, and is `not-permitted`** (ADR 0138), POSIX's errno and darwin's and
// wasmtime's reading, on every platform. The check is needed, not chosen: Remove removes an empty directory too. A
// swap in the window between the two acts on something else inside the grant, never outside it.
func (d *fsDescriptor) unlinkAt(p string) string {
	// NEUTER, run: checkBase(false, p), and TestFSMutationsMatchWasmtime's read-only row reads 0xa0a000a0 with hello.txt
	// unlinked from a read-only grant; child-read reads 0xa09300a0 with sub/inner.txt unlinked through a child opened
	// without mutate-directory, and missing.txt no-entry.
	if l := d.checkBase(true, p); l != "" {
		return l
	}
	// NEUTER, run: os.Lstat and os.Remove of d.dir.Name()+"/"+p instead, path arithmetic for os.Root, and
	// TestFSMutationsMatchWasmtime's unlink-escapes row reads 0x93939e00: outside/victim.txt is unlinked through
	// ../outside/victim.txt, and the two later paths to it find it gone.
	fi, err := d.dir.Lstat(p)
	if err != nil {
		return fsErrorLabel(err)
	}
	// NEUTER, run: drop this arm, and TestFSMutationsMatchWasmtime's unlink-kinds row reads 0x97000098: empty is
	// removed, since Remove takes an empty directory, and sub answers not-empty, Remove's rmdir errno, where wasmtime
	// reads not-permitted for both.
	if fi.IsDir() {
		return "not-permitted"
	}
	if err := d.dir.Remove(p); err != nil {
		return fsErrorLabel(err)
	}
	return ""
}

// rmdirAt is `remove-directory-at`: os.Root's Remove of a directory. **Anything else, a symlink to a directory
// included, is `not-directory`**, rmdir's ENOTDIR, asked of Lstat first because Remove removes a file too (ADR 0138).
// A directory is then removed, and the platform answers `not-empty`, and `invalid` for ".".
func (d *fsDescriptor) rmdirAt(p string) string {
	// NEUTER, run: checkBase(false, p), and TestFSMutationsMatchWasmtime's read-only row reads 0xa000a0a0 with empty
	// removed from a read-only grant; child-read-more reads 0xa0a093a0, missing no-entry through the read-only child.
	if l := d.checkBase(true, p); l != "" {
		return l
	}
	// NEUTER, run: os.Lstat and os.Remove of d.dir.Name()+"/"+p instead, path arithmetic for os.Root, and
	// TestFSMutationsMatchWasmtime's rmdir-escapes row reads 0x93939e00: outside/victim is removed through
	// ../outside/victim, and the two later paths to it find it gone.
	fi, err := d.dir.Lstat(p)
	if err != nil {
		return fsErrorLabel(err)
	}
	// NEUTER, run: drop this arm, and TestFSMutationsMatchWasmtime's rmdir-kinds row reads 0x008b0000: hello.txt,
	// dir-link and sub/inner.txt are removed, two files and a link, by the call that removes directories, and only "."
	// is refused (invalid).
	if !fi.IsDir() {
		return "not-directory"
	}
	if err := d.dir.Remove(p); err != nil {
		return fsErrorLabel(err)
	}
	return ""
}

// renameIn is rename-at within one root r (ADR 0138). **A destination that is an existing directory is answered as
// POSIX's rename answers it**, since os.Root's Rename refuses one with EEXIST, as os.Rename does: onto itself ok with
// no effect, a file onto it `is-directory`, a directory onto a non-empty one `not-empty`, and a directory onto an empty
// one `exist`, where POSIX would replace it (ADR 0138's fifth departure). Onto anything else it is Rename, whose
// renameat answers as the platform does.
//
// NEUTER, run: skip this check, and TestFSMutationsMatchWasmtime's rename-over row reads 0x97868600, empty onto full
// and hello.txt onto sub both exist, Rename's own refusal. rename-dir-over-empty passes under it: its exist is
// Rename's refusal too, so this check is what chooses the labels and not what refuses the replacement.
func renameIn(r *os.Root, oldp, newp string) string {
	if nfi, err := r.Lstat(newp); err == nil && nfi.IsDir() {
		ofi, oerr := r.Lstat(oldp)
		if oerr != nil {
			return fsErrorLabel(oerr)
		}
		switch {
		case os.SameFile(ofi, nfi):
			return ""
		case !ofi.IsDir():
			return "is-directory"
		}
		empty, eerr := dirEmpty(r, newp)
		switch {
		case eerr != nil:
			return fsErrorLabel(eerr)
		case !empty:
			return "not-empty"
		default:
			return "exist"
		}
	}
	// NEUTER, run: os.Rename of r.Name()+"/"+oldp to r.Name()+"/"+newp instead, path arithmetic for os.Root, and
	// TestFSMutationsMatchWasmtime's rename-escapes row reads 0x9e930000, outside/victim.txt taken in as stolen and
	// hello.txt put out as outside/leaked; rename-nested-escape reads 0x93000000, sub's ../hello.txt and its inner.txt
	// at ../x each moved past sub.
	if err := r.Rename(oldp, newp); err != nil {
		return fsErrorLabel(err)
	}
	return ""
}

// dirEmpty reports whether the directory p beneath r has no entries.
func dirEmpty(r *os.Root, p string) (bool, error) {
	f, err := r.Open(p)
	if err != nil {
		return false, err
	}
	defer f.Close() // a read-only probe's close; nobody reads its error
	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}

// fsTwoDescriptors runs op, a rename or a link, over oldPath beneath d and newPath beneath n (ADR 0138):
//   - **the same descriptor:** d's own root;
//   - **two grants: `cross-device`,** since a root cannot span two (ADR 0091's condition 1);
//   - **two descriptors of one grant:** the grant's root, opened from its preopen's host path for the call and closed
//     after it. Each path is resolved on its own descriptor's root first, so an escape from either is `not-permitted`
//     there, and each descriptor's recorded path must still name its directory (fsNamesItsDirectory), or the call is
//     `cross-device`. The paths are then joined onto the recorded ones uncleaned, so a `..` or a symlink in them is
//     walked as the descriptor's root would walk it.
//
// Between the checks and op a component of a recorded path can be swapped; the grant's root still confines, so op
// can act elsewhere inside the grant, never outside it.
func (h *Host) fsTwoDescriptors(d *fsDescriptor, oldPath string, n *fsDescriptor, newPath string,
	op func(r *os.Root, oldp, newp string) string,
) string {
	if d == n {
		return op(d.dir, oldPath, newPath)
	}
	// NEUTER, run: drop this rule, and TestATwoDescriptorCallAcrossGrantsIsCrossDevice's rename across two grants of one
	// host directory is ok, moved.txt made through the second grant. **TestFSMutationsMatchWasmtime passes under it**:
	// its two grants are two directories, so the recorded-path check below refuses the second grant's "." as not the
	// first's. The unit test is this rule's witness.
	if d.grant != n.grant || d.grant < 0 || d.grant >= len(h.Preopens) {
		return "cross-device"
	}
	// NEUTER, run: drop both checks, and TestFSMutationsMatchWasmtime's rename-nested-escape row reads 0x009e9e00 with
	// the grant's hello.txt put at sub's ../x, which the grant's root reaches and sub's does not. Drop only the first,
	// and the fixture passes (its rename from sub's ../hello.txt names sub twice, the same-descriptor arm), and
	// TestATwoDescriptorCallResolvesEachPathOnItsOwnRoot's old-path case moves hello.txt to x.
	if _, err := d.dir.Lstat(oldPath); err != nil {
		return fsErrorLabel(err)
	}
	if _, err := n.dir.Lstat(newPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fsErrorLabel(err)
	}
	g, err := os.OpenRoot(h.Preopens[d.grant].Host)
	if err != nil {
		return fsErrorLabel(err)
	}
	defer g.Close() // the call's own root; nobody reads its close's error
	// NEUTER, run: drop this check, and TestFSMutationsMatchWasmtime's rename-stale row reads 0x05939300: the rename is
	// resolved on the grant's root at the open sub's recorded path, sub, which after its rename names nothing, so it
	// answers no-entry for a directory the descriptor still holds, and would land in whatever came to be named sub.
	if !fsNamesItsDirectory(g, d) || !fsNamesItsDirectory(g, n) {
		return "cross-device"
	}
	return op(g, beneath(d.path, oldPath), beneath(n.path, newPath))
}

// fsNamesItsDirectory reports whether d's recorded path, resolved on the grant's root g, is still d's directory: a
// parent's rename, or a symlink and a `..` that made the recorded path differ from the walked one, makes it not.
func fsNamesItsDirectory(g *os.Root, d *fsDescriptor) bool {
	at, err := g.Stat(d.path)
	if err != nil {
		return false
	}
	own, err := d.dir.Stat(".")
	if err != nil {
		return false
	}
	return os.SameFile(at, own)
}

// beneath is p resolved from a descriptor whose recorded path is base, as a path from the grant: joined, not cleaned.
func beneath(base, p string) string {
	if base == "." {
		return p
	}
	return base + "/" + p
}

// fsPathMethod is an async method `(self, path: string) -> result<_, error-code>` resolved inline (fsMethod): self
// and the path's two words.
func fsPathMethod(name string, f func(d *fsDescriptor, p string) string) asyncImplFor {
	return fsMethod(name, 3, func(c *interp.CanonCaller, _ *hostResources, d *fsDescriptor, rest []interp.Value) (canon.Value, error) {
		p, err := liftPath(c, name, rest[0], rest[1])
		if err != nil {
			return canon.Value{}, err
		}
		return unitResult(f(d, p)), nil
	})
}

// fsRenameAt is `[method]descriptor.rename-at: async func(old-path: string, new-descriptor: borrow<descriptor>,
// new-path: string) -> result<_, error-code>`. Its six flat parameters spill for the async lower and load flat
// (ADR 0123): self, the old path's two words, new-descriptor, the new path's two words. new-descriptor is lent for
// the call, as self is.
func (h *Host) fsRenameAt() asyncImplFor {
	return fsMethod("rename-at", 6, func(c *interp.CanonCaller, res *hostResources, d *fsDescriptor, rest []interp.Value) (canon.Value, error) {
		oldPath, err := liftPath(c, "rename-at", rest[0], rest[1])
		if err != nil {
			return canon.Value{}, err
		}
		newPath, err := liftPath(c, "rename-at", rest[3], rest[4])
		if err != nil {
			return canon.Value{}, err
		}
		n, release, err := lendAs[*fsDescriptor](res, descriptorResource, rest[2].Int32())
		if err != nil {
			return canon.Value{}, err
		}
		defer release()
		if l := checkPair(d, oldPath, n, newPath); l != "" {
			return unitResult(l), nil
		}
		return unitResult(h.fsTwoDescriptors(d, oldPath, n, newPath, renameIn)), nil
	})
}
