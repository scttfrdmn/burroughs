// Package xcorpus builds the cross-check corpus for #67 half 2: for every suite text module
// that must succeed, an independently produced binary image of the same module.
//
// # Why an outside producer, and why it is not in the verdict path
//
// #67's second half asks whether the encoder emits *the right module*, not merely a
// well-formed one. Answering it needs a statement of what the text denotes that does not
// come from Burroughs — comparing our encoder against our decoder is one witness talking to
// itself, which decision 0011's second appendix rules inadmissible. So the images here are
// produced by **wabt**, and wabt appears exactly once, as a generator whose output is
// committed with a provenance manifest. It is never invoked by a test, never in CI, and never
// in the verdict path: the same posture the generated tables have toward the reference
// interpreter (0007, 0009, 0014), for the same reason — a non-Go binary in the conformance
// loop is reproducibility debt in the place the project can least afford it (#8).
//
// The manifest is what makes the committed bytes evidence rather than assertion: it records
// the wabt version, the suite SHA the corpus was cut from, and — this is the part a floor
// cannot supply — **every file wabt could not compile**, so an absence is a stated fact
// instead of a silently smaller population.
//
// # The join, which is an ordinal and not a line
//
// `wast2json` is a second splitter of the same `.wast` files, so a module image has to be
// matched to the command *this* project's `spec.ParseFile` produced. Line number is the
// obvious key and it is **wrong**, measured rather than assumed: in `comments.wast` the two
// readers disagree by one on two modules, because we report the line of the opening `(` and
// wabt reports the line of the `module` keyword —
//
//	 9: ( ;;comment
//	10: module;;comment
//
// and both readings are defensible. Neither reader is buggy, so a key they legitimately
// disagree on is not a key. The join is therefore the module's **ordinal within its file**,
// which both splitters agree on because both walk the same command sequence in order.
//
// Line is retained anyway, as a *corroborating* signal rather than the key — being unfit to
// key on is exactly what makes it a sound second opinion (0014's correction, grave #106): a
// pair whose ordinals line up but whose lines are far apart means the two command sequences
// diverged somewhere earlier, which an ordinal join cannot detect on its own. Measured over
// the whole corpus: 1953 of our 2238 module commands join, and exactly one pair exceeds a ±2
// line window — the `comments.wast` quote form, for which wabt reports line 0.
//
// **The 2238 is era-marked, and only the denominator moved.** #459 taught the s-expression reader
// to drop annotation nodes, which gave `annotations.wast:98,129,154` a `module` head for the first
// time — the instrumented census next door reports it, `publicpath_test.go`'s module pin going
// 2238 → 2241. The join count is *not* restated as 1956 or as 1953-of-2241, because this pair of
// figures came from a one-off session measurement rather than from a committed instrument and
// nothing here can re-derive it: `annotations.wast` is one of the 31 files wabt 1.0.41 cannot
// compile (the annotation flag is outside the tracked union, below), so the three have no
// reference side to join to and the ratio can only have got slightly worse. Saying which half is
// re-measured and which is inferred, rather than adding three to both sides and calling it
// arithmetic.
//
// # What the corpus does not cover, stated rather than floored
//
// wabt 1.0.41 cannot compile 31 of the 257 files — the GC type syntax (`i8`, `i16`, `rec`,
// `sub`, `i31ref`, `anyref`, `arrayref`), the `(module definition ...)` script form, and the
// annotation syntax, whose flag is outside the tracked union. Each is in the manifest by name
// with its diagnostic. The gap matters to a reader deciding what the control proves, and a
// population quoted as "1954 modules" without it would overstate the coverage — *no silent
// caps*.
//
// The gap is also *accounted for* rather than merely stated: the 285 module commands that find
// no counterpart all live in files the manifest names as skipped, measured, with nothing else
// hiding in that number. An unexplained residue in a join is where a mis-keyed pair would sit.
package xcorpus

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// Module is one image in the corpus: an independently produced encoding of the suite text
// module at Ordinal within File.
type Module struct {
	// File is the `.wast` base name without its extension, as `spec.ParseFile` sees it.
	File string
	// Ordinal is the module's position among *all* module commands in its file, counting
	// from zero — the join key. Every module command counts, including the binary and quote
	// forms, because both splitters enumerate the same sequence and skipping a kind on one
	// side only would shift the key.
	Ordinal int
	// Line is wabt's line for the module, kept as a corroborating signal and *not* as a key.
	// See the package comment: the two splitters legitimately disagree here.
	Line int
	// Image is the encoded module.
	Image []byte
}

// Manifest is the corpus and the facts that make it evidence.
type Manifest struct {
	// WabtVersion is `wast2json --version` output, verbatim.
	WabtVersion string
	// SuiteRev is the suite SHA the corpus was cut from, read from the fetch script's pin
	// rather than typed — a SHA at a second site is a citation that can drift from the pin
	// it claims to describe (opgen's cmd states the same reason).
	SuiteRev string
	// Modules is the corpus, sorted by (File, Ordinal) so the emitted output is stable.
	Modules []Module
	// SkippedFiles names every `.wast` file wabt could not compile, with the first line of
	// its diagnostic. A stated absence rather than a smaller number: this is the part of the
	// provenance a count floor cannot carry.
	SkippedFiles []SkippedFile
}

// SkippedFile is one file the generator could not get images out of.
//
// Tagged because this type is the one part of the in-memory Manifest that reaches the committed
// JSON directly rather than through `manifestDoc`'s tagged mirror — untagged, the manifest would
// spell these `File`/`Reason` while every neighbouring key is snake_case.
type SkippedFile struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// reVersion pins the shape of the version string so a generator run against a wabt whose
// `--version` output changed format stamps nothing rather than stamping a surprise.
//
// *A provenance header that says nothing is worse than none, because it looks stamped*
// (gen.PinnedRev's reason, and spec_test's suitePin repeats it). A version that failed to
// parse would otherwise reach the manifest as an empty string, which reads as "recorded".
var reVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// WabtVersion reports the version of the `wast2json` on PATH.
func WabtVersion() (string, error) {
	out, err := exec.Command("wast2json", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("running wast2json --version (is wabt installed?): %w", err)
	}
	v := strings.TrimSpace(string(out))
	if !reVersion.MatchString(v) {
		return "", fmt.Errorf("wast2json --version printed %q, want a bare x.y.z: the manifest "+
			"records this verbatim, and an unrecognized shape means the field would be "+
			"stamped with something that is not a version", v)
	}
	return v, nil
}

// wastJSON is the subset of `wast2json`'s output this generator reads.
//
// Only the module commands, and only their filename and line. The assertion commands are
// deliberately not read: this corpus answers "what module does this text denote", and
// borrowing wabt's *expected results* as well would put a second engine's semantics into the
// project, which is a much larger claim than #67 needs and one the reference interpreter —
// not wabt — is the authority for.
type wastJSON struct {
	Commands []struct {
		Type     string `json:"type"`
		Line     int    `json:"line"`
		Filename string `json:"filename"`
	} `json:"commands"`
}

// Generate runs wast2json over every `.wast` file in suiteDir and collects the images.
//
// workDir receives wast2json's scratch output and is the caller's to create and remove; the
// images are read into memory before it is cleaned up.
//
// **Every file is attempted and every failure is recorded**, rather than stopping at the
// first: a generator that aborts on `annotations.wast` would produce a corpus whose
// shortfall is an exit code instead of a manifest entry.
func Generate(suiteDir, workDir, suiteRev string) (*Manifest, error) {
	version, err := WabtVersion()
	if err != nil {
		return nil, err
	}
	// testenv.SuitePaths, not a local glob: the corpus's population must be the board's
	// population (#340), or the manifest's `SkippedFiles` is a statement about a set nothing
	// else measures. testenv in a generator is the existing shape — `cmd/xcorpus/main.go`
	// already resolves `testenv.SuiteDir` through it.
	paths, err := testenv.SuitePaths(suiteDir)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no .wast files under %s (run: make spec-tests): a corpus "+
			"generated from an empty suite would agree perfectly with an empty committed "+
			"corpus", suiteDir)
	}
	sort.Strings(paths)

	m := &Manifest{WabtVersion: version, SuiteRev: suiteRev}
	for _, path := range paths {
		base := strings.TrimSuffix(filepath.Base(path), ".wast")
		mods, ferr := generateOne(path, base, workDir)
		if ferr != nil {
			m.SkippedFiles = append(m.SkippedFiles, SkippedFile{File: base, Reason: ferr.Error()})
			continue
		}
		m.Modules = append(m.Modules, mods...)
	}
	sort.Slice(m.Modules, func(i, j int) bool {
		if m.Modules[i].File != m.Modules[j].File {
			return m.Modules[i].File < m.Modules[j].File
		}
		return m.Modules[i].Ordinal < m.Modules[j].Ordinal
	})
	return m, nil
}

// features is the flag set wast2json is invoked with: **the tracked union, enumerated to
// match it, and nothing else**.
//
// # Why not `--enable-all`, which is what this started as
//
// Because a feature flag does not only decide what wabt *accepts* — it can change the bytes
// of a module the standard grammar already describes, and then the corpus records an encoding
// the suite never asked for. Measured over the whole suite, three flags re-encode modules that
// compile fine without them (baseline 1573 common modules, images compared byte for byte):
//
//	function-references   105 modules  element segments in expression form (05 70 04 d2 00 0b …)
//	                                   where the baseline emits the elemkind form
//	compact-imports        45 modules   an import as `00 7f` instead of repeating the module name
//	multi-memory            2 modules   (data0)
//
// `--enable-all` therefore made this corpus disagree with our decoder in a bucket that was not a
// gate report at all: 58 × `malformed import kind: 0x7f`, the compact-import form. That was the
// *generator* handing over a proposal encoding, and a cross-check corpus that quietly re-encodes
// its subject is the wrong-module failure #67 exists to detect, arriving through the instrument —
// the worst place for it.
//
// The correction that came with the fix is worth keeping, because it is the shape of a wrong
// diagnosis: a second bucket, 9 × `constant expression required: 0x6a/0x6b/0x6c`, was attributed
// to `--enable-all` at the same time and **survived the fix**. Extended-const needs no flag —
// wabt compiles `(data (i32.add (i32.const 0) (i32.const 42)))` by default, and
// `--enable-extended-const` is measurably inert here (0 modules gained, 0 bytes changed). So
// those 9 are baseline corpus content that *our decoder rejects*, which is an accept-direction
// finding and the first thing this corpus caught. Two buckets that appeared together under one
// flag change had two different causes; only re-measuring after the fix separated them.
//
// # The criterion is the tracked union, not "does it re-encode"
//
// Re-encoding is the *symptom* that forced the question; it cannot be the rule, because
// function-references re-encodes more modules than anything else and is nevertheless **in** the
// tracked union (sections.go's GC gate: "the 0xfb region; ref.eq, and the function-references
// five"). Dropping a flag merely for changing bytes would shrink the corpus by 133 modules and
// 18 whole files to avoid an encoding the engine is required to read.
//
// So the rule is contract §9 G-2: the grammar this project recognizes is the union of the
// tracked set, and the corpus records that union's encodings. Every flag below is a proposal
// `Features` declares a gate for; every flag omitted is one it does not track. What that costs
// is measurable and small — the omitted flags (annotations, code-metadata, custom-page-sizes,
// compact-imports, wide-arithmetic) buy **one** additional module between them, against the 47
// spurious re-encodings the last two produce.
//
// **Extended-const left that list when #109 was stamped**, and the sweep is why it is called out
// rather than quietly edited: a ruling retroactively falsifies the prose written before it, and
// this paragraph said "six omitted flags" naming extended-const among proposals the project "does
// not track" at the moment the contract began tracking it. G-2 now reads "all of Wasm 3.0 core",
// derived from the spec's own release appendix, and extended-const is one of the ten — so the
// clause that licensed the omission is gone even though the omission itself is unchanged. It
// stays omitted because wabt does not gate it and the flag is measurably inert, which is a
// statement about the *producer*; it was previously omitted because we did not track the
// proposal, which was a statement about the *contract*. Same argv, different reason, and the
// reason is the part a reader is relying on.
//
// Note what the criterion does *not* do: omitting `--enable-extended-const` does not keep
// extended-const out of the corpus, because wabt does not gate it. A flag list controls what the
// producer accepts, never what the suite contains — so the omissions above are about **encoding
// fidelity**, and the question of which proposals our decoder must accept is settled by the
// corpus's contents, not by this list. That sentence was load-bearing and is now demonstrated:
// the nine modules our decoder wrongly rejected were in the corpus with the flag off, which is
// exactly what it predicted.
//
// # Enumerated, and this is the one place that is right
//
// *Derive the domain, never enumerate it* is the standing rule, and it is deliberately not
// followed here: this list is a statement about **wabt's** flag vocabulary, which no Go type in
// this repo is the authority for. `--disable-compact-imports` does not exist, so subtracting
// from `--enable-all` is not available either. Deriving these strings from `Features` field
// names would be a fabricated correspondence — `Threads` is `--enable-threads` but
// `ExceptionHandling` is `--enable-exceptions`, and a mapping that has to be hand-written to be
// correct is testimony whether or not it is spelled as a loop (gatemap.go's reason, exactly).
//
// The correspondence is instead *checked*: TestFeatureFlagsCoverTheTrackedGates reflects over
// `Features` and requires every field to appear as a key below, so a ninth gate added to the
// struct fails here rather than silently narrowing the corpus. Same posture as gatemap.go's
// "every bool in Features maps at least one construct" — a gate the corpus does not enable is a
// gate whose accept direction the corpus cannot speak to.
//
// The empty value is a real entry and not a hole: wabt has SIMD **on by default** (it offers
// `--disable-simd`, not an enable form), so the flag for that gate is legitimately "already
// on". Recording it as `""` with a reason keeps the mapping total — omitting the key would make
// the reflection check fail for a fact that is fine, and a check that must be excused is a check
// on its way to being deleted.
var featureFlag = map[string]string{
	"ExceptionHandling": "--enable-exceptions",
	"SIMD":              "", // on by default in wabt; only --disable-simd exists
	"Threads":           "--enable-threads",
	"Memory64":          "--enable-memory64",
	"GC":                "--enable-gc",
	"TailCall":          "--enable-tail-call",
	"RelaxedSIMD":       "--enable-relaxed-simd",
	"MultiMemory":       "--enable-multi-memory",
	// Empty for the same reason SIMD is, and the reason is *already measured above*: wabt
	// compiles `(data (i32.add (i32.const 0) (i32.const 42)))` without being asked, and
	// `--enable-extended-const` was measured inert here — 0 modules gained, 0 bytes changed.
	// The flag exists in wabt's vocabulary, unlike SIMD's, so the entry says "passing it would
	// change nothing" rather than "there is nothing to pass"; both are honest `""`s and the
	// distinction is why each carries its own reason instead of sharing one.
	//
	// This entry arrived with the gate (#109) and did **not** require re-cutting the corpus,
	// which is the unusual case worth naming: the nine extended-const modules were in the
	// snapshot all along — that is how the corpus found the decoder rejecting them — so unlike
	// every other gate here, the flag list and the corpus contents were never out of step.
	"ExtendedConst": "",
}

// extraFlags are tracked-union flags with no `Features` field of their own.
//
// `function-references` is the case, and it is not an exception to the criterion: decision 0008
// folded it into the GC gate ("ref.eq, and the function-references five"), so the proposal is
// tracked while the flag has no field to be keyed by. It is listed separately rather than
// mapped onto "GC" so that the one-flag-per-field check stays an equality — a many-to-one map
// would let a future flag hide behind an existing gate, which is how the ninth-gate failure
// gets back in.
var extraFlags = []string{"--enable-function-references"}

// features is the assembled argv fragment, sorted so a regeneration diff is about images.
var features = func() []string {
	out := append([]string{}, extraFlags...)
	for _, f := range featureFlag {
		if f != "" {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}()

// reDiagnostic pulls the first line of a wast2json diagnostic for the manifest's Reason
// field. The full output is multi-line with a caret indicator; one line is what a reader
// deciding "is this gap explained" needs.
var reDiagnostic = regexp.MustCompile(`(?m)^.*?:\d+:\d+: (error: .*)$`)

// generateOne converts one file and reads back the images it produced.
func generateOne(path, base, workDir string) ([]Module, error) {
	jsonPath := filepath.Join(workDir, base+".json")
	args := append([]string{}, features...)
	args = append(args, path, "-o", jsonPath)
	cmd := exec.Command("wast2json", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		reason := strings.TrimSpace(string(out))
		if mm := reDiagnostic.FindStringSubmatch(reason); mm != nil {
			reason = mm[1]
		}
		if reason == "" {
			reason = err.Error()
		}
		return nil, fmt.Errorf("%s", reason)
	}

	b, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, err
	}
	var doc wastJSON
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", jsonPath, err)
	}

	var mods []Module
	ordinal := 0
	for _, c := range doc.Commands {
		if c.Type != "module" || c.Filename == "" {
			continue
		}
		img, rerr := os.ReadFile(filepath.Join(workDir, c.Filename))
		if rerr != nil {
			return nil, fmt.Errorf("reading the image %s wast2json named: %w", c.Filename, rerr)
		}
		if len(img) == 0 {
			// A zero-length image would join fine and compare against nothing — the
			// empty-set agreement at the level of one row (#29).
			return nil, fmt.Errorf("wast2json emitted an empty image for %s ordinal %d", base, ordinal)
		}
		mods = append(mods, Module{File: base, Ordinal: ordinal, Line: c.Line, Image: img})
		ordinal++
	}
	if len(mods) == 0 {
		// Not an error: a file of pure `assert_malformed` vectors legitimately has no
		// must-succeed module. Returning an empty slice keeps it out of SkippedFiles, which is
		// for files wabt *could not read* — conflating the two would make the manifest's stated
		// gap wrong in the direction that overstates it.
		return nil, nil
	}
	return mods, nil
}
