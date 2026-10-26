// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestOracleFetchRefusesATamperedArchive is the chair's witness for `scripts/oracle-fetch.sh` (third ruling of
// 2026-10-26): a tampered archive is refused on its checksum.
//
// The test builds a release-shaped archive whose `wasmtime` reports 49.0.3, and writes that archive's SHA-256 to
// a sums file of its own. Then:
//   - the untouched archive installs, so the script is not refusing everything;
//   - a substituted archive (well-formed, same name and shape, its binary also reporting 49.0.3) is refused on
//     its checksum, and nothing is installed;
//   - the archive with one byte flipped is refused on its checksum too.
//
// It runs offline through the script's ORACLE_ARCHIVE, ORACLE_SUMS and ORACLE_DIR overrides; the platform
// arm and the download are the script's own.
//
// **Neuter, run:** the comparison removed (`[ "$got" = "$want" ] || die …` replaced by `true`). The substituted
// archive then INSTALLS, and its arm fails on acceptance. The corrupted arm fails as well, but only on its
// reason: `tar` refuses the corrupted stream on xz's own check (*"Lzma library error: Corrupted input data"*).
// That is why the substitution is the witness: a corrupted download has a second guard, and a substituted one
// has only the checksum.
func TestOracleFetchRefusesATamperedArchive(t *testing.T) {
	plat := map[string]string{
		"darwin/arm64": "aarch64-macos", "darwin/amd64": "x86_64-macos",
		"linux/amd64": "x86_64-linux", "linux/arm64": "aarch64-linux",
	}[runtime.GOOS+"/"+runtime.GOARCH]
	if plat == "" {
		t.Skipf("oracle-fetch.sh pins archives for darwin and linux only; %s/%s has no sums row", runtime.GOOS, runtime.GOARCH)
	}
	script, err := filepath.Abs("../../scripts/oracle-fetch.sh")
	if err != nil {
		t.Fatal(err)
	}

	// A release-shaped archive: wasmtime-v49.0.3-<plat>/wasmtime, an executable reporting the version.
	work := t.TempDir()
	top := "wasmtime-v49.0.3-" + plat
	if err := os.MkdirAll(filepath.Join(work, "src", top), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := "#!/bin/sh\necho 'wasmtime 49.0.3 (witness)'\n"
	if err := os.WriteFile(filepath.Join(work, "src", top, "wasmtime"), []byte(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	name := top + ".tar.xz"
	archive := filepath.Join(work, name)
	if out, err := exec.Command("tar", "-cJf", archive, "-C", filepath.Join(work, "src"), top).CombinedOutput(); err != nil {
		t.Fatalf("building the witness archive: %v\n%s", err, out)
	}
	good, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(good)
	sums := filepath.Join(work, "sums")
	if err := os.WriteFile(sums, []byte(hex.EncodeToString(sum[:])+"  "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(arch, dest string) (string, error) {
		c := exec.Command("sh", script)
		c.Env = append(os.Environ(), "ORACLE_ARCHIVE="+arch, "ORACLE_SUMS="+sums, "ORACLE_DIR="+dest)
		out, err := c.CombinedOutput()
		return string(out), err
	}
	installed := func(dest string) bool {
		_, err := os.Stat(filepath.Join(dest, "wasmtime-49.0.3", "wasmtime"))
		return err == nil
	}

	t.Run("an_untouched_archive_installs", func(t *testing.T) {
		dest := filepath.Join(work, "ok")
		out, err := run(archive, dest)
		if err != nil || !installed(dest) {
			t.Fatalf("the untouched archive was not installed (err %v):\n%s", err, out)
		}
	})

	refused := func(t *testing.T, tampered, dest string) {
		t.Helper()
		out, err := run(tampered, dest)
		if err == nil {
			t.Fatalf("a tampered archive was accepted:\n%s", out)
		}
		if !strings.Contains(out, "checksum mismatch") {
			t.Errorf("the tampered archive was refused, but not on its checksum:\n%s", out)
		}
		if installed(dest) {
			t.Errorf("a refused archive left an install behind at %s", dest)
		}
	}

	// The substitution: a well-formed archive of the same name and shape whose binary also reports 49.0.3,
	// so the checksum is the only thing that can tell it from the pinned one.
	t.Run("a_substituted_valid_archive_is_refused_on_its_checksum", func(t *testing.T) {
		sub := filepath.Join(work, "sub")
		if err := os.MkdirAll(filepath.Join(sub, "src", top), 0o755); err != nil {
			t.Fatal(err)
		}
		evil := "#!/bin/sh\necho 'wasmtime 49.0.3 (substituted)'\n"
		if err := os.WriteFile(filepath.Join(sub, "src", top, "wasmtime"), []byte(evil), 0o755); err != nil {
			t.Fatal(err)
		}
		tampered := filepath.Join(sub, name)
		if out, err := exec.Command("tar", "-cJf", tampered, "-C", filepath.Join(sub, "src"), top).CombinedOutput(); err != nil {
			t.Fatalf("building the substituted archive: %v\n%s", err, out)
		}
		refused(t, tampered, filepath.Join(work, "bad-sub"))
	})

	// The corruption: one byte flipped. xz's own check would also catch this one at unpacking, which is why
	// the substitution above, not this arm, is what the neuter kills on acceptance.
	t.Run("a_corrupted_archive_is_refused_on_its_checksum", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[len(bad)/2] ^= 0xff
		tampered := filepath.Join(work, "corrupt", name)
		if err := os.MkdirAll(filepath.Dir(tampered), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tampered, bad, 0o644); err != nil {
			t.Fatal(err)
		}
		refused(t, tampered, filepath.Join(work, "bad-corrupt"))
	})
}
