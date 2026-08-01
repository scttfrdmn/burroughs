// Package text will hold the wat text format's lexer and reader. Today it holds one
// thing: the machine-derived keyword table (decision 0009).
//
// # The table has no engine consumer yet, and that is declared rather than incidental
//
// keywords.go is generated, committed, and read by nothing but this package's own tests.
// That is deliberate and it is Scott's sequencing: the table exists *before* the code
// that consumes it, so the 555-vector `unknown operator` bucket has an authority to earn
// against rather than a hand-written set to argue with. The consumer — maximal munch
// across the `keyword` and `reserved` shapes, the two `unknown operator` producers, and
// `'$'(id)` so identifiers are not mis-lexed — is #53's next increment.
//
// This paragraph exists because *unreachability is a grave only when it's silent*
// (ruling on ErrTrailingData, #6). A table nothing reads is the same classification
// question as a constant nothing reads, and it gets the same answer only if the
// deferral is named at the definition site with a tracking issue. So: declared, tracked
// at #53, and the tripwire is keywords_test.go.
//
// # Why the linter is quiet about it, measured rather than assumed
//
// Two independent silences cover the `keywords` map, and the interesting one is not the
// one this comment first claimed.
//
//   - **The tests read it.** keywords_test.go loops over `keywords`, so `unused` is
//     *correct* to say nothing: the map genuinely has a consumer, just not one in the
//     engine. This is not a suppression at all.
//   - **`.golangci.yml` sets `exclusions.generated: lax`**, which excludes files whose
//     first line carries the `Code generated ... DO NOT EDIT.` marker. That silence is
//     automatic, was not requested for this file, and is the one that would *remain* if
//     the tests were deleted.
//
// Measured by stripping the marker and by removing the test file, in both combinations:
// marker present → 0 issues either way; marker stripped with the tests gone → `var
// keywords is unused`. So the deletion of keywords_test.go is a change that would silently
// take this package from "table with a consumer" to "table with none", with nothing
// objecting.
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
