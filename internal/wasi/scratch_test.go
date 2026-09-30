// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package wasi

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// hostFor builds a host with the given grants and returns it plus the fd of the first preopen, so a witness
// can call a preview-1 handler directly.
//
// The host goes through [newHost] rather than a composite literal, which is #815's rule: a hand-built host
// omits what the constructor sets and the symptom surfaces in another subsystem.
func hostFor(t *testing.T, preopens ...Preopen) (*host, uint32) {
	t.Helper()
	cfg := Config{Stdin: strings.NewReader(""), Stdout: &strings.Builder{}, Stderr: &strings.Builder{}, Preopens: preopens}
	h := newHost(cfg)
	if err := h.initFDs(cfg.Preopens); err != nil {
		t.Fatalf("initFDs: %v", err)
	}
	t.Cleanup(h.closeRoots)
	if len(h.preopenFDs) == 0 {
		return h, 0
	}
	return h, h.preopenFDs[0]
}

// witxNames parses every function the specification declares, which is the same derivation
// [TestSuppliedSurfaceMatchesTheSpecification] uses — *derive the domain from the space, never from the
// registry*. A witness that enumerated the refusal surface by hand would agree with itself.
func witxNames(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "preview1", "wasi_snapshot_preview1.witx"))
	if err != nil {
		t.Fatalf("read the spec: %v", err)
	}
	m := regexp.MustCompile(`\(@interface func \(export "([a-z_0-9]+)"\)`).FindAllStringSubmatch(string(b), -1)
	if len(m) < 40 {
		t.Fatalf("parsed only %d functions from the witx; the extraction is broken and a witness with an "+
			"empty domain passes by asking nothing", len(m))
	}
	out := make([]string, 0, len(m))
	for _, g := range m {
		out = append(out, g[1])
	}
	sort.Strings(out)
	return out
}

// TestWithoutAScratchGrantEveryRefusalIsUnchanged is ADR 0091's condition 3: **a run with no `--scratch` has
// exactly the filesystem behaviour it had before the write slice, errno for errno.**
//
// # Why an errno and not a boolean
//
// The write slice's whole safety claim is that it changes nothing by default. "Nothing broke" is not that
// claim: a function that started answering `ENOSYS` where it used to answer `ENOTCAPABLE` would pass a
// did-it-work check while telling a guest something false about *why* it was refused — that the engine lacks
// the function rather than that the grant lacks the right. ADR 0083 made that distinction load-bearing and
// this slice's 21 remaining deferrals rest on it, so the witness asserts the exact value.
//
// # The domain is the specification, and the expectation is derived from the imports table
//
// Names come from the committed witx. The expected errno comes from the host's own table — a name bound to
// `refuseNosys` must answer `ENOSYS`, and the four functions this slice implemented must answer
// `ENOTCAPABLE` under a read-only grant. So adding a fifth implemented function without granting it a
// scratch check fails here, and the witness cannot drift from what the host actually binds.
//
// Handlers are called with a **read-only preopen fd and zero arguments**, which is sufficient by
// construction: every one of them decides the capability question before it reads guest memory, so there is
// no path or iovec for a zero argument to corrupt. That ordering is itself a property worth pinning, and the
// `ENOTCAPABLE` arms pin it — a handler that read memory first would fault or answer a memory errno instead.
func TestWithoutAScratchGrantEveryRefusalIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	h, fd := hostFor(t, Preopen{Host: dir, Guest: "/d"}) // NO Writable: this is the default-path witness

	// The four this slice implemented, plus path_open's write mode. Under a read-only grant each must say
	// the CAPABILITY is missing, not that the engine is.
	// **`fd_pwrite` is deliberately absent, and its absence is a measurement.** This arm passes a *preopen*
	// fd, and `fd_pwrite` on a preopen correctly answers `errBadf` — a directory has no absolute offset to
	// write at, which is a property of the descriptor rather than of a permission. Its capability refusal
	// needs a *file* fd from a read-only grant, so it has its own test
	// ([TestFdPwriteRefusesAnFdThatDidNotComeFromAScratchGrant]). Putting it here expecting
	// `errNotcapable` was my error, and the arm caught it.
	wantNotcapable := map[string]bool{
		"path_create_directory": true,
		"path_unlink_file":      true,
		"path_remove_directory": true,
	}

	table := h.imports0()
	var checked, refusals, notcapable int
	for _, name := range witxNames(t) {
		entry, ok := table[name]
		if !ok {
			continue // absence is TestSuppliedSurfaceMatchesTheSpecification's subject, not this one
		}
		// **The domain is the refusal surface, not the whole surface.** The first version called every
		// name the witx declares and panicked in `args_sizes_get`, which writes to guest memory through a
		// `Caller` this witness does not have. That is the right failure: an implemented function's
		// behaviour is its own test's subject, and calling one here would be asserting nothing while
		// requiring a whole instance to do it.
		refused := strings.Contains(refusalBinding(t, name), "refuseNosys")
		if !refused && !wantNotcapable[name] {
			continue
		}
		args := make([]interp.Value, len(entry.ft.Params))
		for i, p := range entry.ft.Params {
			switch p {
			case bin.I64:
				args[i] = interp.I64(0)
			case bin.F32:
				args[i] = interp.F32(0)
			case bin.F64:
				args[i] = interp.F64(0)
			default:
				args[i] = interp.I32(0)
			}
		}
		// **Every one of these takes its directory or file descriptor first**, so arg 0 is the preopen and
		// the call reaches its capability check rather than dying on the descriptor. Derived from the witx
		// shape rather than from a list of names: a leading `i32` is the fd in every `fd_`/`path_` function.
		if len(args) > 0 && (strings.HasPrefix(name, "fd_") || strings.HasPrefix(name, "path_")) {
			args[0] = interp.I32(int32(fd))
		}
		res, err := entry.fn(nil, args)
		if err != nil {
			t.Errorf("%s returned a Go error %v; a preview-1 handler reports through its errno", name, err)
			continue
		}
		if len(res) != 1 {
			continue // not an errno-returning shape
		}
		got := uint16(res[0].Int32())
		checked++
		switch {
		case wantNotcapable[name]:
			notcapable++
			if got != errNotcapable {
				t.Errorf("%s under a READ-ONLY grant returned errno %d, want errNotcapable (%d)\n"+
					"\tADR 0091 changes nothing by default. A write function must refuse a read-only "+
					"grant as a missing CAPABILITY; answering ENOSYS would tell the guest the engine "+
					"lacks the function, which after this slice is false.", name, got, errNotcapable)
			}
		case refused:
			refusals++
			if got != errNosys {
				t.Errorf("%s is bound to refuseNosys but returned errno %d, want errNosys (%d)\n"+
					"\tA deferral that stops saying ENOSYS stops being visible as a deferral.",
					name, got, errNosys)
			}
		}
	}
	if refusals < 15 || notcapable != len(wantNotcapable) {
		t.Fatalf("asserted %d refusals and %d capability refusals of %d callable names; want >=15 and %d. "+
			"A witness that checked almost nothing would pass, so the population is pinned.",
			refusals, notcapable, checked, len(wantNotcapable))
	}
	t.Logf("DEFAULT-PATH: %d name(s) answer ENOSYS unchanged, %d answer ENOTCAPABLE under a read-only grant",
		refusals, notcapable)
}

// TestAScratchGrantConfinesEveryWriteToItsRoot is ADR 0091's condition 4: the confinement probe, committed.
//
// # Why it is committed rather than a run someone did once
//
// The ADR's §4 ruling — that confinement is enforceable without granting more than the named directory, and
// therefore that this ADR stays technical rather than going to Scott — rests on exactly these readings. A
// probe that produced them and was then deleted would leave the ratification resting on a number nobody can
// re-derive, which this campaign has already paid for once (the fork's standing property 18).
//
// # Both arms, because the negative one cannot stand alone
//
// Ten escape shapes must be refused and eight in-grant operations must be permitted. **A broken or closed
// `os.Root` refuses all eighteen and would read as perfect confinement**, so the permitted arm is what makes
// the refusals a result. A post-hoc look at the filesystem closes the third gap: a refusal reported to the
// guest is not the same fact as nothing having been written.
func TestAScratchGrantConfinesEveryWriteToItsRoot(t *testing.T) {
	base := t.TempDir()
	inside := filepath.Join(base, "grant")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{inside, outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	const secret = "OUTSIDE-SECRET"
	secretPath := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secretPath, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	// Symlinks INSIDE the grant pointing out of it: the shape a textual `..` filter misses entirely.
	if err := os.Symlink(outside, filepath.Join(inside, "esc")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(inside, "abs")); err != nil {
		t.Fatal(err)
	}

	h, fd := hostFor(t, Preopen{Host: inside, Guest: "/s", Writable: true})
	root, e := h.dirRootAt(fd)
	if e != errSuccess {
		t.Fatalf("the scratch grant yielded no root (errno %d); every arm below would be vacuous", e)
	}

	// --- the escape arms -------------------------------------------------------------------------------
	escapes := []struct {
		name string
		op   func() error
	}{
		{"create ../pwned", func() error { f, err := root.Create("../pwned"); closeIf(f); return err }},
		{"openfile ../../pwned O_CREATE", func() error {
			f, err := root.OpenFile("../../pwned", os.O_CREATE|os.O_WRONLY, 0o600)
			closeIf(f)
			return err
		}},
		{"mkdir ../pwneddir", func() error { return root.Mkdir("../pwneddir", 0o700) }},
		{"read esc/secret.txt through a symlink out", func() error { _, err := root.ReadFile("esc/secret.txt"); return err }},
		{"create esc/pwned through a symlink out", func() error { f, err := root.Create("esc/pwned"); closeIf(f); return err }},
		{"read abs/passwd through an absolute symlink", func() error { _, err := root.ReadFile("abs/passwd"); return err }},
		{"rename out of the grant", func() error {
			if err := root.WriteFile("mv.txt", []byte("x"), 0o600); err != nil {
				return err
			}
			return root.Rename("mv.txt", "../escaped.txt")
		}},
		{"remove ../outside/secret.txt", func() error { return root.Remove("../outside/secret.txt") }},
		{"removeall ../outside", func() error { return root.RemoveAll("../outside") }},
		{"symlink to /etc then read through it", func() error {
			if err := root.Symlink("/etc", "newabs"); err != nil {
				return err
			}
			_, err := root.ReadFile("newabs/passwd")
			return err
		}},
	}
	for _, esc := range escapes {
		if err := esc.op(); err == nil {
			t.Errorf("ESCAPE PERMITTED: %s succeeded. The grant is not confined, and every write the "+
				"engine now offers is reachable outside the directory the operator named.", esc.name)
		}
	}

	// --- the permitted arm, without which the ten above prove nothing ---------------------------------
	permitted := []struct {
		name string
		op   func() error
	}{
		{"mkdir sub", func() error { return root.Mkdir("sub", 0o700) }},
		{"create sub/f.txt", func() error { f, err := root.Create("sub/f.txt"); closeIf(f); return err }},
		{"writefile sub/g.txt", func() error { return root.WriteFile("sub/g.txt", []byte("body"), 0o600) }},
		{"rename within the grant", func() error { return root.Rename("sub/g.txt", "sub/h.txt") }},
		{"truncate and sync via an opened fd", func() error {
			f, err := root.OpenFile("sub/h.txt", os.O_RDWR, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			if err := f.Truncate(2); err != nil {
				return err
			}
			return f.Sync()
		}},
		{"read back sub/h.txt", func() error { _, err := root.ReadFile("sub/h.txt"); return err }},
		{"remove sub/h.txt", func() error { return root.Remove("sub/h.txt") }},
		{"removeall sub", func() error { return root.RemoveAll("sub") }},
	}
	for _, p := range permitted {
		if err := p.op(); err != nil {
			t.Errorf("IN-GRANT REFUSED: %s failed: %v\n\tWithout this arm the ten escape refusals above "+
				"are also what a closed or broken root produces, so a vacuous confinement would read "+
				"as a perfect one.", p.name, err)
		}
	}

	// --- the writes landed in the GRANTED directory, not merely in some directory --------------------
	//
	// **This arm exists because an injection survived without it.** Confining the grant to the *parent* of
	// the granted directory (`os.OpenRoot(filepath.Dir(root))`) left every arm above green: the ten escapes
	// are still refused, because they escape the parent too, and the eight in-grant operations still
	// succeed, because they succeed in the wrong directory. The test was measuring *confined to a root*
	// and reading it as *confined to THE root*. Only the host filesystem can tell them apart.
	if err := root.Mkdir("landing", 0o700); err != nil {
		t.Fatalf("mkdir landing: %v", err)
	}
	if err := root.WriteFile("landing/proof.txt", []byte("landed"), 0o600); err != nil {
		t.Fatalf("write landing/proof.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inside, "landing", "proof.txt")); err != nil {
		t.Errorf("a write through the grant did not land inside the granted directory %q: %v\n"+
			"\tThe root is confining to the wrong place. Escape refusals cannot see this, because a root "+
			"pointed at the wrong directory still refuses paths that leave it.", inside, err)
	}
	if _, err := os.Stat(filepath.Join(base, "landing")); err == nil {
		t.Errorf("a write through the grant landed at %q, one level ABOVE the granted directory",
			filepath.Join(base, "landing"))
	}
	if err := root.RemoveAll("landing"); err != nil {
		t.Errorf("cleanup of landing: %v", err)
	}

	// --- and the filesystem's own account, because a refusal reported is not a write prevented --------
	for _, p := range []string{
		filepath.Join(base, "pwned"), filepath.Join(base, "pwneddir"),
		filepath.Join(base, "escaped.txt"), filepath.Join(outside, "pwned"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("BREACH: %s exists — an escape was refused to the caller and landed anyway", p)
		}
	}
	if b, err := os.ReadFile(secretPath); err != nil || string(b) != secret {
		t.Errorf("BREACH: the file outside the grant reads back as (%q, %v), want %q intact", b, err, secret)
	}
	t.Logf("CONFINEMENT: %d escape shape(s) refused, %d in-grant operation(s) permitted, nothing outside touched",
		len(escapes), len(permitted))
}

// TestFdPwriteRefusesAnFdThatDidNotComeFromAScratchGrant is amendment 1's own arm.
//
// **`fd_pwrite` is the engine's first capability decided by an fd's PROVENANCE**, not by a path: it receives
// a descriptor and an offset, there is nothing to resolve, and `os.Root` is irrelevant because the file was
// confined when it was opened. So the only question is where the fd came from — and **the permit path working
// is no evidence at all about that.** An `fd_pwrite` that ignored the origin bit entirely would pass every
// permitted case in this package, which is why this arm exists separately from the acceptance test.
func TestFdPwriteRefusesAnFdThatDidNotComeFromAScratchGrant(t *testing.T) {
	ro := t.TempDir()
	if err := os.WriteFile(filepath.Join(ro, "r.txt"), []byte("read-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()

	h, _ := hostFor(t,
		Preopen{Host: ro, Guest: "/ro"},
		Preopen{Host: scratch, Guest: "/rw", Writable: true},
	)

	// A file opened for reading under the READ-ONLY grant: the descriptor is valid and the capability is not.
	roFD := h.addFD(&fdEntry{file: mustOpen(t, filepath.Join(ro, "r.txt")), hostPath: filepath.Join(ro, "r.txt")})
	if got := pwriteErrno(t, h, roFD); got != errNotcapable {
		t.Errorf("fd_pwrite on a read-only grant's fd returned errno %d, want errNotcapable (%d): an fd "+
			"that never came from a scratch grant must be refused by the capability model", got, errNotcapable)
	}

	// stdout: a valid descriptor with no absolute offset. EBADF, because this is a property of the
	// descriptor rather than of a permission — and the two must not collapse into one answer.
	if got := pwriteErrno(t, h, 1); got != errBadf {
		t.Errorf("fd_pwrite on stdout returned errno %d, want errBadf (%d)", got, errBadf)
	}

	// **The permitted arm is NOT here, and that is a limitation rather than an omission.** A successful
	// `fd_pwrite` writes `nwritten` back into guest memory, which needs a `Caller` and therefore a whole
	// instance; calling it here panics. Without a permitted arm somewhere, the two refusals above are also
	// what a permanently-refusing `fd_pwrite` produces — so the permit is witnessed by a guest that really
	// writes: [TestAScratchGrantIsRequiredForEveryWrite] below, and the `os` package's own `WriteAtConcurrent` test, which
	// exercises `fd_pwrite` concurrently and is ADR 0091's acceptance part B.
	//
	// The `scratch` grant above is still built, because a host with only a read-only preopen would make the
	// first arm true for the wrong reason: there would be no writable grant for any fd to have come from.
	_ = scratch
}

func mustOpen(t *testing.T, p string) *os.File {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func closeIf(f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}

// refusalBinding reports how `preview1.go` binds `name`, read from the source rather than from a list.
//
// **The expectation is derived from what the host actually binds**, so a witness cannot claim a function
// refuses while the table implements it. A hand-kept list of refusals would have to be edited in lockstep
// with the table, which is the drift this whole family of controls exists to end — and it would be edited by
// whoever changed the table, i.e. by the actor whose change it is meant to catch.
func refusalBinding(t *testing.T, name string) string {
	t.Helper()
	src := readSourceOnce(t)
	re := regexp.MustCompile(`"` + regexp.QuoteMeta(name) + `":\s*\{ft\([^)]*\)[^,]*,\s*([^}]+)\}`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		return ""
	}
	return m[1]
}

var preview1Source string

func readSourceOnce(t *testing.T) string {
	t.Helper()
	if preview1Source == "" {
		b, err := os.ReadFile("preview1.go")
		if err != nil {
			t.Fatalf("read preview1.go: %v", err)
		}
		preview1Source = string(b)
	}
	return preview1Source
}

// pwriteErrno calls `fd_pwrite` on `fd` with a zero-length iovec array and reports the errno.
//
// **It works for the REFUSING cases only, and the first version of this comment claimed otherwise.** It said
// zero-length was sufficient because every check precedes any guest-memory access — true of the refusals, and
// false of the permitted path, which reaches `mWriteU32` to report `nwritten` and panics on the nil `Caller`
// this witness does not have. The claim was written from reading the handler's first few lines and was
// falsified by running it.
//
// So what is pinned here is the ORDERING for the refusals: a handler that read its iovecs before deciding the
// capability would fault here instead of answering, which is a louder and different failure. The permitted
// path is witnessed a layer up, by a guest that actually writes
// ([TestAScratchGrantIsRequiredForEveryWrite] and the `os` package's own `WriteAtConcurrent` test in acceptance part B).
func pwriteErrno(t *testing.T, h *host, fd uint32) uint16 {
	t.Helper()
	res, err := h.fdPwrite(nil, []interp.Value{
		interp.I32(int32(fd)), interp.I32(0), interp.I32(0), interp.I64(0), interp.I32(0),
	})
	if err != nil {
		t.Fatalf("fd_pwrite returned a Go error %v; a preview-1 handler reports through its errno", err)
	}
	return uint16(res[0].Int32())
}

// TestAScratchGrantIsRequiredForEveryWrite runs ONE guest under TWO grants and compares, which is ADR 0091's
// central claim stated as a difference rather than as two separate assertions.
//
// # Why one guest and two grants
//
// "Writes work under `--scratch`" and "writes are refused without it" are each satisfiable by an engine that
// gets the other half wrong. Running the same bytes under both grants makes the *grant* the only variable, so
// a step that behaves identically in both arms is a step this slice did not actually gate — and the test says
// so rather than counting it as a pass.
//
// # It also carries `fd_pwrite`'s permitted arm
//
// `fd_pwrite`'s refusals are witnessed at the host level, where a nil `Caller` is enough. Its *success* writes
// `nwritten` back into guest memory and so needs a real instance; the `writeat` step below is that arm. Without
// it, [TestFdPwriteRefusesAnFdThatDidNotComeFromAScratchGrant] would be equally satisfied by an `fd_pwrite`
// that refused everything.
func TestAScratchGrantIsRequiredForEveryWrite(t *testing.T) {
	wasm := buildGuest(t, "scratchwrite")

	run := func(t *testing.T, writable bool) map[string]string {
		t.Helper()
		dir := t.TempDir()
		var out, errBuf strings.Builder
		cfg := Config{
			Wasm: wasm, Args: []string{"scratchwrite", "/s"},
			Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errBuf,
			Preopens: []Preopen{{Host: dir, Guest: "/s", Writable: writable}},
		}
		if _, err := Run(cfg); err != nil {
			t.Fatalf("writable=%v: the guest did not run: %v\nstderr: %s", writable, err, errBuf.String())
		}
		steps := map[string]string{}
		for _, ln := range strings.Split(out.String(), "\n") {
			if !strings.HasPrefix(ln, "STEP ") {
				continue
			}
			parts := strings.SplitN(strings.TrimPrefix(ln, "STEP "), " ", 2)
			if len(parts) == 2 {
				steps[parts[0]] = parts[1]
			}
		}
		if !strings.Contains(out.String(), "SCRATCH-END") {
			t.Fatalf("writable=%v: the guest did not reach its end marker, so its step list is a prefix "+
				"rather than a result:\n%s", writable, out.String())
		}
		// **Left-behind state is part of the verdict.** A refusal reported to the guest is not the same
		// fact as nothing having been written, and only the host can tell the difference.
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !writable && len(ents) != 0 {
			t.Errorf("a READ-ONLY grant left %d entr(ies) in the host directory: %v — every write was "+
				"reported refused and something landed anyway", len(ents), ents)
		}
		if writable && len(ents) != 0 {
			t.Errorf("the scratch grant left %d entr(ies) behind: %v — the guest removed what it created, "+
				"so a leftover means an unlink or rmdir silently did nothing", len(ents), ents)
		}
		return steps
	}

	rw := run(t, true)
	ro := run(t, false)

	// Under a scratch grant every step must succeed, except the one written to fail for a reason that has
	// nothing to do with capabilities — a non-empty rmdir. Keeping it in the permitted arm is what stops
	// this test from reading "scratch makes everything succeed", which is not the claim.
	for _, name := range []string{"mkdir", "writefile", "open-rdwr", "writeat", "close", "readback", "unlink", "unlink-p", "rmdir"} {
		if got := rw[name]; got != "ok" {
			t.Errorf("under --scratch, step %q = %q, want ok", name, got)
		}
	}
	// The errno is asserted, not just the failure: preview-1 names this case, and the first run of this
	// witness reported *"I/O error"* because `ENOTEMPTY` was unmapped — a refusal telling the guest the host
	// had a problem rather than that its own directory was not empty.
	if got := rw["rmdir-nonempty-refused"]; got == "ok" {
		t.Errorf("under --scratch, removing a NON-EMPTY directory succeeded: path_remove_directory must " +
			"remove an empty directory only, or a guest asking to drop one directory gets a subtree deleted")
	} else if !strings.Contains(got, "not empty") {
		t.Errorf("a non-empty rmdir reported %q, want it to name ENOTEMPTY: an unmapped host errno becomes "+
			"errIO, which blames the host for a request the guest got wrong", got)
	}

	// Without it, every write must be refused — and the steps must DIFFER from the permitted arm, which is
	// the assertion that makes the grant the variable.
	//
	// **`writeat` is in this list and is UNREACHED rather than refused in the read-only arm**, because the
	// `O_CREATE` open ahead of it is refused first, so its entry is absent rather than `"ok"` and it passes
	// the loop below without witnessing anything. Stated rather than left implicit — *a failure shadows the
	// steps behind it*, which is the same shape that made ADR 0091's first recon report five refusals
	// instead of the demand set. `fd_pwrite`'s refusal is witnessed at the host level, where the open can
	// be bypassed; this arm cannot reach it.
	var same []string
	for _, name := range []string{"mkdir", "writefile", "open-rdwr", "writeat", "unlink", "rmdir"} {
		if ro[name] == "ok" {
			t.Errorf("WITHOUT a scratch grant, step %q succeeded: nothing is writable unless granted", name)
		}
		if ro[name] == rw[name] {
			same = append(same, name)
		}
	}
	if len(same) > 0 {
		t.Errorf("%d step(s) behaved identically with and without the grant: %s\n"+
			"\tThe grant is the only variable between the two arms, so a step that does not move is a "+
			"step this slice did not gate — and an all-refusing engine would pass the read-only arm alone.",
			len(same), strings.Join(same, ", "))
	}
	t.Logf("SCRATCH-DIFF: scratch=%v readonly=%v", rw, ro)
}

// openSubdirFD opens a directory inside a grant exactly the way `path_open`'s read branch does, and files it
// in the fd table — so the arms below run against the real entry shape rather than a hand-built one.
//
// It deliberately calls [attachDirRoot], which is why that function was extracted: a helper that rebuilt the
// attachment inline would assert against its own reimplementation, and a drift between the two would be
// invisible in a green run.
func openSubdirFD(t *testing.T, h *host, dir *preopenDir, guestPath string) uint32 {
	t.Helper()
	hostPath, e := h.resolveUnder(dir, guestPath)
	if e != errSuccess {
		t.Fatalf("resolving %q under the grant: errno %d", guestPath, e)
	}
	f, err := os.Open(hostPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	fi, serr := f.Stat()
	entry := &fdEntry{file: f, hostPath: hostPath, isDir: serr == nil && fi.IsDir()}
	if !entry.isDir {
		t.Fatalf("%q is not a directory; every arm below would be testing the wrong fd kind", guestPath)
	}
	attachDirRoot(entry, dir, guestPath)
	return h.addFD(entry)
}

// TestConfinementHoldsThroughASubdirectoryFD is the confinement surface `t.TempDir()`'s cleanup opened up.
//
// # Why this is a separate test from the preopen one
//
// Making the cleanup work required `path_*` to resolve against **any** directory fd, not only a preopen's —
// Go's `RemoveAll` descends by opening each directory and operating through that fd. That widened the
// confinement surface for *both* kinds of grant, and every arm in
// [TestAScratchGrantConfinesEveryWriteToItsRoot] starts from the preopen, so none of them reaches it. **The
// shape that now matters starts one level down.** (Ordered on the #833 review.)
//
// # The threat model, because it decides what layer these arms sit at
//
// **No stock Go guest can express this attack.** Go's `wasip1` runtime cleans a path textually before it
// reaches `path_open` (`appendCleanPath`), and its `os.Root` refuses escaping and absolute symlinks in the
// *guest* before any host call happens. A hostile module hand-written in wasm can send whatever path bytes it
// likes with whatever dirfd it holds, and that is the case being defended against.
//
// So these arms drive the resolution primitives — [host.resolveUnder] for reads and the nested `os.Root` for
// writes — against a subdirectory fd built by [openSubdirFD]. What they do **not** cover is the wasm-level
// marshalling between a guest's arguments and those primitives; the happy path of that is
// [TestAScratchGrantIsRequiredForEveryWrite] and `t.TempDir()`'s cleanup in acceptance part A.
//
// # Both grant kinds, because they reach confinement by different mechanisms
//
//	--scratch   the subdirectory fd holds a NESTED os.Root, so confinement is inherited from the grant's.
//	--dir       there is no root; resolution goes through resolveUnder against the subdirectory's own
//	            resolved host path, and containment is checked on the RESOLVED result.
func TestConfinementHoldsThroughASubdirectoryFD(t *testing.T) {
	for _, grant := range []struct {
		name     string
		writable bool
	}{{"scratch", true}, {"dir", false}} {
		t.Run(grant.name, func(t *testing.T) {
			base := t.TempDir()
			inside := filepath.Join(base, "grant")
			outside := filepath.Join(base, "outside")
			sub := filepath.Join(inside, "sub")
			for _, d := range []string{sub, outside} {
				if err := os.MkdirAll(d, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			const secret = "SUBDIR-SECRET"
			if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte(secret), 0o600); err != nil {
				t.Fatal(err)
			}
			// A file inside the grant but OUTSIDE the subdirectory, so "climbed out of the subdir" is
			// distinguishable from "climbed out of the grant".
			if err := os.WriteFile(filepath.Join(inside, "sibling.txt"), []byte("sibling"), 0o600); err != nil {
				t.Fatal(err)
			}
			// Symlinks planted INSIDE the subdirectory, pointing out of the grant. These are the shapes a
			// textual `..` filter cannot see, and they are relative to the subdirectory fd rather than to
			// the preopen.
			if err := os.Symlink(outside, filepath.Join(sub, "esc")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/etc", filepath.Join(sub, "abs")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(sub, "link-to-secret")); err != nil {
				t.Fatal(err)
			}

			h, preopenFD := hostFor(t, Preopen{Host: inside, Guest: "/g", Writable: grant.writable})
			grantDir := h.dirAt(preopenFD)
			if grantDir == nil {
				t.Fatal("the preopen fd yielded no directory; every arm below would be vacuous")
			}
			subFD := openSubdirFD(t, h, grantDir, "sub")
			subDir := h.dirAt(subFD)
			if subDir == nil {
				t.Fatal("a subdirectory fd yielded no directory, so path_* against it answers EBADF and " +
					"t.TempDir's cleanup could not have worked")
			}
			if grant.writable && subDir.root == nil {
				t.Fatal("a subdirectory fd under a SCRATCH grant carries no nested root, so every write " +
					"through it would be refused and the permitted arms below would pass vacuously")
			}

			// --- READ escapes, relative to the subdirectory fd ---------------------------------------
			readEscapes := []struct{ name, path string }{
				{"climb out of the grant", "../../outside/secret.txt"},
				{"climb further than the base", "../../../../../../etc/passwd"},
				{"absolute path", "/etc/passwd"},
				{"through a symlink pointing out", "esc/secret.txt"},
				{"through an absolute symlink", "abs/passwd"},
				{"a symlink straight at the secret", "link-to-secret"},
			}
			for _, esc := range readEscapes {
				got, errno := h.resolveUnder(subDir, esc.path)
				if errno == errSuccess {
					t.Errorf("READ ESCAPE PERMITTED via a subdirectory fd: %s (%q) resolved to %q\n"+
						"\tResolution against a non-preopen directory fd must be confined too. This is "+
						"the surface t.TempDir's cleanup opened up (#833 review).", esc.name, esc.path, got)
				}
			}

			// **A path inside the grant but outside the subdirectory is also refused, and that is
			// STRICTER than the grant rather than weaker.** Recorded as an observation, not asserted as a
			// requirement: containment is checked against the subdirectory's resolved path, and anything
			// contained by a directory inside the grant is contained by the grant, so the direction of the
			// difference is safe. A guest that wants the sibling can address it through the preopen.
			if _, errno := h.resolveUnder(subDir, "../sibling.txt"); errno == errSuccess {
				t.Logf("SUBDIR-SCOPE: ../sibling.txt IS reachable through the subdirectory fd — resolution " +
					"is grant-scoped rather than subdirectory-scoped. Safe either way; recorded because " +
					"the arms above assume the stricter reading.")
			} else {
				t.Logf("SUBDIR-SCOPE: ../sibling.txt is refused through the subdirectory fd (errno %d) — "+
					"resolution is subdirectory-scoped, which is stricter than the grant.", errno)
			}

			// --- a permitted read through the subdirectory fd, so the refusals above mean something ---
			if err := os.WriteFile(filepath.Join(sub, "ok.txt"), []byte("ok"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got, errno := h.resolveUnder(subDir, "ok.txt"); errno != errSuccess {
				t.Errorf("a legitimate read through the subdirectory fd was refused (errno %d): without "+
					"this arm the six refusals above are also what a subdirectory fd that resolved "+
					"NOTHING produces", errno)
			} else {
				// **Compared against the SYMLINK-RESOLVED expectation.** `resolveUnder` returns an
				// `EvalSymlinks` result, and on macOS `t.TempDir()` hands back a path under `/var`, which
				// is a symlink to `/private/var`. Comparing against the unresolved path failed here and
				// the failure was mine, not the engine's — the resolver is doing exactly what ADR 0083
				// requires of it.
				wantOK, rerr := filepath.EvalSymlinks(filepath.Join(sub, "ok.txt"))
				if rerr != nil {
					t.Fatalf("resolving the expected path: %v", rerr)
				}
				if got != wantOK {
					t.Errorf("a read through the subdirectory fd resolved to %q, want %q — the fd is "+
						"pointed at the wrong directory", got, wantOK)
				}
			}

			if !grant.writable {
				// A read-only grant's subdirectory fd must have no write capability at all.
				if _, errno := h.dirRootAt(subFD); errno != errNotcapable {
					t.Errorf("a subdirectory fd under a READ-ONLY grant answered errno %d for a write "+
						"root, want errNotcapable (%d): --dir stays read-only at every level, not only "+
						"at the preopen", errno, errNotcapable)
				}
				return
			}

			// --- WRITE escapes through the subdirectory fd's nested root -----------------------------
			root, errno := h.dirRootAt(subFD)
			if errno != errSuccess {
				t.Fatalf("the subdirectory fd yielded no write root (errno %d)", errno)
			}
			writeEscapes := []struct {
				name string
				op   func() error
			}{
				{"create above the grant", func() error { f, err := root.Create("../../pwned"); closeIf(f); return err }},
				{"mkdir above the grant", func() error { return root.Mkdir("../../pwneddir", 0o700) }},
				{"create through a symlink out", func() error { f, err := root.Create("esc/pwned"); closeIf(f); return err }},
				{"write through a symlink at the secret", func() error {
					return root.WriteFile("link-to-secret", []byte("OVERWRITTEN"), 0o600)
				}},
				{"remove the outside secret", func() error { return root.Remove("../../outside/secret.txt") }},
				{"rename out of the grant", func() error {
					if err := root.WriteFile("mv.txt", []byte("x"), 0o600); err != nil {
						return err
					}
					return root.Rename("mv.txt", "../../escaped.txt")
				}},
			}
			for _, esc := range writeEscapes {
				if err := esc.op(); err == nil {
					t.Errorf("WRITE ESCAPE PERMITTED via a subdirectory fd: %s. The nested root is not "+
						"inheriting the grant's confinement.", esc.name)
				}
			}

			// --- and the landing check: a permitted write through the subdirectory fd lands INSIDE ----
			//
			// This is the arm the preopen test learned it needed: escape refusals cannot tell a root
			// aimed at the wrong directory from one aimed correctly, because both refuse the same shapes.
			if err := root.Mkdir("landing", 0o700); err != nil {
				t.Fatalf("mkdir through the subdirectory fd: %v", err)
			}
			if err := root.WriteFile("landing/proof.txt", []byte("landed"), 0o600); err != nil {
				t.Fatalf("write through the subdirectory fd: %v", err)
			}
			want := filepath.Join(sub, "landing", "proof.txt")
			if _, err := os.Stat(want); err != nil {
				t.Errorf("a write through the subdirectory fd did not land at %q: %v\n"+
					"\tThe nested root is confining to the wrong directory.", want, err)
			}
			for _, wrong := range []string{
				filepath.Join(inside, "landing"), filepath.Join(base, "landing"), filepath.Join(outside, "landing"),
			} {
				if _, err := os.Stat(wrong); err == nil {
					t.Errorf("a write through the subdirectory fd landed at %q instead of beneath the "+
						"subdirectory", wrong)
				}
			}

			// The filesystem's own account: a refusal reported is not a write prevented.
			for _, p := range []string{
				filepath.Join(base, "pwned"), filepath.Join(base, "pwneddir"),
				filepath.Join(base, "escaped.txt"), filepath.Join(outside, "pwned"),
			} {
				if _, err := os.Stat(p); err == nil {
					t.Errorf("BREACH: %s exists — an escape was refused to the caller and landed anyway", p)
				}
			}
			if b, err := os.ReadFile(filepath.Join(outside, "secret.txt")); err != nil || string(b) != secret {
				t.Errorf("BREACH: the secret outside the grant reads back as (%q, %v), want %q intact",
					b, err, secret)
			}
			t.Logf("SUBDIR CONFINEMENT (%s): %d read escape(s) and %d write escape(s) refused, legitimate "+
				"access permitted, writes landed beneath the subdirectory", grant.name,
				len(readEscapes), len(writeEscapes))
		})
	}
}
