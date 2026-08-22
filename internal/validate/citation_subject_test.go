// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The description-from-source tripwire, and it exists because inference was measured against the
// reference and lost.
//
// # The measurement
//
// The citation domain repair (#333) brought four more files under the range pins, and re-pinning
// required a description for each newly covered range. Six were written for files whose cited lines
// had not been read — from what the *Go* code around the citation appeared to be doing, which is the
// available reading and the wrong one. **Five of the six were wrong**, and the sixth was right
// because it was copied from a description someone had written from the reference:
//
//	written                                | valid.ml actually says
//	---------------------------------------+-----------------------
//	`check_elem`'s active arm              | the `TableInit` arm
//	the segment rules' region              | the table arms
//	`check_block`                          | `BrTable`
//	the blocktype lookup                   | `check_valtype`
//	the operand-stack type rule            | `check_block`
//
// All five *resolved*: every range was well-formed, inside the file, and (where keyable) contained
// its subject's message site. Nothing in this package could see them, because every existing citation
// check asks where a range points and none of them asks what the prose beside it *claims* the range
// contains. They were caught by reading `valid.ml` before the commit — a procedure, and one whose
// five-in-six error rate is the argument for not leaving it as one. (Ruling: Scott, PR #335 relay;
// the law is in `docs/laws/citations.md`.)
//
// # What is checked, and how the domain avoids being a list
//
// A citation line that names a reference-defined identifier in backticks is asserting a relationship
// between the two, and there are exactly two honest ones: the identifier is *in* the cited range, or
// the cited range is *inside* the identifier's own definition (a citation to three lines of
// `check_module`'s body names `check_module`). Either satisfies this check; neither holds for any of
// the five rows above.
//
// Both halves of the trigger are derived rather than enumerated, which is #333's lesson applied at
// the point of construction rather than after the grave:
//
//   - the **files** are globbed, and unlike `citationFiles` this glob keeps `_test.go` — a citation
//     is a citation wherever it is written, and the five specimens were written in a test file's pin
//     table;
//   - the **candidate identifiers** come from `valid.ml`'s own top-level bindings and constructor
//     arms, so `checkOffset` and `binary.Limits` are not candidates and a reference function this
//     package has never mentioned becomes one the moment a comment names it.
//
// # What it cannot see, stated because a coverage claim cannot check itself
//
// The window is **one line**. A description whose identifier wraps onto the line above is not keyed
// by this check — it lands in the residue count instead, which is pinned, so a wrap moves both pins
// and arrives loudly rather than silently. A two-line window was tried and is *worse*: on
// `ErrUnknownTag`'s comment it joined a clause naming the tag arm's own reference function to the
// next line's citation of the `lookup` family at `:40-49`, and reported that correct citation as
// wrong. A window wide enough to catch a wrap is wide enough to cross a clause boundary, and one of
// those two errors is silent.
//
// Residue is also where a genuinely region-shaped citation lands — `valid.ml:618-651` is "the table
// arms", a description with no single subject to name — and that is a fact about the reference rather
// than a gap. The count keeps it from being a silent exclusion.
//
// A line carrying several ranges is checked against *any* of them rather than each: two subjects and
// two ranges on one line are not claiming a pairing, and a per-clause parse is what a pairing check
// would need. Stated because it means a swapped pair inside one line survives this check.
//
// # It enforces agreement, not provenance — a lucky inference passes
//
// The rule this holds is *the description is written by reading the cited lines*, and that is a claim
// about **how the description was produced**, which no test can see. What this check asks is whether
// the description *agrees* with the lines — so it catches a description that disagrees, and a
// description inferred from the surrounding Go code that happened to land on the right subject passes
// it untouched. Agreement is the correct proxy and this is the right control; it is not the rule, and
// the gap between them is one-directional in the reassuring direction.
//
// Worth being exact about how the five specimens were actually found, since it is the same gap: they
// were caught by **reading `valid.ml`**, before any of this existed. Six descriptions, five wrong, and
// had the sixth been inferred rather than copied it would have been a sixth pass here with no more
// provenance than the five. So the measurement that minted this control is a measurement this control
// could not have taken — which is the honest statement of what it buys: it makes a *wrong* inference
// findable by machine, and leaves a *right* one indistinguishable from a reading. (Ruling: Scott, PR
// #337 relay.)
func TestRangeCitationSubjectsAreReadFromTheReference(t *testing.T) {
	ref := testenv.RequireSpecRef(t, testenv.RefValidML)
	lines := strings.Split(ref, "\n")
	defs, arms := refSubjects(lines)

	// Vacuity, and pinned exactly rather than floored — the same argument `wantCategories` makes a
	// file over: the reference is fetched at a pin, so upstream growing its vocabulary is a fact for
	// a reader to record rather than churn to absorb. A floor here would also be the wrong shape,
	// since the failure this guards is a regex that stopped matching and that failure is a *collapse*,
	// which any floor catches while hiding the interesting case of a few bindings going missing.
	const wantDefs, wantArms = 88, 170
	if len(defs) != wantDefs || len(arms) != wantArms {
		t.Fatalf("parsed %d top-level binding(s) and %d constructor arm(s) from %s, want %d and %d — "+
			"the candidate set is this trigger's domain, so a shrunken one makes citations unkeyable "+
			"and an empty one makes this test agree with everything",
			len(defs), len(arms), testenv.RefValidML, wantDefs, wantArms)
	}

	tickRe := regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_' ()]*)`")
	keyed, residue := 0, 0
	for _, file := range citingFilesWithTests(t) {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			var ranges [][2]int
			for _, m := range rangeRe.FindAllStringSubmatch(line, -1) {
				lo, hi := atoiOrZero(m[1]), atoiOrZero(m[2])
				ranges = append(ranges, [2]int{lo, hi})
			}
			if len(ranges) == 0 {
				continue
			}
			var subjects []string
			for _, m := range tickRe.FindAllStringSubmatch(line, -1) {
				// `Select (Some ts)` names an arm and its payload; the arm is the subject.
				name := strings.Fields(m[1])
				if len(name) == 0 {
					continue
				}
				if _, ok := defs[name[0]]; ok || arms[name[0]] {
					subjects = append(subjects, name[0])
				}
			}
			if len(subjects) == 0 {
				residue++
				continue
			}
			for _, name := range subjects {
				keyed++
				if subjectAnswers(lines, defs, name, ranges) {
					continue
				}
				t.Errorf("%s:%d describes %s as %q and no reading of that citation holds: %q is "+
					"neither written inside %v nor is any of those ranges inside its own definition "+
					"at %v. The description was written from the code around the citation rather "+
					"than from the cited lines",
					file, i+1, testenv.RefValidML, name, name, ranges, defs[name])
			}
		}
	}

	// Both pinned, and separately, for the reason the sibling counts are: a description that stops
	// naming its subject moves a row from the checked column into the excused one, and that has to be
	// louder than a passing test with one fewer assertion in it.
	//
	// 30 keyed and 10 residue. **Six became keyed when the rule was minted** — `TableInit`, `BrTable`,
	// `Select (Some ts)`, `check_block`, `check_limits` and `check_memorytype`, the same list as the
	// five wrong descriptions plus the one that was right. A description that names its subject is a
	// description someone had to read the reference to write, so moving a row into the keyed column is
	// the repair and not bookkeeping.
	//
	// **The three after that are the rule working on new prose rather than on a repair**, and they are
	// worth distinguishing. The `check_elem` slice added two range citations, one in the engine comment
	// and one in that slice's own test header, and the engine comment's first draft named its two
	// reference functions *without backticks* — a description that resolved, described the right lines,
	// and landed in residue anyway, because an unbackticked name is invisible to the subject extractor.
	// The pin caught it as a residue increment. Backticking both moved the row into the checked column
	// and keyed it twice, since both identifiers are written inside the range that comment cites. So the
	// failure this pin catches in practice is not a wrong description but a *correct description the
	// check cannot read*, which is a fourth thing to be exact about after the three the header lists.
	//
	// Note what this paragraph does not do: it does not spell those two ranges. An earlier draft did,
	// and each draft that named a range beside a backticked reference function **keyed a further row**,
	// so the pin chased the prose describing it — 30, then 31, then 32, one per revision. The count is a
	// fixed point of what the file says about itself, and the way out is for commentary about citations
	// to describe them rather than perform them. Left as a note because the loop is not obvious until it
	// has been walked into, and the shape generalizes: an instrument whose domain includes its own
	// documentation has no stable reading while that documentation quotes its subject.
	//
	// The residue, in full, because an excused row that nobody can enumerate is an exclusion:
	// `vec.go`'s four section comments (:885-937, :906-908, :938-955, :663-686), `bulk.go`'s table-arms
	// region and this file's sentence about it (:618-651), `validate.go`'s slice-4 summary pointing at
	// `instr.go` (:442-446), and three rows in `vec_authority_test.go`'s own prose (:373-378, :390-393,
	// :41-42) that describe what another instrument keys rather than naming a reference subject.
	// **#343's `match.ml` port moved both figures, and the split is the rule working as designed.**
	// The relation's own rules each cite the reference lines they transcribe and name the reference
	// function in the same breath, so they land keyed without anyone aiming for it — which is the
	// point: a rule ported by reading its arms cannot help naming them. The residue increment is the
	// other half of the same file, the doc blocks that argue about *representation* rather than
	// transcribe a rule — why an index-keyed port loses a disjunct, why one function alone reads the
	// null bit, why the group extent had to be retained. Those cite the reference to say what it
	// does differently, so they resolve and describe correctly and key nothing, exactly as the
	// `check_elem` note above predicts.
	//
	// Deliberately not spelled: which ranges those are. That is the fixed-point trap this header's
	// own note walked into at 30-31-32, and it applies to a paragraph explaining an increment just
	// as much as to one explaining a repair.
	// Two keyed and three residue, against `match.go`'s five ranges in total.
	//
	// **#359's reference-type slice moved the keyed figure by seven and the residue by nothing, and
	// that is the cleanest reading this pin has produced.** Six range citations arrived; every one of
	// them sits on a line that also names its subject in backticks, so nothing landed in the excused
	// column, and one of the six names two subjects — a rule shared by two opcodes, cited once and
	// keyed twice, which is the per-subject counting working rather than a double count. A slice
	// written by transcribing arms cannot help naming them, which is the same observation the
	// paragraph above makes about the relation's port; the difference is that this time the residue
	// did not move at all, so the increment is exactly the arm count and nothing else.
	//
	// Deliberately not spelled here either: which lines those are, for the reason two paragraphs up.
	//
	// **Slice 7's GC-instruction port moved keyed by eleven and residue by fifteen, and the figure
	// worth reading is neither of those — it is the gap.** The two columns summed to 55 against 41
	// ranges before; they sum to 81 against 67 now, so the 14-row excess of keyed-subjects over
	// ranges is *unchanged*. That excess is entirely multi-subject ranges — one citation whose line
	// names two of the reference's identifiers, keyed twice on purpose — so an unchanged gap says
	// every one of the twenty-six new citations names exactly one subject or none. A slice folding 31
	// opcodes onto 21 arms is where a shared citation would have been most expected, and this is the
	// cheap check that the folding did not quietly produce one: an arm serving two opcodes cites the
	// reference's one rule, which is one subject, and the many-to-one is carried by the `switch`
	// rather than by the prose.
	//
	// The residue moving *more* than the keyed column is the second reading, and it is the expected
	// direction for this slice rather than a warning. Two thirds of `gc.go`'s doc blocks argue about
	// representation — why a parameterized constructor makes a divergence unrepresentable, why one
	// wire-format fact moved into `binary`, which of five witnessed strings the corpus never carries
	// — and a block that cites the reference to say what this port does *differently* resolves
	// correctly and keys nothing, which is the `check_elem` note at the top of this header working as
	// described for a third slice.
	//
	// **Slice 8 moved keyed by seven and residue by nothing, and the gap is still 14.** Seven new
	// citations, all in `ref.go`, all naming exactly one of the reference's identifiers on the line
	// that cites it; the two columns sum to 88 against 74 ranges, so the excess of keyed subjects over
	// ranges is the same 14 multi-subject rows it was before slice 7 and after it. Three slices now
	// agree that the excess is a property of a fixed set of shared citations rather than something a
	// port accumulates, which is the reading the paragraph above wanted a second instance of.
	//
	// **One of the seven arrived by repair, and it is this header's fourth category again.** The
	// `peek_ref` block's first draft named its subject as the reference spells the call —
	// `peek_ref 0 s e.at` in backticks — and landed in residue: the extractor reads a backticked
	// *identifier*, and the dotted argument makes the whole span match nothing, so a description that
	// resolved and described the right lines was invisible for a reason that has nothing to do with
	// whether it was true. Backticking the bare name and saying in the block why the call form is not
	// used moved it into the keyed column. That is the same failure the `check_elem` note records for
	// an unbackticked name, now with a second spelling: the check reads names, and every way of
	// writing a name that is not a name is a residue increment waiting to be misread as a gap.
	// **Slice 9 moved keyed by two and residue by two against four new ranges, and one of the two keyed
	// arrived by repair — this header's fourth category, in a third spelling.** `returnCall`'s block
	// names `ReturnCall` on the line that cites it and keyed on the first reading.
	// `returnCallIndirect`'s did not, and the reason is the extractor's character class: it admits
	// letters, spaces, apostrophes and parentheses, so `` `ReturnCall x` `` keys on its head, but a
	// **comma** is not in the class, so `` `ReturnCallIndirect (x, y)` `` matched nothing and a correct
	// description of the right lines sat in the excused column. Writing the arm's name alone and its
	// payload outside the backticks moved it across. The three spellings that have now done this are an
	// unbackticked name (`check_elem`), a dotted call form (`peek_ref 0 s e.at`) and a comma inside a
	// payload — all three descriptions true, all three invisible, which is why this pin is read as
	// *which column* rather than as a total.
	//
	// **The repair also demonstrated something the earlier two did not: the range and the name must
	// share a line.** The first attempt put the payload on a continuation line and left the citation
	// alone on the second, which keyed nothing — the loop above reads a line at a time. That is worth a
	// sentence because the fix looked applied and the figure did not move, which is the shape of a
	// repair confirmed by its author rather than by the instrument.
	//
	// The two residue increments are `requireTailResults`' `:546-549`, whose line names `call` and
	// `call_indirect` — this format's instruction names, not the reference's `Call`/`CallIndirect`
	// arms — and `indirectTarget`'s `:560-565`, which sits alone above a quoted block.
	//
	// **Three more rows arrived from the slice's witness file, and they are not the fixed-point trap
	// this header warns about.** `tailcall_test.go` cites `:546-549`, `:544-550` and `:560-565` to say
	// which rule each battery witnesses; the `:544-550` line names `func` — the reference's own lookup —
	// so it keys, and the other two land in residue. That is prose citing a *rule*, which is the domain
	// this pin is for. The trap is prose citing a *citation*, which is why the paragraphs above describe
	// their subjects instead of quoting the ranges: this pin's domain includes its own file.
	//
	// So the final split is keyed 62 and residue 33, summing to 95 against 78 ranges — an excess of 17
	// where three slices running read 14. **The increment is exactly those three test-file rows**, which
	// this pin counts and `wantRanges` does not, its domain being engine files only. The excess is
	// therefore two things added together — multi-subject ranges and test-prose ranges — and it held at
	// 14 for three slices because none of them cited a rule from a test file. It moves here for that
	// reason and not because a citation went unread, which is the difference the arithmetic alone
	// cannot say.
	// **The split above was slice 9's. Slice 10's is keyed 80 and residue 34, and the interesting part
	// is that both of this header's recorded spellings recurred inside the slice's own new file.**
	// Per file: `exception.go` arrives at 8/0 and `exception_test.go` at 5/0, `elem_test.go` goes 1 to
	// 3, `module.go` goes 17/2 to 20/3, and the other sixteen files hold — so +18 keyed and +1 residue,
	// against `wantRanges`' +9.
	//
	// The recurrences, both caught here and neither prevented by the paragraphs above them:
	// `tryTable`'s block first read ``TryTable (bt, cs, es)``, which is the **comma inside a payload**
	// exactly as slice 9 diagnosed it, in a file written three paragraphs' distance from the warning.
	// And `tagTypeAt`'s citation first sat on the line naming `deftype_of_typeuse` and
	// `functype_of_comptype` — a **fifth category**, and the first that no rewording repairs: both are
	// the reference's own identifiers, correctly spelled, and both are defined in the type algebra
	// rather than in `valid.ml`, whose definitions and arms are the only names `refSubjects` reads. The
	// repair was to move the range up beside `tag c x`, which *is* one of them.
	//
	// **That repair produced the finding worth more than either recurrence: a citation can be destroyed
	// by reflowing a paragraph, and the destruction reads here as an improvement.** The first attempt
	// wrapped the citation itself — ``valid.ml:572-`` ending one line and ``575`` opening the next — so
	// `rangeRe` matched nothing on either, and the row left the residue column without entering the
	// keyed one: keyed held while residue fell by one. **Residue falling is therefore ambiguous in this
	// pin alone** — a row repaired into `keyed` and a row deleted from the domain move it the same
	// direction — and what separated them was `wantRanges` next door falling from 87 to 86 in the same
	// run. The two pins are read together for this reason and not merely for arithmetic, and the
	// wrapping is now annotated at the site, since reflowing that paragraph is a semantic act.
	//
	// `module.go`'s residue increment is conformance rather than drift: the new `check_tag` phase line
	// joined the two unbackticked phase-list rows already in `modulePre` — `check_type → check_rectype`
	// and `check_global` — making three. Backticking all three is an edit to two other slices' prose
	// and is not this slice's to make; backticking only the new one would leave the local convention
	// with a single exception, which is worse than either.
	//
	// So the excess over `wantRanges` is 27 where slice 9 read 17, and it decomposes exactly: **six**
	// test-file ranges this pin's domain contains and `wantRanges`' does not (`exception_test.go`'s
	// five, `elem_test.go`'s one) plus **four** extra subjects from three multi-subject lines —
	// `module.go`'s `check_externtype`/`ExternTagT`/`check_tagtype` line keys three subjects for one
	// range, `exception.go`'s `pop`/`match_stack` line two, and `elem_test.go`'s
	// `check_elem`/`check_const` line two. Six and four is the whole +10, which is the check that no
	// range went unread while the totals happened to agree.
	// **#328's split is keyed 85 and residue 35, all four new ranges in `module.go` — 20/3 to 25/4 —
	// and the reading is the multi-subject excess moving for the first time since slice 8.** Four
	// citations, five keyed subjects: `check_externtype`/`ExternFuncT` on the import arm's line and
	// `check_func`/`func_type` on the declaration loop's, two lines each naming a definition *and* the
	// thing inside it that the range shows. Both are rules whose whole content is "resolve this index
	// through that lookup", so the lookup's name is unavoidable in any true description of them — which
	// is the slice-7 observation about shared citations arriving from the other direction: the excess
	// grows when a rule's statement *needs* two of the reference's names, not when one name is reused.
	//
	// The single residue increment is the message line inside Rule C, and it is this header's third
	// category rather than a fourth: the sentence says the message is the reference's own and cites the
	// two lines it is copied from, naming no identifier because the thing being pointed at is a *string*.
	// A citation whose subject is a sentence keys nothing and is right not to.
	//
	// **#413's start-section slice is keyed 86 and residue 36 — two new ranges, one into each column,
	// and the residue one is this header's third category for the second time.** `module.go`'s
	// `moduleStart` block cites the whole of `check_start` on a line naming it, so it keys — and the
	// range is *described* here rather than quoted, because this pin's domain includes this file and
	// the first draft of this paragraph quoted it and incremented the figure it was explaining.
	// `validate.go`'s `ErrStartFunction` cites the rule's two message lines on a continuation line
	// whose subject is the
	// reference's *string* — the sentence names `check_start` one line above and the citation sits with
	// the words being quoted — so it lands in residue for the same reason Rule C's message line did,
	// and for the same reason it is right to: what is being pointed at is a sentence, not an
	// identifier. Restating it would mean moving the range onto the `check_start` line and separating
	// the citation from the text it certifies, which is the wrapping hazard two paragraphs up in
	// reverse.
	// **#419's split is keyed 87 and residue 37 — two new ranges, one into each column, and the residue
	// one is the *unbackticked phase line* category rather than either of the two this header has
	// recorded.**
	// `module.go`'s tables loop opens with the whole of `check_table` cited on a line that names it
	// without backticks — quoted here as a description rather than as the range, because this pin's
	// domain includes its own file and the first draft of this paragraph incremented the figure it was
	// explaining, exactly as the fixed-point paragraph above records happening at 30-31-32. It joins the
	// three unbackticked phase-list rows already in `modulePre` that slice 10's paragraph above
	// describes — `check_type → check_rectype`, `check_global`, and `check_tag`. So the description
	// *does* name the reference's own identifier and this pin cannot see it, the backtick being half the
	// trigger.
	//
	// Written that way deliberately, which is the part worth pinning rather than repairing. Backticking
	// this one row alone would make the local convention inside one function's body read four ways
	// where it currently reads one, and the alternative — backticking all four — is an edit to three
	// other slices' prose, which is the same judgement slice 10 recorded and declined for the same
	// reason. What the residue count buys here is that the choice is *counted*: a fourth unbackticked
	// phase row is a number moving, not a silent convention.
	//
	// The keyed one is in `global_test.go`, on a line naming `check_module` and citing the two lines of
	// its fold that put tables ahead of globals — the "cited range lies within the identifier's own
	// definition" reading, and the second test-file range this pin's domain holds that `wantRanges`'
	// does not. So the excess over that pin grows by one while both totals move by one, which is the
	// arithmetic slice 9's paragraph set up to be readable.
	//
	// **The `check_valtype` slice is keyed 97 and residue 39, and it is the first re-pin where the two
	// columns move by more than two between them.** Nine range citations arrive in the engine files and
	// two leave, for +7 on `wantRanges`; this pin counts *subjects* and includes the test files, so its
	// arithmetic differs from that one twice over and the difference is the point of having both. Where
	// they land:
	//
	//   - +2 keyed from `rectype_scope_test.go`, the slice's own control file: `check_subtype` and
	//     `check_subtype_sub` cited a second time, on the two lines that explain why the group's scope
	//     is resolved before the forward rule compares indices. Invisible to `wantRanges`, whose domain
	//     is the engine files, and the third test-file range this pin holds that it does not. The file's
	//     two `types.ml` ranges are invisible to *both*: this project's range trigger names one
	//     reference file, and a citation into `types.ml` — where `subst_of` and `roll_rectype` live, the
	//     two functions that make the prefix a prefix — is unchecked by anything. Recorded here because
	//     a citation no sweep can see is worth naming at the place a reader would expect it counted.
	//   - +8 keyed in the engine files. One line carries **three** — `check_reftype` is `check_heaptype`
	//     is `check_typeuse`,
	//     the three-step reduction the element segment's declared type goes through — which is this
	//     pin's first row keying more than two and is why a citation's *subject count* is not its
	//     count. Then one each for `check_rectype` (`instr.go`, on the scoped `check_valtype` helper),
	//     `check_subtype`, `check_comptype`, and `check_valtype` (the import descriptor's global arm),
	//     plus one net from `check_subtype_sub`: the range moved off the `checkTypes` header — where it
	//     keyed on `require` alone — onto the function named after it, where it keys on both.
	//   - +2 residue, both this header's third category. The `check_rectype` second-pass line cites
	//     `Lib.List32.iteri`'s two lines with the OCaml on the line *above* the citation, and
	//     `funcBody`'s cites `check_local`'s body on the line naming `Set`/`Unset` — constructor names
	//     the reference builds by `if defaultable t then Set else Unset` rather than matching on, so
	//     they are in neither map and the row keys nothing. Both point at code rather than at a string,
	//     which makes them the third category by *mechanism* and not by kind: what keys a row is a
	//     backticked identifier the reference *binds*, and a two-line citation whose identifier sits one
	//     line up is unreachable to a per-line trigger. Recorded rather than repaired, because pulling
	//     the citation up onto the OCaml line is the wrapping hazard this header names twice.
	//
	// This pin's total therefore moves +12 where `wantRanges` moves +7, and the excess over it goes
	// 30 → 35. Three of the five are the subject-count effect named above — +2 from the three-subject
	// reduction line, +1 from `check_subtype_sub`'s range landing on a line naming both it and
	// `require` — and two are the control file's, which `wantRanges`' domain excludes. The two pins
	// diverging by a number with a stated cause is what makes them two pins; a shared figure would have
	// absorbed all five silently.
	//
	// **#452's local-initialization slice is keyed 103 and residue 44, against nine new engine ranges,
	// and the excess over `wantRanges` goes 35 → 37 for a cause that is one shape counted twice.** Six
	// keyed and five excused sum to eleven where nine ranges arrived, and the +2 is that shape:
	// the reference's set and tee arms return the *same* second component, so a true description of
	// either one names both, and the two lines that say so each key twice. That is the slice-7
	// observation from the other side again — the excess grows when a rule's statement needs two of the
	// reference's names, not when one name is reused — and this time both extra subjects come from one
	// pair of arms rather than from a lookup.
	//
	// The five excused rows are worth splitting, because four are one mechanism this header has already
	// diagnosed and the fourth spelling of it is the sharpest yet:
	//
	//   - **Two cite a rule by quoting the reference's OCaml**, and a quoted expression keys nothing:
	//     `=`, `,` and `"` are outside the extractor's character class, so a backticked span carrying
	//     the rule *verbatim* matches nothing at all. The three spellings above are an unbackticked
	//     name, a dotted call form and a comma inside a payload; this is the same class boundary met by
	//     a description so faithful it transcribes the statement. **The repair and the description are
	//     in tension here** — dropping the quotation to name the arm would key the row and would delete
	//     the one form a reader can check against the reference without opening it — so both are
	//     recorded rather than repaired, which is what the residue column is for.
	//   - **One is a continuation line**, the init half's citation sitting a line below the identifier
	//     it describes: the "range and the name must share a line" category, and left alone because
	//     pulling the range up is the wrapping hazard this header names three times.
	//   - **Two carry no reference identifier because their subject is not one** — a sentence about
	//     *where in this engine* the undo is performed, and one about how the parameter half of the
	//     context is assembled here. A citation whose claim is about this side of the port names the
	//     reference to say what it does differently, which is the `check_elem` note at the top working
	//     as described for a fifth slice.
	const wantKeyed, wantResidue = 103, 44
	if keyed != wantKeyed || residue != wantResidue {
		t.Errorf("keyed %d range citation(s) by named subject and left %d as residue, want %d and "+
			"%d — recount and re-pin. A row moves from residue to keyed when its description starts "+
			"naming the reference's own identifier, which is the direction this test wants, and the "+
			"pin is how the move gets read rather than absorbed", keyed, residue, wantKeyed, wantResidue)
	}
}

// subjectAnswers reports whether a citation's ranges are consistent with naming `name`.
//
// Two readings, both derived from the reference: the identifier appears within a cited range, or a
// cited range lies within the identifier's own definition. The second is not a weakening — a comment
// citing `valid.ml:1168-1169` and calling it `check_module`'s export phase is describing two lines of
// that function's body, and the function's name is nowhere in them.
func subjectAnswers(lines []string, defs map[string][2]int, name string, ranges [][2]int) bool {
	word := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	span, defined := defs[name]
	for _, r := range ranges {
		if r[0] < 1 || r[1] > len(lines) || r[1] < r[0] {
			continue // malformed; TestReferenceRangeCitationsAreWellFormed owns that verdict
		}
		if word.MatchString(strings.Join(lines[r[0]-1:r[1]], "\n")) {
			return true
		}
		if defined && span[0] <= r[0] && r[1] <= span[1] {
			return true
		}
	}
	return false
}

// refSubjects parses `valid.ml`'s named subjects: top-level `let`/`and` bindings with the line span
// each occupies, and the constructor names that appear as match arms.
//
// Top-level means column zero. A local `let t1 = ...` inside a function body is not a subject a
// citation can name — the two the reference binds in `check_elem` are called `t1` and `t2`, and
// admitting them would key two rows on identifiers whose scope is four lines wide.
func refSubjects(lines []string) (map[string][2]int, map[string]bool) {
	bindRe := regexp.MustCompile(`^(?:let|and)\s+(?:rec\s+)?([a-z_][A-Za-z0-9_']*)`)
	armRe := regexp.MustCompile(`\|\s*([A-Z][A-Za-z0-9_']*)`)

	type binding struct {
		name string
		at   int
	}
	var order []binding
	arms := map[string]bool{}
	for i, l := range lines {
		if m := bindRe.FindStringSubmatch(l); m != nil {
			order = append(order, binding{m[1], i + 1})
		}
		for _, m := range armRe.FindAllStringSubmatch(l, -1) {
			arms[m[1]] = true
		}
	}
	defs := make(map[string][2]int, len(order))
	for k, b := range order {
		end := len(lines)
		if k+1 < len(order) {
			end = order[k+1].at - 1
		}
		// A name bound twice keeps its first start and its last end: `check_limits` is one binding,
		// but the reference does rebind a few helpers, and a span that ended at the first rebinding
		// would report a citation inside the second as unanswered.
		if span, ok := defs[b.name]; ok {
			defs[b.name] = [2]int{span[0], end}
			continue
		}
		defs[b.name] = [2]int{b.at, end}
	}
	return defs, arms
}

// citingFilesWithTests is citationFiles' domain plus this package's tests.
//
// Derived for #333's reason and *wider* than citationFiles for a reason of its own: the five wrong
// descriptions that minted this check were in `vec_authority_test.go`'s pin table, so a domain that
// excluded tests would have excluded every specimen. The two globs stay separate rather than one
// being widened — citationFiles feeds counts about the engine's citations, and folding test comments
// into those would move pins that mean something else.
func citingFilesWithTests(tb testing.TB) []string {
	tb.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		tb.Fatalf("globbing the package's sources: %v", err)
	}
	if len(files) < len(citationFiles) {
		tb.Fatalf("globbed %d .go file(s) and citationFiles holds %d — this domain is a superset of "+
			"that one by construction, so a smaller count means the glob ran somewhere else",
			len(files), len(citationFiles))
	}
	return files
}

// atoiOrZero is strconv.Atoi with the error dropped, safe here because rangeRe's groups are `\d+`
// and the caller bounds-checks the result against the reference's length anyway.
func atoiOrZero(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
