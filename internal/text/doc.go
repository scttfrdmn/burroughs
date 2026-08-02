// Package text holds the wat text format's lexer, the machine-derived keyword table
// (decision 0009) it lexes against, and the parser — #8, decomposed into the strata of
// #62 (module-field skeleton, landed), #63, and #64.
//
// The 2/3 seam is drawn at **defect ownership, not surface form** (Scott's ruling on #63's
// pre-registered forecast). Measuring the landed parser against the board found 336 of 390
// non-passing text vectors *folded in form and flat in defect* — `expr1: plaininstr
// expr_list` (parser.mly:814) merely transports the token stream to a defect that lives in
// an immediate reader — so that minimal arm is #63's, giving 353/37, and #64 keeps the
// desugaring families the reference itself marks `/* Sugar */`.
//
// # The table's deferral, discharged
//
// This section used to declare a table with no engine consumer — generated, committed, and
// read by nothing but this package's own tests, which is the classification question
// *unreachability is a grave only when it's silent* (#6) asks about a constant, asked about
// a table. It was declared and tracked at #53, and the discharge is what the section records
// now: the lexer landed, #53 is closed, and `keywords` is read by lexing code rather than by
// tests alone. The sequencing was Scott's and it paid — the table existed *before* its
// consumer, so the `unknown operator` bucket was earned against an authority rather than
// argued with against a hand-written set.
//
// Kept rather than deleted because the deferral's *shape* is the reusable part, and this
// package is now running the same play one layer up: TestUTF8DecodeSitedOnlyAtNameAndVar
// (utf8position_test.go) is a control pre-registered against a parser that does not exist,
// and it is honest for the same reason the table was — named at its site, tracked at #62,
// and expiring by mechanism rather than by memory.
//
// # Why the linter is quiet about it, measured rather than assumed
//
// Two independent silences cover the `keywords` map, and the interesting one is not the
// one this comment first claimed.
//
//   - **The engine reads it**, at lexer.go:385, and the tests read it too. When this
//     section was written only the second was true and it said so; the lexer's arrival made
//     "read by nothing but this package's own tests" false, and a measurement left standing
//     after its subject moved is a claim, not a measurement. Either way `unused` is
//     *correct* to say nothing — the map has consumers. This is not a suppression at all.
//   - **`.golangci.yml` sets `exclusions.generated: lax`**, which excludes files whose
//     first line carries the `Code generated ... DO NOT EDIT.` marker. That silence is
//     automatic, was not requested for this file, and is the one that would *remain* if
//     the tests were deleted.
//
// Measured by stripping the marker and by removing the test file, in both combinations:
// marker present → 0 issues either way; marker stripped with the tests gone → `var
// keywords is unused`. That measurement was taken when the tests were the only consumer, and
// the conclusion it supported — that deleting keywords_test.go would silently take the
// package from "table with a consumer" to "table with none" — **no longer holds**, because
// the lexer is now a consumer the tests' deletion cannot remove. The generated-file exclusion
// is still the silence that would remain, so the section's point about it stands; the risk it
// was worried about is what the lexer's arrival retired.
//
// Naming that is *suppression discipline: noticed-and-named, or not at all* (decision
// 0005) applied to a suppression nobody wrote. Removing the exclusion is not the fix — it
// exists for good reason across every generated table. The fix is a control that does not
// depend on a linter's opinion, which is the next section.
//
// Recorded this way because the first version of this paragraph asserted the exclusion was
// why the linter stays quiet, reasoning from `internal/gen`'s prose about a different
// question rather than from this file. It happened to be true and was not the whole truth,
// which is the shape of a claim that survives review: *print-don't-trust* applies to a
// comment's factual claims as much as to an error message's.
//
// # Why the committed table needs a control of its own
//
// `make keyword-drift` re-runs the extraction and asserts agreement — but it requires
// third_party/spec vendored, so it cannot live in `make check` (0007's consequence, in
// this grammar). Which leaves a gap with a name: `DO NOT EDIT` is a *request*. A hand
// edit adding `get_local` to keywords.go would pass `make check` on a fresh clone, and
// the eleven obsolete mnemonics are the one set whose absence three of #53's
// oracle-covered vectors score against directly.
//
// So keywords_test.go asserts the committed file's properties from the consuming side,
// with no reference needed: a size floor, the eleven absences, and the shape the
// authority's own `keyword` production requires. Same subject as keywordgen's checks and
// a different artifact — that one judges a fresh extraction, this one judges the file in
// the tree. A drift check and an integrity check are not the same question, and only one
// of them can be asked without a fetch.
package text
