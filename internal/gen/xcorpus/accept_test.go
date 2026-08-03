package xcorpus_test

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/gen/xcorpus"
)

// The accept-direction control on the decoder, and it is the reason #109 was findable at all.
//
// # Why the suite cannot ask this question
//
// Every one of the board's 4162 green vectors is a **rejection**: `assert_malformed` with an
// expected error string. So the board measures one direction of the decoder's judgement
// exhaustively and the other direction **not at all** — a decoder that rejects every valid
// module in existence scores 4162/4162. Contract §9 G-3 names that asymmetry and the standing
// rule draws the consequence: *a control that would catch an accept-direction defect the suite
// cannot see is product work*, because the suite scores such defects green by construction and
// nothing else will find them.
//
// This corpus is 1954 modules that a **conforming independent producer** (wabt 1.0.41) emitted
// from the suite's must-succeed text modules. Every one of them is therefore a module the engine
// is *required* to accept, which makes "decode all of them" the exact question the board never
// asks.
//
// # What it found, and what that says about the shape of the risk
//
// #109. With every gate on, nine modules were rejected with `constant expression required:
// 0x6a/0x6b/0x6c` — `(data (i32.add (i32.const 0) (i32.const 42)))` and its siblings across
// `data.wast`, `elem.wast`, and `global.wast`. Extended-const was not in `Features` at all, and
// `constOps`' comment asserted the proposal "arrives with its gate" while no such gate existed:
// the defect stated as the rule, which review cannot catch because review verifies code against
// claims. Nine wrongly-rejected valid modules, invisible on two boards, found by a corpus that
// simply tried to read them.
//
// The measurement was made by hand at the time and then **discarded**, which is the gap this file
// closes. A finding produced by an ad-hoc probe is a finding the next regression re-earns; the
// corpus was committed for #67's comparator and the accept question was answered in a scratch
// file. That is the *artifacts become oracles* rule, one step later than it should have been.

// allFeaturesOn is every gate on, derived by reflection rather than listed.
//
// Third copy of this helper (internal/binary, internal/spec, here), and the duplication is the
// same deliberate one those two document: the dependency cannot run from the engine to a test,
// and exporting a testing-only door from `binary` to serve tests in other packages is worse than
// three derived copies. What makes it safe is that all three *derive* — none enumerates the
// fields — so a tenth gate is picked up everywhere without an edit.
func allFeaturesOn(t *testing.T) binary.Features {
	t.Helper()

	var f binary.Features
	v := reflect.ValueOf(&f).Elem()
	for i := range v.NumField() {
		fld := v.Field(i)
		if fld.Kind() != reflect.Bool {
			t.Fatalf("binary.Features.%s is %s, not a bool: a gate this helper cannot turn on runs "+
				"as *off* while the test claims everything is on, and every module using it would "+
				"then be counted as a rejection this control blames on the decoder",
				v.Type().Field(i).Name, fld.Kind())
		}
		fld.SetBool(true)
	}
	return f
}

// TestEveryCorpusModuleDecodesUnderFullFeatures is the control: under the tracked union, the
// decoder must accept every module a conforming producer emitted.
//
// **Zero rejections, exactly** — not a ceiling, not a floor. This is one of the few bounds in the
// repo that is genuinely an equality rather than a ratchet, and the reason is that every module
// here is one the engine is *required* to read: there is no honest nonzero value, so a budget
// would only be a place for a regression to hide. Compare `unsupportedCeiling`, which is a
// ceiling precisely because it drains gradually.
//
// The failure message buckets by error string and names the first few modules per bucket, because
// a bare count is not a work plan — the nine that made this test were one bucket, and knowing the
// bucket was `constant expression required` is what turned "nine rejections" into "extended-const
// has no gate".
func TestEveryCorpusModuleDecodesUnderFullFeatures(t *testing.T) {
	m, err := xcorpus.Load(corpusDir(t))
	if err != nil {
		t.Fatalf("loading the committed corpus: %v", err)
	}

	// Vacuity floor, and it is the whole risk with a walk like this: an empty corpus is accepted
	// by every decoder ever written, so a manifest that failed to load rows, a blob that came
	// back short, or a future refactor that filtered the population would report a perfect green
	// while asking nothing. Floored on both axes because they fail independently — 1954 rows of
	// zero-length images would clear a module count and assert nothing, since an empty image is
	// rejected for a reason that has nothing to do with the grammar.
	//
	// The exact figure is pinned beside the floor rather than instead of it (grave #105): the
	// corpus is a committed snapshot, so its size is knowable exactly, and a floor alone stayed
	// green through a 6% silent loss in `keywordgen`. If a regeneration changes this number the
	// diff is the finding and the constant is updated with it.
	const wantModules = 1954
	if len(m.Modules) != wantModules {
		t.Errorf("corpus has %d modules, want exactly %d (wabt %s, suite %s): the committed "+
			"snapshot's population is knowable exactly, and a walk over a smaller one clears "+
			"every floor while covering less than its name claims",
			len(m.Modules), wantModules, m.WabtVersion, m.SuiteRev)
	}
	if len(m.Modules) < 1000 {
		t.Fatalf("corpus has %d modules: a decoder accepts an empty population perfectly, so "+
			"this walk must be over a plausibly-sized one before its green means anything",
			len(m.Modules))
	}
	bytes := 0
	for _, mod := range m.Modules {
		bytes += len(mod.Image)
	}
	if bytes < 100_000 {
		t.Fatalf("corpus images total %d bytes across %d modules: an image population with no "+
			"bytes in it exercises the header check and nothing else", bytes, len(m.Modules))
	}

	d := &binary.Decoder{Features: allFeaturesOn(t)}
	type bucket struct {
		n    int
		some []string
	}
	buckets := map[string]*bucket{}
	rejected, walked := 0, 0
	for _, mod := range m.Modules {
		walked++
		if _, derr := d.DecodeModule(mod.Image); derr != nil {
			rejected++
			b := buckets[derr.Error()]
			if b == nil {
				b = &bucket{}
				buckets[derr.Error()] = b
			}
			b.n++
			if len(b.some) < 3 {
				b.some = append(b.some, mod.File+":"+itoa(mod.Line))
			}
		}
	}

	// The floor that counts, and it is here rather than above because that is the lesson: the
	// population floors before the loop check `len(m.Modules)`, which is the *corpus*, not what
	// this walk consumed. Written that way first, and slicing the range to `m.Modules[:0]` left it
	// **green** — a control that could not fail on the one input it exists to reject, found by
	// writing the falsification and watching it not fire (*a control isn't born until it has been
	// watched die*). The population being right says nothing about the loop having run over it, and
	// no assertion downstream of a zero-iteration loop can tell the difference.
	if walked != len(m.Modules) {
		t.Fatalf("walked %d of %d modules: the population floors above measure the corpus, not "+
			"this loop, so a filtered or short-circuited walk passes them and then agrees with "+
			"everything", walked, len(m.Modules))
	}

	if rejected == 0 {
		t.Logf("%d modules, %d image bytes: all accepted under the tracked union (wabt %s, suite %s)",
			len(m.Modules), bytes, m.WabtVersion, m.SuiteRev)
		return
	}

	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return buckets[keys[i]].n > buckets[keys[j]].n })

	t.Errorf("%d of %d corpus modules were rejected with every gate on. Each is a module a "+
		"conforming producer emitted from a must-succeed suite module, so each is a module the "+
		"engine is required to accept — and **no board can see this**: every one of the suite's "+
		"green vectors is a rejection, so a decoder that wrongly rejects scores full marks "+
		"(contract §9 G-3). Bucketed largest first; the bucket is the work plan.",
		rejected, len(m.Modules))
	for _, k := range keys {
		t.Errorf("  %4d  %s\n\t  e.g. %v", buckets[k].n, k, buckets[k].some)
	}
}

// TestCorpusGateDeclinesAreNamedAndComplete is the same walk in v0's actual posture, and it asks
// a different question: with the gates *off*, is every rejection a **gate decline**?
//
// The distinction from the test above is the point of having both. That one asks whether the
// engine can read the tracked union; this one asks whether the default configuration's refusals
// are all attributable to configuration. A rejection here that is not a feature decline is a
// decoder defect wearing a gate's clothes — it would be invisible under full features (where
// nothing is declined) and invisible on the board (where nothing is accepted), so it is exactly
// the residue the two boards cannot cover between them.
//
// It also pins the #5 ruling over the whole population rather than over one probe: a gate-off
// engine must reject a gated module, and must **not** describe it as malformed. 692 modules is
// 692 chances for a gate to manufacture malformedness, and this is the only place that number of
// messages is ever read.
func TestCorpusGateDeclinesAreNamedAndComplete(t *testing.T) {
	m, err := xcorpus.Load(corpusDir(t))
	if err != nil {
		t.Fatalf("loading the committed corpus: %v", err)
	}
	if len(m.Modules) < 1000 {
		t.Fatalf("corpus has %d modules: too few for this walk's green to mean anything", len(m.Modules))
	}

	// v0's posture: the zero value, which is what a caller who configures nothing gets.
	//
	// No `walked` counter here, unlike the test above, and the difference is the reason the sweep
	// after that grave was worth running: this walk's floor is on **declines**, which can only
	// accrue from an iteration that actually happened, so a filtered or zero-length range fails
	// `declined < 500` on its own. The floor above measured the *population* and was therefore
	// blind to its loop; this one measures work done and is not. Adding the counter anyway would
	// be one concept with two triggers (#82), and saying so is the honest end of the sweep — *a
	// sweep that knows where the class doesn't apply is a sweep that understood the class*.
	d := &binary.Decoder{}
	declined, other := 0, 0
	byFeature := map[string]int{}
	for _, mod := range m.Modules {
		_, derr := d.DecodeModule(mod.Image)
		if derr == nil {
			continue
		}
		// `errors.Is` against the exported sentinel, not a string match: the message is testimony
		// and is checked separately below, but the *classification* has to come from the error
		// chain or this test would score a decoder that merely says the right words.
		//
		// This was a hand-rolled Unwrap walk with a `//nolint:errorlint,err113` on it, which was
		// the tell: `featureErr` wraps with `%w` (sections.go), so `errors.Is` has always been the
		// direct expression of the question. A suppression is a claim that the linter is wrong
		// about a deliberate design, and here it was covering a reimplementation of the function
		// the linter was pointing at — *noticed-and-named, or not at all* cuts both ways, and the
		// naming is what exposed it.
		if !errors.Is(derr, binary.ErrFeatureDisabled) {
			other++
			t.Errorf("%s:%d was rejected with %v, which is not a feature decline: with every gate "+
				"on this module decodes (the other test in this file asserts it), so a rejection "+
				"here that names no feature is the decoder refusing a valid module for a reason "+
				"its configuration does not explain", mod.File, mod.Line, derr)
			continue
		}
		if msg := derr.Error(); containsWord(msg, "malformed") {
			t.Errorf("%s:%d: %q says malformed for a module Wasm 3.0 defines — gates partition "+
				"acceptance, they never redraw the grammar (#5)", mod.File, mod.Line, msg)
		}
		declined++
		byFeature[derr.Error()]++
	}

	// The floor here is on the *declines*, and it is a floor rather than an equality because this
	// number legitimately falls as gates default on — the opposite direction from the equality
	// above. Its purpose is to catch the walk going quiet: if the decoder started accepting gated
	// constructs with the gates off, every module would pass and this test would report a serene
	// green for the accept-and-ignore failure #48 was about.
	if declined < 500 {
		t.Errorf("only %d of %d modules were declined with every gate off, want >=500: this walk "+
			"passing quietly is the accept-and-ignore failure (#48), where a gate-off engine reads "+
			"a gated construct and says nothing", declined, len(m.Modules))
	}
	if other == 0 {
		keys := make([]string, 0, len(byFeature))
		for k := range byFeature {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return byFeature[keys[i]] > byFeature[keys[j]] })
		t.Logf("%d of %d modules declined by name in v0's posture:", declined, len(m.Modules))
		for _, k := range keys {
			t.Logf("  %4d  %s", byFeature[k], k)
		}
	}
}

// containsWord is a substring test, spelled out so the message assertion in this file does not
// pull in `strings` for one call and so the intent — "does the message contain this word" — is
// stated rather than read off an import.
func containsWord(s, word string) bool {
	for i := 0; i+len(word) <= len(s); i++ {
		if s[i:i+len(word)] == word {
			return true
		}
	}
	return false
}

// itoa formats a line number. `strconv` for one call site in a failure message is a dependency the
// message does not need.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
