#!/usr/bin/env sh
# citecheck.sh — every citation a diff adds must resolve to the artifact it names.
#
# Two guessed issue numbers reached a working tree in consecutive PRs (`#283`, which resolved
# to the *wrong* artifact, and `#284`, which did not exist when it was typed and then happened
# to be the number the filing came back with). Both were self-caught; both were saved by luck,
# and the third would not be. So the check exists, and it is the mechanical half of a rule this
# repo already states in prose — *a deferral's citation outlives its subject*, *a self-citation
# must resolve like any other*, *fixtures cite the suite, and the citations are checked*.
#
# Usage:
#   scripts/citecheck.sh <rev>              # one commit, against its first parent
#   scripts/citecheck.sh <base> <head>      # a range, e.g. a PR's merge base to its tip
#   scripts/citecheck.sh --worktree [base]  # base (default `main`) against the working tree
#   scripts/citecheck.sh --pr <number>      # the PR's title and body, which need the network
#
# `--pr` scans prose instead of a diff and is the only form that runs checks 4 and 7 — closecheck.sh
# takes its PR the same way and for the same reason, both tools asking a question about the body GitHub
# will act on. The two forms are separate invocations rather than one flag, because a diff and a
# body are different populations and folding them would hide which one a verdict came from.
#
# The two revision forms match ratio.sh deliberately: read the sibling before writing the
# reader. Both tools answer a question about *a diff*, so both take a diff the same way.
#
# `--worktree` exists because the local case is the *uncommitted* one, and leaving it out would
# repeat the specimen this check is a sibling of: the #281 sweep ran `git grep`, whose domain is
# tracked files, and so excluded exactly the new unstaged region the defect lived in. A guessed
# issue number is in the working tree before it is anywhere else — that is where it is cheapest
# to catch and where a commit-range-only tool cannot look.
#
# ## What is binding and what is printed
#
# Seven checks, and they are not equally strong. Saying which is which is the point, because a
# tool that gates on existence while its name suggests it gates on correctness is testimony
# about itself:
#
#   1. **ADR citations resolve — binding, offline.** `decision NNNN` / `ADR NNNN` must have a
#      matching `docs/decisions/NNNN-*.md`. Needs no network, so this half runs everywhere and
#      always, including on a plane.
#   2. **Issue citations resolve — binding, needs the network.** Every `#NNNN` added by the diff
#      must be a real issue or PR in this repo. This is the half that catches a guessed number.
#   3. **`grave #N` carries `type:grave` — binding, needs the network.** The one *title-shaped*
#      claim in the set that has a mechanical oracle: a citation whose immediately preceding word
#      is "grave" must resolve to an **issue** (not a PR) labelled `type:grave`. `#283`'s failure
#      mode was resolving to the wrong *kind* of artifact, and existence alone cannot see that.
#   4. **No citation in a PR *body* names that PR — binding, needs the network, `--pr` only.** The
#      second title-shaped claim with a mechanical oracle, mechanical because it needs no judgement
#      about agreement: a body citing its own number is never intentional. Specimen on #339, which
#      cited "PR #339 relay" for a ruling relayed on #337 — a guess at what the *next* PR number
#      would be, written into prose before that PR existed, and the guess came true. Resolution
#      cannot see it (a well-formed `#N` resolves to whatever `N` turns out to be) and neither can a
#      reader, since the sentence reads perfectly. Only the comparison sees it, and it costs one `=`.
#
#      **Its population is the body's *prose*: fenced code blocks are excluded, and that second
#      boundary was measured the same way the first was — by watching the check fail on correct
#      content.** The very PR that added this check failed it, on a fenced block quoting `ratio.sh`'s
#      output verbatim, where the printed line *is* a commit's `Ratio-Class: ordered — #339` trailer.
#      A trailer naming the PR its commit lives in is the convention check 4's diff exclusion already
#      allows; the body was reporting it as evidence, not making a claim. **The other available fix
#      was to edit the quoted output so the token disappeared, and that is fabricated evidence to
#      satisfy a prose check** — a strictly worse defect than the one being checked, and one this
#      PR had already committed once. So: a claim is prose, a fence is a quotation, and only the
#      first can cite. Checks 1–3 still read fenced content, deliberately — resolution is a property
#      of the *number* and a fabricated one inside a quoted block is exactly what wants catching.
#      An **odd fence count is a failure**, not a narrowing: an unbalanced fence would silently swallow
#      the rest of the body into the excluded region, which is the under-matching trigger this file's
#      own trigger-coverage note is careful about. And the prose line count is printed, so a
#      population that collapsed to zero cannot report a pass as a verdict.
#
#      **The scope is the body and deliberately not the diff, and that boundary was measured rather
#      than assumed.** The first draft applied this to the diff and it failed immediately — on the
#      *correct* prose of the very PR that added it, because a code comment citing the PR it is
#      written in is this repo's convention for attributing a ruling, and a ruling relayed on a
#      review genuinely lives at that PR's number. `citation_subject_test.go`'s "(Ruling: Scott, PR
#      #337 relay.)" was written in `3c9ab09`, which *is* #337, so the broad reading would have
#      failed #337 retroactively. Body and comment are different populations: "never intentional"
#      is true of one and false of the other. Recorded because an over-matching guard fails loudly
#      and this one did, which is the only reason the distinction got drawn before merge.
#   5. **A closure claim in the tree names a *closed* issue — binding, rides check 2's request,
#      diff modes only.** A comment saying `closing #N` or `closes #N` asserts that the code it sits
#      beside dispatched #N. That is checkable, and #325's specimen is `sections.go:1238` — *"Retained
#      in Index, not a new field, closing #204"* — written while #204 was open. The close was a side
#      effect of prose, and **prose is not a channel the tracker reads**: the citation ran one way
#      and nothing checked the other, so a fixed-but-open issue sat in the queue that "what is the
#      next product PR" is chosen from. #325 sampled three engine-side issues and found three
#      already fixed; the cost is a measurement per candidate that evaporates on contact, and for a
#      `type:grave` it is worse, since a grave's lesson lives in its **closing** comment and
#      `label:type:grave --state closed` — the graveyard — under-reports by exactly these.
#
#      **It needs the network and costs no extra request**: `.state` comes back in the same
#      `gh api` payload check 2 already fetches per citation. #325's option (b) estimated "no
#      network" and that estimate was wrong in the cheap direction — worth saying, because a
#      forecast about an instrument's cost is as falsifiable as any other. The field is *validated*
#      rather than trusted: a `state` that is neither `open` nor `closed` is reported as a mechanism
#      failure, because the value is read positionally out of a tab-joined `--jq` and a reordered
#      field would otherwise turn every closure claim into a silent pass.
#
#      **The verb set is English's and not GitHub's, and it excludes `resolve`.** `closecheck.sh`
#      scans a PR body for the keywords *GitHub* acts on, so its set is GitHub's by necessity; here
#      the actor is a reader and the defect is a false sentence, so `closing` — which GitHub ignores,
#      and which is the specimen's own word — has to be in. `resolve` is out, measured rather than
#      argued: over the tree it adds exactly one site, `call_test.go:482`'s "this reduction resolves
#      #164's four vectors down to two", which claims a reduction and not a closure. It is also this
#      script's own word for what it does to a citation, so admitting it would point the arm at the
#      file that hosts it.
#
#      **A conditional or negated claim is exempt and prints a `note`.** "would fix #411", "cannot
#      close #N", "to fix #N" assert nothing about state; a modal before the verb is what separates
#      a claim of accomplished closure from a plan or a denial. The exemption is *printed and
#      counted*, never dropped, on the precedent the `verb` and `qualified` arms set in this file: a
#      token that vanished from the output would be an exemption mechanism, and the whole cost of an
#      exclusion arm is that whatever it exempts stops being checked.
#
#      **Diff modes only, which is the mirror of check 4 and for the mirror reason.** A closing
#      keyword in a PR *body* is not a claim to be verified — `closecheck.sh` forbids it outright
#      (grave #314: a squash message is derived from the title and body, so `Filed, not fixed: #310`
#      closed #310). Asking whether its target is open would be a second question about a token that
#      is already banned. Body and tree are different populations with different rules for the same
#      word, so check 4 runs in `--pr` alone and check 5 in the diff modes alone, and each prints a
#      not-applicable line in the other so neither reads as a pass it did not compute.
#
#      **The falsification bill, and it found a defect a reader could not have.** Each mutation was
#      applied, *confirmed applied by printing the mutated line*, run, and reverted — the confirmation
#      step because two neuters on the sibling #411 control silently failed to apply and a mutation
#      that never landed is indistinguishable from a tolerated one at the exit code:
#
#      (Every row below names its fixture's *form* and never spells it, for this arm's own reason: a
#      literal `closes` beside a literal number is scanned by the run that documents it, and the
#      target used throughout was an open issue, so the bill would fail check 5 on itself. The
#      citation warning below records the same trap costing three dangling citations in a
#      `CHANGELOG.md` paragraph; the fixtures used #411 and an unresolvable five-digit number.)
#
#      (**An issue citation is one to four digits**, and a fixture wider than that is invisible rather
#      than unresolvable — measured while re-running C6 for grave #435: a closure claim on `#99991`
#      extracts nothing at all, prints no FAIL, and reports `0 of 0` at exit 0. A genuinely
#      five-*digit* fixture could therefore not have produced C6's recorded `3 of 4`, so either the
#      number above was five *characters* — a hash and four nines, which is four digits and does fire —
#      or that row is credited to a mutation the extractor never saw. (Spelled as a form rather than
#      written out, because `make cite` failed this very paragraph for spelling it: a four-nines token
#      in prose about unresolvable numbers is an unresolvable number, and this file's own warning two
#      paragraphs up is that prose *about* the defect instantiates it. The check earned that one on its
#      author, in the sentence documenting the trap.) Unresolved here, because the artifact that
#      would settle it is the fixture itself and it was reverted; recorded because the constraint is
#      what the next fixture needs, and because the trap is this bill's own — *a mutation that never
#      landed is indistinguishable from a tolerated one at the exit code*, and the width guard is a
#      second way for one not to land. C6 was re-confirmed on a four-digit number: `0 of 1` with the
#      resolution FAIL above it and exit 1.)
#
#        C1   the plain claim, on an open issue         the verdict itself
#        C2   the verb behind `to`                      the modal exemption (must stay a note)
#        C3   the verb with a colon before the number   the retained colon — grave #314's spelling
#        C4   verb and number on two added lines        the paragraph join
#        C5   `.state` and `.title` swapped in the jq   the positional-field guard
#        C5b  C5 with the guard deleted                 the guard is not decoration
#        C6   the claim on a number that 404s           the coverage arm (`3 of 4`)
#        C7   the closing-row strip removed             grave #416's summary identity
#        C8   the verb `resolve`, in a claim            the measured exclusion is in force, not just
#                                                      documented — no verdict, no note, no count
#        C9   `--pr 409`                                the mode boundary
#
#      **C1 failed on the first run: the check printed its whole FAIL paragraph and exited 0.** The
#      `fail=1` was missing, so the finding reached the mechanism channel and never reached the
#      verdict channel — a red diagnosis inside a green CI, which is grave #365's shape inside a
#      check written to catch an unverified claim. No amount of reading the output finds it; the word
#      FAIL was all there. C5b is the same lesson from the other end: with the guard removed and the
#      field swapped, the fixture's citation printed `ok … claimed closed by this diff, and CI: the
#      PR-body scans …` and exited 0 — a state comparison against a title, agreeing with nothing.
#      C7's arithmetic is the record worth keeping in numbers: 28 tokens against categories summing
#      to 22.
#
#      Prose *about* this defect instantiates it, exactly as the citation warning below says: the
#      `CHANGELOG.md` paragraph recording #325 quotes *"closed #183"* and *"closed with #210"*, and
#      is scanned. Both resolve closed, so it passes — by luck of the targets' state, not by design.
#      A future writeup quoting a claim about a still-open issue fails, and the remedy is the one
#      that arm already implies: describe the form, or name the number outside the verb's reach.
#   6. **A *stated* claim about a citation's state agrees with the tracker — binding, rides check 2's
#      request, both modes.** `#N is open`, `#N stays closed`, `#N's remains open`: the sentence
#      asserts one of the two values of a field the payload already carries, so the comparison costs
#      nothing. Grave #434 is the specimen — a paragraph naming #328 as the live carrier of open work
#      the day after #328 closed, written into the *repair* of a stale premise, two bounds from the
#      zero that #328's own closure produced. **No sweep's domain includes the tracker**, so nothing
#      in the tree could see it; check 2 reported `#328 -> issue [phase:v0]` and passed, because
#      resolution is a property of the number and the false sentence was around it.
#
#      **It runs in `--pr` too, and is the only network check that runs in both modes.** Checks 4 and
#      5 exempt each other's population because a closing keyword in a body is *banned* rather than
#      verified; a state claim is not banned anywhere, and #434's own first draft was in PR #433's
#      body. A false sentence is false in whichever population it is written in.
#
#      **The trigger is a copula, and the first draft was not — which is this check's whole content.**
#      That draft matched the tracker's two state words anywhere in the citation's sentence, on the
#      reasoning that `open`/`closed` are the domain of the field being compared. Measured over 40
#      commits before landing: **5 of 5 distinct flags false**, against a pre-registered ceiling of
#      20%, each false by a different mechanism — the state word's subject being another noun, `open`
#      as a verb, a past-tense account, a `--state closed` inside a quoted flag. By that registration
#      the trigger did not land, and narrowing it was the work rather than licensing it after the
#      fact. Worse: **that draft caught #434 for the wrong reason.** The subject of "stays open" in
#      the specimen is *the issue* — #9 — and not #328 at all. The catch was proximity, and
#      *protection by coincidence is not protection.*
#
#      So the comparison was never the hard part; **aboutness** is — which noun a state word is
#      predicated of. A copula makes the citation the subject *by grammar*, so aboutness is decided
#      rather than guessed, and that is the only form admitted. Present tense only, with `was`/`were`
#      **absent from the verb list rather than filtered out of it**: a past-tense account is correct
#      by construction, and *the tense is the mechanism* (`foreclose_test.go`'s historical-account
#      class draws the same line for the same reason). Negation needs no list either — `is not open`
#      does not match a pattern that wants the state word beside the copula.
#
#      **Stated under-match, because one that is silent is the defect: this does not catch #434.**
#      The three sites that grave lived at assert openness by paraphrase — "has no vocabulary",
#      "waits on", "carries it alone" — which is unbounded English, and nothing here reaches it. One
#      catch in three was the pre-registered forecast and the narrowing took it to zero of three; the
#      historical catch on PR #433's body is what the check has — and on inspection it is not this
#      check's at all, since the broad draft made it by co-occurrence and was wrong about which noun
#      the sentence was about. **So the measured yield is two true positives, both from one tracker
#      state change and both on this PR's own prose.** The ruling that disposed of #432 falsified a
#      sentence in this file written minutes earlier (caught locally, at the line below), and then
#      falsified a second one in PR #437's body (caught by CI, on the run for the pushed SHA, after
#      the local repair — because I fixed the file and did not think to re-scan the body against the
#      changed world). The second is the better witness of the two: *the author had already applied
#      the lesson and still left a site standing.* Measured over 9 agreeing stated claims across
#      tracked files, counted with this check's own trigger rather than by hand — an earlier count of
#      6 was mine and was wrong, which is the same sourcing defect one remove out. Note the scope:
#      the population is *added lines*, so those 9 are ungated until a diff touches them.
#      The declaration lives beside the arm, on Scott's order, because a tripwire
#      with no witness is a legitimate thing to be only if it says so where the code is. What it does
#      gate is the stated form. **The paraphrase half gets no instrument, by ruling** — the remedy
#      there is not code: source the premise from the tracker rather than from a paragraph.
#
#      **My own pricing of this check was measured with the wrong instrument.** "26 citations, 0
#      disagreements" was produced by *reading* — a human resolving aboutness — so it forecast nothing
#      about a predicate that cannot read. *Measure with the instrument, not by hand.* The 40-commit
#      run had a second design error, mine and not the check's: old prose compared against today's
#      tracker measures drift since, not the predicate's accuracy today.
#   7. **A *discharge* claim in a PR body names a file that PR changes — binding, needs the network,
#      `--pr` only.** *"Recorded at the site and in the citations family"* asserts that work landed
#      somewhere; the somewhere is a file, and the PR's own changed-file list is the oracle. #492
#      shipped that sentence with only the first half true — `docs/laws/citations.md` was not in the
#      diff — and the same session told Scott an ADR tense repair had been amended into the commit
#      when the edit was made *after* the amend. One cause, and it is grave #434's in a new channel:
#      **a claim about the tree sourced from memory of having typed it**, whose sharp sub-form is
#      Scott's — *an edit made after an amend is not in the amend.* The available instrument was one
#      command.
#
#      **The ordered form was measured against its own specimen and caught neither error, which is the
#      whole reason this one is narrower.** The order (Scott, #492 review, filed as #493) was *"a file
#      path named in a PR body as changed must appear in `git diff --name-only` for that PR"*. Over
#      #492: 12 paths named in the body against 58 in the diff, **two flagged and both correct
#      sentences** — `docs/laws/citations.md` named in a *proposal* (*"Mint it in …"*) and in a
#      *negative* (*"nowhere a reader of … does"*), `internal/binary/instr.go` named in a statement
#      that five stale pointers into it are carried into #456, which is true of a file that PR
#      deliberately did not touch. Meanwhile the false sentence **names no path at all**: it names a
#      law *family*. A control whose first two firings must both be argued down is an exemption
#      surface being built by hand, and the narrowing was the work rather than a licence taken after
#      the fact — the same disposition check 6's 5-of-5 measurement forced.
#
#      **So the trigger is the claim shape, and the location is the verb's complement rather than its
#      neighbour.** A past-participle discharge verb — `recorded`, `added`, `minted`, `filed`,
#      `landed`, `repaired`, `stated`, the order's own set — then a locational preposition, then a
#      location, in that order, before the sentence ends. "In the same sentence" would be proximity,
#      and **the span rule is measurable on the same body**: #492's Decisions-needed bullet 1 is
#      *"Mint it in `docs/laws/citations.md`, and make … a stated term on #456's resolver"* — one
#      sentence carrying a listed verb and a resolvable path, silent here because what follows
#      `stated` is `on #456's resolver` and what precedes the path is an imperative. *Aboutness is
#      not proximity*; the verb decides attachment, exactly as the copula does in check 6.
#
#      **The location vocabulary is derived, never enumerated**, which is the law the rest of this
#      file is written under. Three offline sources: tracked paths (`git ls-files`), the basename of a
#      tracked path when it is *unique* in the tree, and the law-family names read off `docs/laws/*.md`
#      so that *"the citations family"* resolves to `docs/laws/citations.md`. ADR numbers join through
#      `docs/decisions/NNNN-*.md`, which check 1 already resolves.
#
#      **Five stated under-matches, because a trigger that fails silently is the defect this check is
#      for.** (a) An unresolvable location is **counted and printed**, never checked: *"at the site"*,
#      *"in the changelog"*, *"at both sites"* name no artifact, and #492's false sentence carries one
#      of each — only its second half is catchable. (b) A single extensionless word is not a
#      vocabulary token, so `LICENSE`, `NOTICE` and `Makefile` are unreachable by name: the token is
#      indistinguishable from ordinary English (*"in the notice above"*), and a false FAIL teaches its
#      author to phrase around the tool, which is the worse direction for a gate. (c) A basename
#      naming more than one file does not resolve — `instr.go` names three, as #492's own body says.
#      (d) An adjective between the determiner and the location blocks the match: what resolves is the
#      *head* of the prepositional phrase. (e) A paraphrased discharge is outside the population —
#      *"done, in `docs/laws/operations.md`"* and *"pre-registered in
#      `docs/laws/boards-and-buckets.md`"* are both in #492's body, both true, and neither carries a
#      listed verb. The verb set is the order's and is not widened by guesswork.
#
#      **`was`/`were` before the verb is exempt and printed**, on check 6's precedent and for check 6's
#      reason: a past-tense account records a transition, and *the tense is the mechanism*. A modal or
#      a negation immediately before the verb is exempt the same way, as in check 5, and both are
#      printed rather than dropped because whatever an exclusion arm exempts stops being checked.
#
#      **Measured against a pre-registration of exactly one FAIL on #492's body** (#493): 1 FAIL — the
#      false sentence, on `docs/laws/citations.md` — 1 `ok` on *"Recorded in
#      `internal/testenv/foreclose_test.go`'s header"*, which that PR does change, 3 claims whose
#      location did not resolve, and **silence on all three correct mentions**.
#
#      **The falsification bill. Two of the five narrowings are witnessed by the specimen and three
#      are not, and that split is the finding rather than a gap to paper over** — *identical boards are
#      the finding*, so the three got hand-built discriminating shapes instead of an argument. Every
#      row was applied, **confirmed applied by diffing the file against a saved copy**, run, and
#      reverted; two rows had silently failed to apply before that step was added, and *a mutation that
#      never landed is indistinguishable from a tolerated one at the exit code*:
#
#        M1  the membership test forced true       #492's body passes at exit 0 and both claims report
#                                                 `ok` — the FAIL comes from the comparison
#        M2  the span rule removed (location       **two** FAILs on `docs/laws/citations.md` and 6
#            anywhere in the sentence)             unresolvable notes: the second is the *"Mint it in
#                                                  … a stated term"* sentence, so this reproduces the
#                                                  ordered form's false positive exactly
#        M3  the heading break removed             no change on #492 — witnessed on a shim-driven body
#        M4  the list-marker break removed         no change on #492 — witnessed on a shim-driven body
#        M5  the `was`/`were` exemption removed    no change on #492 — witnessed on a shim-driven body
#
#      The three shim rows use the mechanism `internal/testenv/prfetch_test.go` already uses — a `gh`
#      on PATH that answers both calls — over one synthetic body carrying all three shapes: a `##
#      Landed` heading followed by a line opening *"in `docs/laws/gates.md` …"*, two adjacent bullets
#      where the first ends on the verb and the second opens on the location, and *"The rule was
#      recorded in `docs/laws/gates.md`"*, against a one-file diff that does not contain it. Baseline:
#      exit 0, 0 claims, 1 exempt note. Each mutation: exit 1 and exactly one FAIL. The `was` row is
#      also the vacuity check on its own exemption — the sentence reaches the resolver, so the note is
#      not silence dressed as an exclusion.
#
#      **The whole bill was re-run after the `dready` restructure below, and two rows failed to apply
#      for a third reason: the driver loop was written for `sh` word-splitting and run under `zsh`.**
#      `set -- $m` over `"M3 948 heading-break"` left `$2` empty there, so `sed -i '' "${ln}d"` became
#      `sed d` — which deletes **every** line, and an empty script exits 0. Both rows printed *mutation
#      survived*, which is the wrong direction of wrong: a narrowing that is load-bearing reported as
#      dead weight. Two tells, and neither was the exit code — the `what` field printed as `()`, and
#      the confirmation diff named line 1 of a file whose line 948 was the target. So the confirmation
#      is now a **line count**, `before - 1`, asserted before the run: *a mutation is confirmed by the
#      shape of the change, not by the fact that a command ran.*
#
#      **`--pr` only, which is check 4's boundary and check 4's argument.** A code comment saying
#      *"recorded in X"* is a claim about the tree at large, not about the diff it happens to sit in;
#      the body is where "this PR did it" is asserted, and it is the channel a principal reads.
#
# What is deliberately **not** gated: whether the resolved title matches the sentence citing it.
# There is no general oracle for that — agreement between a citation's context and an issue's title
# is a judgement, and a fuzzy word-overlap gate would fail on correct prose and pass on wrong
# prose, which is worse than no gate. So the title is **printed beside every citation** for the
# reviewer and the CI log, and the verdict channel carries only what a machine can decide. Verdict
# channel and mechanism channel are different instruments; this script uses both and says which.
#
# **Check 6 is a carve-out of one sub-claim from that non-goal, and it is worth saying which one,
# because "does the sentence agree with the citation" is exactly what the paragraph above declines.**
# What it gates is not agreement in general but a sentence whose predicate *is* a field the payload
# carries, in the one grammatical form where which noun the predicate attaches to is decided by the
# grammar rather than by a reading. Everything the paragraph above warns about is still not gated:
# whether the sentence's *characterisation* of the issue is apt, whether the paraphrase of a state is
# right, whether the citation is the number the writer meant. The carve-out is the copula and the two
# field values, and it stops there. The distinction is the same one check 5 draws — a boolean hiding
# inside a judgement — and the discipline is to take the boolean and leave the judgement printed.
#
# Checks 3, 4, 5 and 6 are the exceptions that prove the shape of the rule rather than weakening it:
# all four are title-*shaped* questions that turn out to have non-title answers — a label, an
# integer comparison, and a state field twice over — so none is the judgement the printed title is
# there for. Check 5 is the clearest case, since "does this comment's claim about #N hold" sounds like
# it needs a reading of the sentence and needs one boolean. The pattern worth
# reusing: when a printed field keeps catching the same class by eye, look for the sub-claim inside
# it that a machine can decide, and assert that. Reading the printed title has caught or would have
# caught four guessed numbers; the fourth is what promoted this sub-claim out of the print. (Directive:
# Scott, PR #339 review.)
#
# **Check 7 is not in that family, and the enumeration above must not be read as covering it.** Its
# subject is not the citation's target: it compares a sentence's claim about *this PR's own diff*
# against that diff, so what it resolves is a path rather than a number and the printed title has
# nothing to do with it. It rides this script because the data is already here — the body is fetched
# for check 4 and ADR numbers already resolve for check 1 — and a sibling scanner over one grammar
# would drift from this one, which is the argument the header makes for keeping the classification in
# a single awk program.
#
# ## The domain is printed, always
#
# The last line reports the range, the number of added lines scanned, and the count of
# citation-shaped tokens broken down by what each turned out to be — *coverage is a claim*, and a
# checker that reports "OK" without saying over what has made an unstated claim about its own
# population. A diff that cites nothing passes and says so in those words: zero citations is a
# legitimate state, and it is not the same fact as zero failures.
#
# **It says "citation-shaped tokens" and not "citations" because two of the categories are not
# citations** — a qualified reference is one this repo cannot resolve, and a `verb` is a Go format
# flag that was never a reference at all (#308). While the total was called the citation count, the
# `%#02x` phantom made "12 citations, all resolving" an over-count, and the only number that had
# ever been checkable — how many *issue* citations were resolved — was buried inside it. The
# breakdown is the fix: the total counts what the extractor matched, and each category says what it
# was.
#
# ## Trigger coverage, stated because an under-matching trigger fails silently
#
# * `#NNNN` is matched as `#` plus a run of digits, accepted only at 1–4 digits. A six-digit hex
#   colour is therefore skipped whole rather than shredded into a false four-digit citation.
#   (Written without an example, because this file is in the diff it checks: the first draft's
#   example number was itself scanned, reported as unresolvable, and was right to be. **The hazard
#   is not this file — it is any file the diff touches.** A `CHANGELOG.md` paragraph explaining a
#   dangling-citation defect quoted three unqualified numbers as examples and became three dangling
#   citations, on this checker's own PR. Prose about a citation defect describes the form; it does
#   not instantiate it.)
# * The grave rule triggers on the *immediately preceding* word — `grave #78`, `graves #78`,
#   `grave issue #78` — and on a following run of numbers joined by `/`, `,`, or `and`, which is
#   how this repo writes `graves #78/#105`. It deliberately does **not** trigger on the word
#   appearing anywhere earlier in the line: "sweep after a grave … see #262" would then demand a
#   label #262 has no reason to carry. That under-match is the stated cost, and it is the right
#   direction of error for a gate — a missed grave citation is caught by review, a false demand
#   trains people to work around the tool.
# * **An ADR citation is a four-digit number, and a one-digit one is not a citation at all.** ADRs
#   number their own decisions — 0027 has five, 0028 three — and this repo refers to them in prose
#   as "decision 3", "decisions 2 and 3", "0029 decision 2". The first version of this trigger read
#   any run of digits after the word, so every such sentence became a citation to
#   `docs/decisions/3-*.md` and failed. That is an **over**-matching trigger predicate, the mirror
#   of the under-matching one the grave rule's note above is careful about, and it fails in the
#   worse direction for a gate: an under-match misses a finding, an over-match makes the gate
#   unpassable for correct prose and *teaches people to work around the tool*. Measured on merged
#   history rather than argued: `6a36e97` (the commit that added ADR 0028) fails this check three
#   times, on its own record's headings, and so did every ADR with numbered decisions. The
#   narrowing is the artifact's own convention — every file in `docs/decisions/` is `NNNN-`, all 29
#   of them — so four digits is what a citation looks like and anything shorter is prose.
#   A 2- or 3-digit run is neither shape and is reported as such (phase 1b) rather than dropped,
#   because dropping it would trade the over-match for the silent under-match.
# * **A closure claim triggers on the verb immediately before the citation**, with the same
#   sentence-ish break stripped as the grave rule — but the break class keeps the **colon**, where
#   the grave rule strips it. `fixed: #310` is grave #314's own specimen, so a strip that ate the
#   colon would exempt the exact form the defect was written in. The adjacency is otherwise the
#   grave rule's, including the paragraph join, so a claim wrapped across two added lines is caught
#   here where `closecheck.sh`'s line-at-a-time scan states an under-match on it.
# * Deleted lines are not scanned. A diff is responsible for the citations it *adds*; the ones it
#   removes are the previous author's, and re-litigating them turns every edit into a sweep.
# * **A discharge claim triggers on a listed verb followed by a locational preposition and then a
#   resolvable location**, in that order and before the sentence ends. Its population is the body's
#   prose — fenced blocks excluded, as in check 4, so a *quotation* of a claim is not a claim, which is
#   also the only way prose about this defect can name the specimen sentence without instantiating it.
#   Wrapped lines are joined, since a hard-wrapped body puts the verb and the location on different
#   lines about as often as not; the join breaks at a blank line, at a **heading**, and at each list
#   marker. The heading break is not tidiness: one of the seven mandatory section names *is* a listed
#   verb, so `## Landed` would otherwise be a claim with no location. The list-marker break is the
#   paragraph join's mirror — two adjacent bullets are two claims, and welding them pairs one bullet's
#   verb with the next one's path. A sentence ends at `.`, `;`, `!` or `?` **followed by a space or the
#   end of the paragraph**, which is why `citations.md` does not end one.
#
# The per-diff domain is the reason a *one-time* repo-wide sweep was run at authorship rather than
# assumed away: this check can only ever see what a diff adds, so everything already committed is
# outside its population by construction. That sweep resolved 63 `grave #N` citations and flagged
# 10 — five graves missing the label, five citations naming the wrong artifact — triaged in #286.
# A checker that starts clean on a corpus it never read would be claiming coverage it does not
# have, which is the law it was written under.
#
# **Check 5 was swept the same way and is reported the same way, including the part that makes it
# look weaker.** 16 closure-claim sites across the tree, 9 distinct targets (#19 ×2, #127 ×2, #183,
# #204, #210, #284 ×2, #310 ×3, #373 ×2), **all closed** — so the sweep produced zero findings, and
# that is a fact about when it ran and not about the check. #325's specimen `closing #204` is in the
# corpus and would have failed on the day it was written; #204 has since been closed, by the audit
# #325 asked for. The one site the check has anything to say about today is a `note`: this session's
# own "would fix #411", exempt as conditional, with #411 open. A zero-finding sweep is worth
# printing because the alternative is a reader inferring the population was never read.

set -eu

usage="usage: citecheck.sh <rev> | <base> <head> | --worktree [base] | --pr <number>"

# `--pr` is its own mode rather than a modifier: it scans prose, so it has no base, no head and no
# diff, and every later reference to those would have to special-case it anyway.
selfpr=""
prmode=0
if [ "${1-}" = "--pr" ]; then
	prmode=1
	selfpr="${2:?$usage}"
	base=""
	head=""
else
	base="${1:?$usage}"
	head="${2-}"
	# An unrecognized `--flag` is refused rather than read as a revision, which is how **grave
	# #549** was reached twice in one session on real work: `--range origin/main HEAD` scanned 0
	# lines of a 1219-line diff, and `--body <file>` scanned 0 lines of a comment carrying 20
	# citations. Neither is a form this script has; `--body` *is* a `closecheck.sh` form, and that
	# asymmetry is what invites the second mistake, so the message names it.
	#
	# Refused by *shape* rather than against an allow-list of known flags, because an allow-list is
	# the half that goes stale when a form is added — the same under-matching-predicate defect the
	# `--pr` guard below was repaired for.
	case "$base" in
	--worktree) ;;
	-*)
		echo "FAIL  \"$base\" is not a form this script has, and it was not read as a revision."
		echo "      \`--body\` is a closecheck.sh form; \`--range\` is nobody's (grave #549)."
		echo "      $usage"
		exit 1
		;;
	esac
fi

# requirerev refuses a revision that does not resolve, before anything is diffed.
#
# **Grave #549, and grave #365's specimen recurring below the comment that narrates it.** Both diff
# captures used to end `|| true`, and `git diff` is invoked here without `--exit-code` or `--quiet`,
# so it returns 0 whether or not there are differences: that token had no legitimate subject and
# caught exactly one thing — an argument that is not a revision. git wrote `fatal:` to stderr, `||
# true` turned 128 into 0, `diffout` was empty, and all seven checks ran over nothing and printed
# "this diff cites nothing", which is a finding about a diff rather than a confession that no diff
# was read.
#
# Checked here rather than left to `set -e`, which is this script's own argument at the `--pr` fetch
# below: the property should be visible at the line that has it, and the reader should get a message
# naming the argument instead of git's. The user-supplied revisions are what is verified, not the
# derived `$base^` — a one-arg form on a root commit has no parent, and that case is caught by the
# diff's own status a few lines on rather than reported here as a bad argument.
requirerev() {
	if ! git rev-parse --verify --quiet "$1^{commit}" >/dev/null; then
		echo "FAIL  \"$1\" is not a revision in this repository, so no diff was read."
		echo "      This is not a pass: a check that could not ask its question does not get to"
		echo "      report green (grave #549)."
		echo "      $usage"
		exit 1
	fi
}

# readdiff captures the diff and refuses to continue if git could not produce one.
readdiff() {
	if ! diffout="$(git diff "$@")"; then
		echo "FAIL  \`git diff $*\` failed, so no diff was read; git's own message is above."
		echo "      This is not a pass (grave #549)."
		exit 1
	fi
}

if [ "$prmode" -eq 1 ]; then
	if ! command -v gh >/dev/null 2>&1; then
		echo "FAIL  gh is not installed, so PR #$selfpr's body was not scanned."
		echo "      This is not a pass. A check that could not ask its question does not get to"
		echo "      report green; CI is where this half is binding."
		exit 1
	fi
	# **The fetch is its own statement, and the reason is grave #365.** This line used to read
	# `diffout="$(gh pr view … | sed 's/^/+/')"`, and a pipeline's exit status belongs to whatever
	# ran last — `sed`, which succeeds on empty input. So `set -eu` never saw the failure: a
	# rate-limited or unauthenticated `gh` produced an empty body, `extract` found no citations in
	# it, and the script exited **0** announcing "self-citation check ran against PR #N, over 0
	# prose line(s)" — a positive claim that it ran, followed by the benign-sounding "this diff
	# cites nothing", which is a finding about a diff and not a confession that nothing was read.
	# CI's `citations` job runs exactly this arm on `pull_request`, so the hole was load-bearing.
	#
	# The sibling had it right. `closecheck.sh`'s `--pr` arm assigns the fetch with no pipe, so
	# `set -e` catches it and that script exits 1 on the same input — the guard below existed here
	# too, in the same words, and only ever tested `command -v gh`: **the binary's absence, which
	# is the least likely way a fetch fails.** A guard's trigger predicate is a claim about the
	# space, and this one under-matched by construction. Checked explicitly rather than by relying
	# on `set -e` again, so the property is visible at the line that has it. Both scripts' `--pr`
	# arms are asserted to refuse a green on a failing fetch by `TestPRFetchFailureIsNeverAPass`
	# (`internal/testenv`), with a success arm beside it so the failure arm cannot pass by breaking
	# the script for an unrelated reason.
	if ! prbody="$(gh pr view "$selfpr" --json title,body --jq '.title + "\n" + .body')"; then
		echo "FAIL  PR #$selfpr's body could not be fetched, so it was not scanned."
		echo "      This is not a pass, for the same reason a missing gh is not: a check that"
		echo "      could not ask its question does not get to report green. Common causes are"
		echo "      an API rate limit, an unauthenticated gh, and a PR number that does not"
		echo "      exist — run \`gh pr view $selfpr\` to see which, then re-run this."
		exit 1
	fi
	# Fed to the same `extract` as a diff, by prefixing every line with `+`. The alternative is a
	# second scanner for prose, and two scanners over one grammar drift — the citation grammar is
	# one concept and gets one place, which is the argument this script's own header makes for
	# keeping the classification inside a single awk program.
	#
	# `printf` into `sed` is deliberately *not* the shape above: its leading command reads a shell
	# variable and has no failure mode to swallow.
	diffout="$(printf '%s\n' "$prbody" | sed 's/^/+/')"
elif [ "$base" = "--worktree" ]; then
	base="${2-main}"
	head="" # the empty head *is* the working tree, for git diff and for the label below
	requirerev "$base"
	# Unquoted `$head` deliberately: empty has to vanish from the argument list rather than become
	# an empty pathspec, which is the whole mechanism by which this mode means "the working tree".
	readdiff "$base" $head
else
	requirerev "$base"
	if [ -n "$head" ]; then
		requirerev "$head"
	fi
	if [ -z "$head" ]; then
		head="$base"
		base="$base^"
	fi
	readdiff "$base" $head
fi

# In worktree mode, `git diff` is not the whole working tree: an **untracked** file is invisible
# to it, which is the tracked-files defect this mode was added to avoid, one layer in. A new file
# is the most likely home for a freshly guessed number, so its lines are appended to the stream as
# synthetic additions — `+++` first, so the paragraph join below cannot weld a new file's opening
# line to the previous hunk's tail. Found while falsifying the check: the first `--worktree` run
# could not see this script.
if [ "$prmode" -eq 0 ] && [ -z "$head" ]; then
	for f in $(git ls-files --others --exclude-standard); do
		diffout="$diffout
+++ b/$f
$(sed 's/^/+/' "$f")"
	done
fi
nlines="$(printf '%s\n' "$diffout" | grep '^+' | grep -v '^+++' | grep -c '' || true)"

# extract prints one `<kind> <number>` row per citation the diff adds: `issue N`, `grave N`, or
# `adr N`. The classification lives entirely in this awk program rather than half here and half
# in the shell, so there is one place to read the trigger from.
#
# **It joins consecutive added lines before matching, and that is not a nicety.** The first draft
# matched the `+` lines one at a time and missed `ADR 0025` on this script's own PR, because the
# citation was split by a prose wrap — `... citing ADR` / `0025's carve-out ...`. That is #78 /
# #80 / #105 exactly, the wrapped-lead defect, committed by the checker written to enforce the law
# about it, and found the only way it can be: by running the instrument over the diff that
# introduced it (*artifacts become oracles*). `internal/testenv`'s law-index reader had already
# paid for this and split-then-joins for the same reason; read the sibling.
#
# The join stops at any non-added line — a context line, a deletion, a hunk header, a `+++` file
# header — so the unit is a run of consecutive added lines, which is what a wrapped paragraph is.
# It cannot fabricate a citation by welding the tail of one file's hunk to the head of another's.
extract() {
	awk '
	function emit(kind, n) {
		if (length(n) >= 1 && length(n) <= 4) print kind " " n
	}
	function scan(s,   t, tok, gap, num, isgrave, prevgrave, tail, ctail) {
		# ADR citations first: `decision 0025`, `decisions 0025`, `ADR 0025`. **Four digits,
		# because that is the filename convention** — see the trigger-coverage note above on why a
		# one-to-three digit run after the word is a sub-decision reference and not a citation.
		t = s
		while (match(t, /([Dd]ecision[s]?|ADR[s]?)[ ]+[0-9][0-9]*/)) {
			tok = substr(t, RSTART, RLENGTH)
			sub(/^[^0-9]*/, "", tok)
			if (length(tok) == 4) {
				emit("adr", tok)
			} else if (length(tok) >= 2) {
				# A 2- or 3-digit run is neither: no ADR is spelled that way and no record has 10
				# numbered decisions. Reported rather than dropped, since dropping it is the
				# under-match this narrowing could otherwise introduce.
				emit("adrshort", tok)
			}
			t = substr(t, RSTART + RLENGTH)
		}

		# Issue citations, left to right, carrying the grave-run state forward.
		t = s
		prevgrave = 0
		while (match(t, /#[0-9][0-9]*/)) {
			gap = substr(t, 1, RSTART - 1)
			num = substr(t, RSTART + 1, RLENGTH - 1)
			t = substr(t, RSTART + RLENGTH)

			# **A `#` that is a Go format-verb flag, not a citation** (#308). The narrow
			# hex verb `%#02x` put its own flag-and-width into this checkers output as a
			# citation, and because the number it landed on names a real grave, the gate
			# printed `ok` beside a field width — a *phantom resolved citation*, which is
			# worse than a false alarm. (The number is deliberately not written here: see
			# this files header on prose about a citation defect describing the form
			# rather than instantiating it. The first draft of this block wrote it out,
			# and this checker duly resolved it against an unrelated LEB128 grave.) A
			# false alarm is
			# visibly wrong; this asserted that a citation had been checked and found
			# good where there was no citation at all, and it inflated the total the
			# last line prints, so "all resolving" was an over-count nobody could
			# falsify from the green.
			#
			# The predicate is the **verb form**, not the two call sites that happened
			# to have one: a `#` whose preceding run is `%` plus Go flag characters
			# (`+-0#`). `%#x` is invisible only because no digits follow it, and `%#4x`
			# would have been the next specimen — the population is Go format verbs,
			# not the two files a grep found.
			#
			# **A space is deliberately not in the flag class**, though `% d` is a real
			# verb: the ordinary English "100% #298" would otherwise parse as a verb and
			# a genuine citation would stop being resolved. That is the `pre-#298`
			# direction of error, an exemption wearing a fix, and this arm sits one
			# character away from it — so the under-match on the vanishingly rare
			# space-flag-then-hash form is taken on purpose. (Also not written out: with
			# the space excluded from the class, spelling that verb here would emit its
			# width as a bare citation, which is the same self-instantiation the block
			# above records.)
			#
			# Printed and counted rather than dropped, on the precedent the next arm
			# sets: a token that vanished from the output would be an exemption
			# mechanism. **No apostrophes in this block** — the awk program is inside a
			# single-quoted shell string, which is why its neighbours read "this
			# checkers own header"; the first draft of this comment used one and broke
			# the script at parse time.
			if (match(gap, /%[-+0#]*$/)) {
				print "verb " substr(gap, RSTART, RLENGTH) "#" num
				prevgrave = 0
				continue
			}

			# **A bare `#N` names *this* repo, so a cross-project citation needs a
			# qualifier or it is dangling by construction.** `umami#396` is the
			# GitHub-conventional form and is what this arm recognizes: a token
			# butted directly against the `#`, no space. Emitted as `qualified` and
			# *printed* below rather than dropped — an unresolvable-by-design
			# reference that vanished from the output would be an exemption
			# mechanism, and the count is the only thing making it reviewable.
			#
			# Reported as a *qualified reference* and not as a cross-project
			# citation, because this arm cannot tell those apart: a markdown anchor
			# whose slug opens with digits matches identically. Naming it what the
			# match actually establishes is the difference between a note and a
			# lying witness. The previous behaviour failed such a link as a bad
			# issue citation, a false positive in the other direction. (No literal
			# example here, per this files own header: an example would be scanned
			# by the very run it documents, and the first draft of this comment was.)
			#
			# Not resolved against that repo: doing so would make this checkers
			# verdict depend on a repo whose numbering nobody here controls, and a
			# gate that can go red because a sibling project renumbered is a gate
			# that will be disabled. Provenance is asserted by the qualifier; the
			# claim it makes is checkable by a human following it, which is all a
			# cross-repo citation can honestly offer.
			#
			# The qualifier must **end** in an alphanumeric, and that is not
			# cosmetic: the first draft accepted a trailing `-` or `.`, so the
			# ordinary English `pre-#298` parsed as project `pre-`, and a real
			# citation was silently exempted from resolution instead of being
			# checked. Found by running this checker over the diff that first
			# wrote that phrase — *artifacts become oracles* — and it is the
			# dangerous direction of error, since the whole cost of this arm is
			# that whatever it claims is unresolvable stops being resolved. No
			# `owner/repo#N` ends in punctuation, so the tightening costs nothing.
			if (gap ~ /[A-Za-z]([A-Za-z0-9_.-]*[A-Za-z0-9])?$/) {
				match(gap, /[A-Za-z]([A-Za-z0-9_.-]*[A-Za-z0-9])?$/)
				if (length(num) >= 1 && length(num) <= 5)
					print "qualified " substr(gap, RSTART, RLENGTH) "#" num
				prevgrave = 0
				continue
			}

			# The trigger: the word immediately before this citation, with a
			# sentence-ish break stripped so an earlier mention cannot reach it.
			tail = tolower(gap)
			sub(/^.*[.;:]/, "", tail)
			isgrave = (tail ~ /graves?[ ]*(issue[ ]*)?$/)
			# ... or a continuation of a `graves #78/#105` run.
			if (!isgrave && prevgrave && gap ~ /^[ ]*([\/,]|and)[ ]*$/) isgrave = 1

			# **Check 5s trigger, on its own tail, because the break class differs.**
			# The grave rule strips through the last `.`, `;` or `:`; this one keeps
			# the colon, since `fixed: #310` is grave #314s own form and a strip that
			# ate the colon would exempt the exact spelling the defect was written in.
			# Emitted as an *extra* row about a token that already has one, which is
			# why the shell strips these before taking the total: the summary identity
			# grave #416 exists for counts what the extractor matched, once each.
			#
			# A modal or a negation before the verb means the sentence asserts nothing
			# about state -- "would fix", "cannot close", "to fix" -- so it is emitted
			# as exempt and printed, never dropped. The cost of any exclusion arm is
			# that whatever it exempts stops being checked, and this file has two
			# older arms (verb, qualified) that pay it the same way.
			ctail = tolower(gap)
			sub(/^.*[.;]/, "", ctail)
			if (ctail ~ /(clos(e[sd]?|ing)|fix(e[sd])?)[ ]*:?[ ]*$/) {
				if (ctail ~ /(would|could|should|shall|will|might|may|must|to|cannot|not|never|nor|if|when|once)[ ]+(clos(e[sd]?|ing)|fix(e[sd])?)[ ]*:?[ ]*$/)
					emit("closingif", num)
				else
					emit("closing", num)
			}

			if (length(num) < 1 || length(num) > 4) { prevgrave = 0; continue }
			emit(isgrave ? "grave" : "issue", num)
			prevgrave = isgrave
		}
	}
	function flush() {
		if (buf != "") scan(buf)
		buf = ""
	}
	/^\+\+\+/ { flush(); next }
	/^\+/ {
		buf = (buf == "" ? "" : buf " ") substr($0, 2)
		next
	}
	{ flush() }
	END { flush() }
	'
}

cites="$(printf '%s\n' "$diffout" | extract | sort -u)"

# Check 5's population, split off *before* the total is taken. A closure claim is an extra row about
# a token that already has an `issue` or `grave` row, so leaving it in `cites` would inflate `ncites`
# past the sum of the five categories — which is precisely the identity grave #416 exists for. Held
# as its own stream for the same reason `prose_nums` is: two populations, one grammar, and a verdict
# a reader can attribute to the population it came from.
#
# Diff modes only. A closing keyword in a PR *body* is not a claim to verify — `closecheck.sh`
# forbids it outright (grave #314), so asking whether its target is open would be a second question
# about a banned token. The rows are stripped in both modes and consulted in one.
closing_nums=""
closing_exempt=""
if [ "$prmode" -eq 0 ]; then
	closing_nums="$(printf '%s\n' "$cites" | awk '$1 == "closing" { print $2 }' | sort -u)"
	closing_exempt="$(printf '%s\n' "$cites" | awk '$1 == "closingif" { print $2 }' | sort -u)"
fi
cites="$(printf '%s\n' "$cites" | grep -v '^closing' || true)"
ncites="$(printf '%s' "$cites" | grep -c '' || true)"

# Check 4's population: the body's prose, with fenced code blocks removed. See the header — a fence
# is a quotation and a quotation is evidence, so only prose can cite. Computed as a second, narrower
# stream rather than by tagging tokens in `extract`, because checks 1-3 keep the wide population and
# one scanner over two domains is easier to read than one scanner carrying a flag through.
prose_nums=""
nprose=0
nfences=0
if [ "$prmode" -eq 1 ]; then
	nfences="$(printf '%s\n' "$diffout" | grep -c '^+[[:space:]]*```' || true)"
	if [ "$((nfences % 2))" -ne 0 ]; then
		echo "FAIL  PR #$selfpr's body has $nfences fence markers — an odd count."
		echo "      Check 4 excludes fenced blocks, so an unbalanced fence swallows the rest of the"
		echo "      body into the excluded region and the check silently stops asking. Balance the"
		echo "      fences; an under-matching population is not a pass."
		exit 1
	fi
	prose_only="$(printf '%s\n' "$diffout" | awk '/^\+[[:space:]]*```/ { fence = !fence; next } !fence')"
	nprose="$(printf '%s\n' "$prose_only" | grep -c '^+' || true)"
	prose_nums="$(printf '%s\n' "$prose_only" | extract |
		awk '$1 == "issue" || $1 == "grave" { print $2 }' | sort -u)"
fi

# Check 6's population: citations that are the **grammatical subject** of a present-tense state
# claim — `#N is open`, `#N stays closed`, `#N's remains open`. Computed as its own pass for the same
# reason `prose_nums` is, two populations and one grammar.
#
# **The trigger is a copula with the citation as its subject, and the first draft of this check was
# not, which is the whole lesson of it.** That draft matched the tracker's own two state words
# anywhere in the citation's *sentence*, on the reasoning that `open`/`closed` are the domain of the
# field being compared and so not an enumeration of English. It was measured over 40 commits before
# landing, and **5 of 5 distinct flags were false positives**, each by a different mechanism:
#
#   - `(grave #314), so asking whether **its target** is open` — the state word's subject is another noun
#   - `has to **open** GC for an unrelated reason (#395)` — `open` is a verb, not a state
#   - `while #204 **was** open` — past tense, and correct as written
#   - `` `--state closed --limit 500` … and #303 is the control `` — the word is inside a quoted flag
#
# Against a **pre-registered ceiling of 20%**, so by that registration the trigger did not land and
# narrowing it was the work rather than licensing it. Worse, and this is the part worth keeping: the
# grave that motivated the check — #434, `"the issue stays open because #328's 103 module-and-section
# vectors have no vocabulary yet"` — was caught by that draft **for the wrong reason.** The subject of
# `stays open` in that sentence is *the issue*, meaning #9; it is not #328. The catch was proximity,
# and *protection by coincidence is not protection.*
#
# So the hard part was never the comparison — Scott's grant was right that comparing two states is a
# comparison and not an instrument. The hard part is **aboutness**: which noun a state word is
# predicated of. My own pricing of this check ("26 citations, 0 disagreements") measured a *human*
# resolving aboutness by reading, so it forecast nothing about a predicate that cannot. *Measure with
# the instrument, not by hand.*
#
# What survives is narrow and sound: a copula makes the citation the subject **by grammar**, so
# aboutness is not guessed. Present tense only — `was`/`were` are absent from the verb list rather
# than filtered out of it, because a past-tense account is correct by construction and *the tense is
# the mechanism* (`foreclose_test.go`'s historical-account class says the same thing about the same
# problem). Negation needs no list either: `is not open` simply does not match a pattern that wants
# the state word next to the copula.
#
# **Stated cost, because an under-matching trigger fails silently: this does not catch #434.** The
# paraphrase channel that grave travelled through — "has no vocabulary", "waits on", "carries it
# alone" — is unbounded English, and nothing here reaches it. What this does catch is the *stated* form
# of the same claim, which the tree uses in 7 distinct places. **The paraphrase half gets no instrument
# at all, by ruling** (#432, closed at the #437 review): detecting what a sentence is about is
# unbounded, and the remedy is a habit rather than code — source a premise the tracker holds from the
# tracker, not from an adjacent paragraph.
#
# **This sentence used to say that #432 stayed open, and that is how the check took its first true
# positive — on itself, live and unmutated.** The ruling that granted the paraphrase half no instrument
# also closed #432, so a sentence written twenty minutes earlier became false, and `make cite` failed
# with the arm's own FAIL paragraph pointing here. Recorded because it is the cheapest possible
# demonstration of the thing the check is for: **the sentence did not change, the world did**, and
# nothing in the tree except this arm could have noticed. Note which remedy applied — the work really
# was disposed of, so the *sentence* was stale; had the disposition been wrong, reopening #432 would
# have been the fix. The arm prints both because they are different repairs.
#
# **Two spellings hide a claim from this arm, and both were found the same way the true positive
# above was — in prose written about #432, in the diff that reports them (#441).** The match is
# local, so anything between the copula and the state word blocks it: `#432 is **closed**`, where the
# emphasis markers are the intervening text, and `#432 is` / `closed on Scott's ruling`, where a line
# wrap is, since the extraction is per added line and never a window over the paragraph. Both claims
# were about this check's own subject and both read as claims to a human; the run said `0 of 0`. Not
# repaired here, and the reason is the ruling above rather than effort: widening to skip emphasis and
# to join wrapped lines re-opens the sentence-window direction that was 5-of-5 false, and a wrapped
# window is exactly where aboutness stops being decidable by a copula. Written down instead, because
# what needs to not happen is a reader taking `0 of 0` for *no claims were made* — an author who
# wants a claim checked can put it on one line unadorned, which is what this diff did once it knew.
state_claims=""
if [ -n "$diffout" ]; then
	state_claims="$(printf '%s\n' "$diffout" | awk '
	/^\+\+\+/ { next }
	/^\+/ {
		line = tolower(substr($0, 2))
		# The citation is the subject of the copula, so the match is local and needs no
		# sentence window: `#N` (optionally possessive) then a present-tense copula, then an
		# optional `still`, then the state word. Backtick-quoted flags do not match, because
		# `--state closed` has no citation in subject position before it.
		while (match(line, /#[0-9][0-9]*('"'"'s)?[ ]+(is|are|stays|remains|sits)[ ]+(still[ ]+)?(open|closed)/)) {
			m = substr(line, RSTART, RLENGTH)
			line = substr(line, RSTART + RLENGTH)
			match(m, /[0-9][0-9]*/); num = substr(m, RSTART, RLENGTH)
			if (length(num) < 1 || length(num) > 4) continue
			print num "\t" (m ~ /closed$/ ? "closed" : "open")
		}
		next
	}
	' | sort -u)"
fi

# Check 7's population: discharge claims in the body, each resolved to a tracked path. `--pr` only, and
# unlike checks 4, 5 and 6 it needs a *second* thing about the PR — the list of files it changes. See
# the header for why the trigger is the claim shape rather than the path, and for the five under-matches.
#
# **`gh api …/pulls/N/files --paginate` rather than `gh pr view --json files`, and the direction of the
# error is the reason.** A truncated file list turns a *true* claim into a FAIL — the direction that
# teaches an author to phrase around the gate — and `--json files` is a GraphQL connection whose page
# this script does not control, where `--paginate` over the REST resource is the same idiom check 2
# already uses and exhausts the pages by construction. Assigned with no pipe, per grave #365: a
# pipeline's exit status belongs to whatever ran last, and `--jq` here is `gh`'s own flag rather than a
# second process. An empty list is treated as a mechanism failure below rather than as "this PR changes
# nothing", because the second reading makes every claim in the body fail at once.
#
# **The field is `.filename` and the first draft wrote `.path`, which is the vacuity guard below earning
# itself on the run that added it.** REST's pull-files element has no `path`, so `--jq '.[].path'`
# printed one *empty line per changed file* and exited 0 — and the line count was **58 on a PR with 58
# changed files**, which is how it survived a check: I counted lines and inferred content, so the
# instrument agreed with the issue's own figure while carrying nothing. *A suspiciously clean result is
# a tell*, and a count is not a reading. What caught it is the zero-length guard, which is check 5's
# `.state` validation one field over: a name read out of a payload is trusted by nobody here.
#
# **Check 7's own mechanism failure does not `exit`, and an existing control is what taught it that.**
# The first draft exited 1 on each of the three arms below, and `TestCitationLookupFailureIsNotAVerdict`
# went red on both of its shims: they fail *every* `gh api` call, so check 7's fetch died before the
# per-citation resolver ran, and the log carried check 7's mechanism FAIL where #410's two distinct
# wordings belong — the same one-wording-covers-two-causes shape that grave being about. It also took
# the summary line with it, so grave #416's identity assertion had nothing to read and reported *a skip
# is not a verdict* instead. The general form: **a late check's broken oracle must not silence an
# earlier check's verdict.** The body fetch above may exit, because nothing can run without a body;
# check 7's cannot, because checks 1–6 can all still ask their questions. So each arm records `dfail=1`,
# leaves `dready` at 0, and the run continues to a non-zero exit through the shared flag.
discharge_rows=""
prfiles=""
nprfiles=0
dready=0
dfail=0
if [ "$prmode" -eq 1 ] && ! prfiles="$(gh api "repos/{owner}/{repo}/pulls/$selfpr/files" --paginate --jq '.[].filename')"; then
	echo "FAIL  PR #$selfpr's changed-file list could not be fetched, so no discharge claim in"
	echo "      its body was checked. This is not a pass: a check that could not ask its question"
	echo "      does not get to report green. The other checks below still ran — read their lines"
	echo "      for their own verdicts, which this failure says nothing about."
	dfail=1
fi
if [ "$prmode" -eq 1 ] && [ "$dfail" -eq 0 ]; then
	nprfiles="$(printf '%s' "$prfiles" | grep -c '' || true)"
	if [ "$nprfiles" -eq 0 ]; then
		echo "FAIL  PR #$selfpr reports zero changed files, which is a mechanism failure and not a"
		echo "      verdict: a PR with an empty diff cannot exist, and reading the empty answer as"
		echo "      one would fail every discharge claim in the body at once. Re-run once the"
		echo "      transport is working."
		dfail=1
	fi
fi
if [ "$prmode" -eq 1 ] && [ "$dfail" -eq 0 ]; then
	# The location vocabulary, derived and never enumerated. Lowercased on the left because prose
	# capitalizes and `CHANGELOG.md` does not; the real path is carried on the right, so the verdict
	# names the artifact rather than the token that found it.
	dvocab="$(mktemp)"
	dprose="$(mktemp)"
	{
		# Tracked paths. **A token with neither `/` nor `.` is dropped**, which costs `LICENSE`,
		# `NOTICE` and `Makefile`: a bare extensionless word is indistinguishable from ordinary
		# English ("in the notice above"), and the false FAIL is the direction that trains an author
		# to phrase around the tool. Stated in the header as under-match (b).
		git ls-files | awk '{ t = tolower($0); if (t ~ /[\/.]/) print t "\t" $0 }'
		# A basename, but only where it is **unique** in the tree — `instr.go` names three files, so
		# it resolves to none of them. Under-match (c), and #492's own body says the number.
		git ls-files | awk -F/ '
			{ b = tolower($NF); n[b]++; p[b] = $0 }
			END { for (k in n) if (n[k] == 1 && k ~ /[.]/) print k "\t" p[k] }'
		# The law families, read off the directory rather than listed: "the citations family" is how
		# this repo names `docs/laws/citations.md`, and #492's false sentence used exactly that form.
		for f in docs/laws/*.md; do
			b="${f##*/}"
			printf '%s family\t%s\n' "$(printf '%s' "${b%.md}" | tr 'A-Z' 'a-z')" "$f"
		done
		# ADR numbers, in the spellings check 1 already accepts.
		for f in docs/decisions/*.md; do
			b="${f##*/}"
			n="${b%%-*}"
			printf 'adr %s\t%s\nadrs %s\t%s\ndecision %s\t%s\ndecisions %s\t%s\n' \
				"$n" "$f" "$n" "$f" "$n" "$f" "$n" "$f"
		done
	} >"$dvocab"
	printf '%s\n' "$prose_only" >"$dprose"
	if [ ! -s "$dvocab" ]; then
		echo "FAIL  check 7's location vocabulary came out empty, so no discharge claim could resolve"
		echo "      and the check would report a green it never computed. It is derived from"
		echo "      \`git ls-files\`, \`docs/laws/*.md\` and \`docs/decisions/*.md\`; one of those"
		echo "      answered nothing. A vacuous population is not a pass."
		rm -f "$dvocab" "$dprose"
		dfail=1
	fi
fi
if [ "$prmode" -eq 1 ] && [ "$dfail" -eq 0 ]; then
	# Two files rather than a pipe into a one-file program, because the vocabulary has to be read
	# before the prose and `NR == FNR` is how awk says that. The rows are `kind`, `path`, `sentence`,
	# and the placeholder `-` sits in the path column of the two note kinds: with a tab in `IFS` the
	# shell collapses a run of them, so an empty middle field would silently shift the sentence left.
	discharge_rows="$(awk '
	NR == FNR {
		i = index($0, "\t")
		if (i > 1) { k = substr($0, 1, i - 1); nk++; keys[nk] = k; kpath[k] = substr($0, i + 1) }
		next
	}
	/^\+\+\+/ { flush(); next }
	/^\+/ {
		s = substr($0, 2)
		# The three paragraph breaks. A blank line is a paragraph; a heading is a section label and
		# one of the seven section names is itself a listed verb; a list marker starts a new claim,
		# and welding two bullets would pair one verbs sentence with the next ones path.
		if (s ~ /^[ \t]*$/) { flush(); next }
		if (s ~ /^[ \t]*#+[ ]/) { flush(); next }
		if (s ~ /^[ \t]*([-*+][ ]|[0-9]+[.)][ ])/) flush()
		buf = (buf == "" ? s : buf " " s)
		next
	}
	{ flush() }
	END { flush() }
	function flush() { if (buf != "") scan(buf); buf = "" }
	# The cut lands on a **space**, never on a byte offset, because awk here is byte-oriented: a
	# length() of one em-dash is 3, so substr(s, 1, 177) kept two bytes of a three-byte character,
	# the fragment was not valid UTF-8, and the sort -u this feeds **refused it** — exit non-zero
	# with one line, "sort: Illegal byte sequence", which under pipefail became the whole verdict of
	# the script. A cosmetic defect in a quoted-back message, amplified into a verdict with no
	# located failure (grave #620). A space is 0x20 and no byte of a multi-byte UTF-8 sequence is
	# below 0x80, so cutting at one is cutting at a character boundary — no byte arithmetic, which
	# this awk cannot do. With no space in range the sentence is returned whole: truncation is
	# legibility, and a shorter line is not worth a broken one.
	#
	# (No apostrophe anywhere in this block, and that is not a style choice — the awk program is one
	# single-quoted shell word, so one apostrophe ends it. Every comment above is written around the
	# same constraint, and the first draft of this one did not notice.)
	function trunc(s,   t, i) {
		if (length(s) <= 180) return s
		t = substr(s, 1, 177)
		i = length(t)
		while (i > 0 && substr(t, i, 1) != " ") i--
		if (i <= 1) return s
		return substr(t, 1, i - 1) "..."
	}
	function scan(p,   plain, sent) {
		# Emphasis and code markers come out first: they are what stands between a copula-style
		# local match and its target, which is the spelling that hid two claims from check 6.
		plain = p
		gsub(/`/, "", plain)
		gsub(/[*]/, "", plain)
		# A run of blanks collapses, because the join keeps a wrapped lines own indentation and the
		# sentence is quoted back to a reader in a FAIL. The patterns below all read `[ ]+`, so this
		# is legibility rather than matching.
		gsub(/[ \t]+/, " ", plain)
		while (length(plain) > 0) {
			# A sentence ends at a terminator **followed by a space**, so a path does not end one.
			if (match(plain, /[.;!?] /)) {
				sent = substr(plain, 1, RSTART)
				plain = substr(plain, RSTART + RLENGTH)
			} else {
				sent = plain
				plain = ""
			}
			claim(sent)
		}
	}
	function claim(s,   low, t, pre, span, before, after, key) {
		low = tolower(s)
		t = low
		while (match(t, /(recorded|added|minted|filed|landed|repaired|stated)/)) {
			pre = substr(t, 1, RSTART - 1)
			before = (RSTART > 1) ? substr(t, RSTART - 1, 1) : " "
			after = substr(t, RSTART + RLENGTH, 1)
			span = substr(t, RSTART + RLENGTH)
			# Advance first, so every arm below can leave the loop by continuing and no verb is
			# scanned twice. The span is verb-to-sentence-end, which is the whole narrowing.
			t = span
			# Word boundaries by hand, because awk has none: `restated` is not `stated`.
			if (before ~ /[a-z0-9]/ || after ~ /[a-z0-9]/) continue
			# Exempt and printed: a modal or a negation asserts nothing about what landed, and a
			# past-tense copula records a transition rather than claiming a present state.
			if (pre ~ /(be|been|being|to|would|could|should|shall|will|may|might|must|not|never|nor|if|when|once|unless|was|were)[ ]+$/) {
				print "exempt\t-\t" trunc(s)
				continue
			}
			sawprep = 0
			key = locate(span)
			if (key != "") print "claim\t" kpath[key] "\t" trunc(s)
			else if (sawprep) print "unres\t-\t" trunc(s)
		}
	}
	# The location is the **head** of a locational prepositional phrase after the verb. Determiners
	# are stepped over; an adjective is not, which is under-match (d). `on` is deliberately absent:
	# "landed on 92/136" is a result and not a location, and it was measured on #492s body.
	function locate(u,   rest, i, best, bestlen) {
		while (match(u, /(^|[^a-z])(in|into|at|to|under|beside|inside|within)[ ]+((the|its|this|a|an)[ ]+)*/)) {
			rest = substr(u, RSTART + RLENGTH)
			u = rest
			sawprep = 1
			best = ""
			bestlen = 0
			# Longest match wins, so a path is never shadowed by a basename that prefixes it.
			for (i = 1; i <= nk; i++)
				if (index(rest, keys[i]) == 1 && length(keys[i]) > bestlen) {
					best = keys[i]
					bestlen = length(keys[i])
				}
			if (best != "") return best
		}
		return ""
	}
	' "$dvocab" "$dprose" | sort -u)"
	rm -f "$dvocab" "$dprose"
	dready=1
fi

fail=0
# Folded in here rather than set above, because `fail` is initialized below check 7's setup and a flag
# assigned before its initializer is a flag reset to 0. The FAIL paragraph is already printed; this is
# the verdict channel catching up to it.
if [ "$dfail" -eq 1 ]; then
	fail=1
fi
adrs=0
issues=0
graves=0
foreigns=0
verbs=0
# **The three coverage counters accumulate the numbers they saw and are counted distinct at print
# time, and grave #435 is why they are not integers incremented in place.** `closing_nums` is
# extracted with `sort -u` over **numbers**; the resolution loop iterates `cites`, which is `sort -u`'d
# over **class and number**. So one number under two classes is two rows and two visits, the counter
# counted visits, the extractor counted claims, and the comparison below fired on a number that was
# never dropped — while its message named a **drop** and pointed at `FAIL` lines that by construction
# do not exist for one.
#
# The two-class case is not exotic; **it is this repo's own convention for closing a grave.** A diff
# saying `closes #N` and also writing `grave #N` — the second being what the label check exists for —
# emits `issue N` and `grave N`, and trips the arm. Measured on `89388bf..a14e76f` as `6 of 5`: #395
# resolved once as `grave` and once as `issue`, check 5's arm printing its `ok` on both. Reproduced
# and fixed under mutation, which is also how the wrong first diagnosis was caught — the same claim
# written *twice* dedupes and reports `1 of 1` on HEAD, so "cited twice" was not the mechanism, and
# the mutation that was supposed to confirm it refuted it instead.
#
# Comparing two counts requires the two sides be counted the same way. That is the whole content of
# the defect, and the wrong first guess is kept because it names the thing that made the real
# mechanism invisible: both sides say `sort -u`, over different keys.
closing_seen=""
exempt_seen=""
state_seen=""

# Phase 0a: Go format verbs whose `#` flag looked like a citation (#308). Not resolved, because
# there is nothing to resolve; printed because a silently dropped token is an exemption mechanism.
for v in $(printf '%s\n' "$cites" | awk '$1 == "verb" { print $2 }'); do
	verbs=$((verbs + 1))
	echo "note  $v -> Go format verb flag, not a citation (see #308)"
done

# Phase 0: qualified references — a cross-project citation, or a markdown anchor shaped alike. Named,
# counted, and deliberately not resolved: see the `qualified` arm in extract() for why a gate must
# not depend on a sibling repo's numbering, and why this line does not claim which kind it is.
for q in $(printf '%s\n' "$cites" | awk '$1 == "qualified" { print $2 }'); do
	foreigns=$((foreigns + 1))
	echo "note  $q -> qualified reference, not resolved against this repo (provenance is the qualifier)"
done

# Phase 1: ADR citations, offline.
for n in $(printf '%s\n' "$cites" | awk '$1 == "adr" { print $2 }'); do
	adrs=$((adrs + 1))
	# The glob is the resolution: `docs/decisions/0025-*.md`. A number with no file is a
	# citation to a decision that does not exist, which is the ADR face of a guessed issue.
	found="$(find docs/decisions -maxdepth 1 -name "$n-*.md" 2>/dev/null | head -1)"
	if [ -z "$found" ]; then
		echo "FAIL  decision $n -> no docs/decisions/$n-*.md"
		echo "      An ADR citation resolves to a file or it is a citation to a record that does"
		echo "      not exist. Either the number is wrong — check \`ls docs/decisions/\` — or the"
		echo "      record was never written, in which case write it before citing it: an ADR is"
		echo "      the tombstone of a decision Scott has called, not a forward reference to one."
		# GRAVE #449: this line was missing, so the arm printed all five lines above and the
		# script returned 0 — a located, named, correctly-diagnosed finding that never reached
		# the verdict channel, on a green `make cite` and a green CI step. It is the **second**
		# time in this file: the header's C1 note records the same missing `fail=1` in another
		# arm, and that repair fixed the site the falsification pointed at without sweeping the
		# rest. A FAIL names a site, not the population.
		#
		# Phase 1b twenty lines down had it right the whole time, which is what makes the class
		# hard to see: a correct FAIL site and an incorrect one look identical locally — both
		# print, neither assigns — and what distinguishes them is whether some *other* line is
		# keyed on the same condition. `closecheck.sh:246` is the legitimate version of the
		# absence (its print is inside a piped `while`, so a flag would die with the subshell,
		# and its verdict is keyed on the same `$nfound`), which is why the sweep for this grave
		# had to be read rather than trusted.
		fail=1
	else
		echo "ok    decision $n -> ${found#docs/decisions/}"
	fi
done

# Phase 1b: a `decision NN` / `decision NNN` that is neither an ADR citation nor a sub-decision
# reference. Binding, because the alternative to reporting it is silently dropping it, and this
# arm exists precisely so narrowing the ADR trigger to four digits could not become an under-match.
for n in $(printf '%s\n' "$cites" | awk '$1 == "adrshort" { print $2 }'); do
	adrs=$((adrs + 1))
	echo "FAIL  decision $n -> ADR citations are four digits (docs/decisions/NNNN-*.md);" \
		"a one-digit reference is a record's own numbered decision. This is neither."
	echo "      Write the ADR's four-digit number if a record is meant, or name the record and"
	echo "      its internal decision — \"0025's decision 2\" — if a sub-decision is."
	fail=1
done

# Phase 2 and 3: issue citations, which need the network.
#
# `gh api repos/{owner}/{repo}/issues/N` rather than `gh issue view N`, and the difference is
# load-bearing: `gh issue view` resolves a *PR* number happily and reports it as an issue, so it
# cannot answer check 3 at all. The REST payload carries `pull_request` (present iff the number
# is a PR) plus the labels, in one request per citation.
need_gh="$(printf '%s\n' "$cites" | awk '$1 == "issue" || $1 == "grave"' | grep -c '' || true)"
if [ "$need_gh" -gt 0 ]; then
	if ! command -v gh >/dev/null 2>&1; then
		echo "FAIL  gh is not installed, so $need_gh issue citations were not resolved."
		echo "      This is not a pass. The offline half above ran; the network half did not,"
		echo "      and a check that could not ask its question does not get to report green."
		echo "      CI is where this half is binding (see .github/workflows/ci.yml)."
		exit 1
	fi
	for row in $(printf '%s\n' "$cites" | awk '$1 == "issue" || $1 == "grave" { print $1 ":" $2 }'); do
		kind="${row%:*}"
		n="${row#*:}"
		# **The category counters are incremented from the extractor's kind, here, and not from the
		# resolution arms below** (grave #416). They used to live in the two success arms, so any
		# citation whose lookup failed was counted in the total and in *no* category — and this
		# file's own header claims the opposite: "the total counts what the extractor matched, and
		# each category says what it was." The identity held on every green run and broke exactly
		# when something was wrong, which is the direction that matters: a reader comparing
		# `1 citation-shaped tokens (0 issue, 0 grave, …)` cannot tell a miscount from a failure.
		# Found by #410's own control, whose fixture is one citation that never resolves.
		if [ "$kind" = grave ]; then
			graves=$((graves + 1))
		else
			issues=$((issues + 1))
		fi
		# **stderr is kept, and the branch below is why** (#410). Any nonzero exit used to become
		# `does not resolve: no such issue or PR in this repo` — a claim about the *tracker* resting
		# on an exit code that cannot tell "the answer is no" from "the question was never asked".
		# Discarding stderr is what made the two indistinguishable, and the remedy names a wrong
		# action ("file the missing artifact") when the number was fine, which invites minting a
		# duplicate of an issue that already exists. Observed on a `--pr 409` run: #180 reported as
		# unresolvable at 4931/5000 rate limit, with the identical request answering out of band.
		#
		# This is the distinction `:620` already draws correctly for the whole phase — a check that
		# could not ask its question does not report green — applied at the granularity of one
		# citation, which is where it was abandoned. (That number was 479 when written and was
		# wrong before its own PR merged: grave #418, whose repair this is.)
		err="$(mktemp)"
		# `.state` is check 5's whole answer and rides this request rather than adding one; the
		# **title stays last**, because it is the only free-text field and a tab inside it would
		# shift every field after it.
		if ! meta="$(gh api "repos/{owner}/{repo}/issues/$n" \
			--jq '(if .pull_request then "pr" else "issue" end) + "\t" + ([.labels[].name] | join(",")) + "\t" + .state + "\t" + .title' 2>"$err")"; then
			# **HTTP 404 is the verdict; anything else is a mechanism failure.** Both exit nonzero,
			# because neither is a pass — but only one of them is a statement about the number, and
			# only one of them has "repoint the citation" as its remedy.
			if ! grep -q 'HTTP 404' "$err"; then
				echo "FAIL  could not resolve #$n -- the request failed: $(tr '\n' ' ' <"$err" | cut -c1-200)"
				echo "      This is a mechanism failure, not a verdict: the tracker was never asked, so"
				echo "      nothing here says whether #$n exists. Do NOT repoint the citation and do NOT"
				echo "      file a replacement artifact — either would act on an answer that was never"
				echo "      given. Re-run once the transport is working. Nonzero because a check that"
				echo "      could not ask its question does not get to report green (#410)."
				rm -f "$err"
				fail=1
				continue
			fi
			rm -f "$err"
			echo "FAIL  #$n -> does not resolve: no such issue or PR in this repo"
			echo "      A well-formed \`#N\` resolves to whatever N happens to be, so the number was"
			echo "      never checked against the tracker. Two remedies, and they are different:"
			echo "      if the artifact exists under another number, repoint the citation; if it does"
			echo "      not exist yet, \`gh issue create\` it and cite what comes back. Never guess"
			echo "      the next number — a guess that later comes true is a citation pointing at"
			echo "      itself, which is the defect check 4 below exists for."
			fail=1
			continue
		fi
		rm -f "$err"
		what="$(printf '%s' "$meta" | cut -f1)"
		labels="$(printf '%s' "$meta" | cut -f2)"
		state="$(printf '%s' "$meta" | cut -f3)"
		title="$(printf '%s' "$meta" | cut -f4)"
		# **The state field is validated, not trusted, and that is not defensiveness about GitHub.**
		# It is read *positionally* out of a tab-joined `--jq`, so a field added or reordered above
		# it silently hands check 5 a label list or a title — neither of which equals `open`, so
		# every closure claim in the tree would start passing and the check would report a green it
		# never computed. A mechanism failure, in the words this file already uses for one.
		case "$state" in
		open | closed) ;;
		*)
			echo "FAIL  #$n -> the tracker reported state \"$state\", which is neither open nor closed."
			echo "      Check 5 reads .state positionally out of this script's \`--jq\`; a reordered"
			echo "      or added field lands here. This is a mechanism failure and not a verdict"
			echo "      about #$n — nothing below has been established about whether it is closed."
			fail=1
			continue
			;;
		esac
		# Check 5, above the kind arms for check 4's reason: a closure claim can sit on a grave or on
		# a plain issue, and whether the label is right is a different question from whether the
		# sentence is true.
		if printf '%s\n' "$closing_nums" | grep -qx "$n"; then
			closing_seen="${closing_seen}${n}
"
			if [ "$state" = open ]; then
				echo "FAIL  #$n -> the diff claims to close it, and #$n is OPEN: $title"
				echo "      A comment saying it closes #$n asserts the code beside it dispatched #$n."
				echo "      Prose is not a channel the tracker reads, so the claim went one way and"
				echo "      nothing came back: the issue stays in the queue that the next product PR"
				echo "      is chosen from, and if it is a grave its lesson has nowhere to live, since"
				echo "      that goes in the *closing* comment (#325). Two remedies: close #$n — from"
				echo "      the PR, never by a keyword in the body, which is banned (closecheck.sh) —"
				echo "      or, if the work is not actually done, say what the code does instead."
				# **This line was missing in the first draft and the neuter is what found it.** The
				# check printed the whole FAIL paragraph above and the script exited 0: the finding
				# reached the mechanism channel and never reached the verdict channel, so CI would
				# have been green with the diagnosis in the log. It is the shape of grave #365 in a
				# check written to catch a claim nobody verified, and a reader of the output could
				# not have seen it — the words "FAIL" were all there. Only the exit code was wrong,
				# and only running the mutation reads the exit code.
				fail=1
			else
				echo "ok    #$n -> claimed closed by this diff, and $state: $title"
			fi
		fi
		if printf '%s\n' "$closing_exempt" | grep -qx "$n"; then
			exempt_seen="${exempt_seen}${n}
"
			echo "note  #$n -> a conditional or negated closure claim, $state — exempt from check 5:"
			echo "      a modal before the verb (\"would fix\", \"cannot close\") asserts nothing about"
			echo "      state. Printed rather than dropped, because an exemption nobody sees is an"
			echo "      exemption mechanism."
		fi
		# Check 6 (#432, grave #434): the sentence asserts a state and the tracker holds one. Placed
		# beside check 5 because it is the same question in the other direction — check 5 verifies a
		# claim that a citation *became* closed, this one a claim about what it *is* — and both read
		# the `.state` already in hand.
		#
		# **Two sentences this check carries at its site, on the order that landed it (Scott, PR #437
		# review), so that neither can be inferred away by a later reader.**
		#
		# **It does not catch grave #434's shape, and the reason is predication rather than proximity.**
		# That grave asserted openness by *paraphrase* — "has no vocabulary", "waits on", "carries it
		# alone" — and the question such a sentence turns on is which noun a state word is predicated
		# of, which is unbounded. This arm reads only the form where a copula makes the citation the
		# subject, so aboutness is settled by grammar and never guessed. The draft that appeared to
		# catch #434 matched the two state words anywhere in the sentence and was wrong about the
		# subject: in *"the issue stays open because #328's 103 vectors have no vocabulary yet"* the
		# subject of "stays open" is the issue, meaning #9, and not #328. Co-occurrence, not the
		# predicate. **Nobody should read this check as covering that shape.**
		#
		# **Measured yield to date: two true positives, one tracker state change, both on the prose of
		# the PR that added this arm.** The paragraph above the population stream said that #432 stayed
		# open; the same ruling that granted #432's paraphrase half no instrument *closed #432*, so a
		# sentence written minutes earlier became false and `make cite` failed with this arm's FAIL
		# paragraph pointing at it. **The sentence did not change, the world did** — which is the entire
		# failure mode, and nothing else in the tree could have noticed.
		#
		# The second catch is the one worth keeping. After repairing this file I pushed, and CI's
		# `--pr` half failed on **PR #437's own body**, which still said #432 stayed open in two places.
		# So the author had already been shown the lesson, applied it at the site he was looking at, and
		# left a second site standing in a channel he had stopped scanning: *the repair corrects the
		# instance and leaves the sourcing running.* That one was not self-inflicted in the same way —
		# no mutation, no hand, and the verdict arrived from a machine the author was not consulting.
		#
		# Before those it was **zero**: every other failure it has printed was a mutation fired by hand,
		# and the one historical catch credited to it belonged to the broad draft, made by co-occurrence
		# and wrong about which noun the sentence was about. Over the tracked tree the narrow form
		# appears in **9 places and all 9 agree** with the tracker (#111 ×2, #260, #326, #9 ×2 open;
		# #400, #53 ×2 closed), counted by running this arm's own trigger over `git ls-files` — an
		# earlier count of 6 was a search of the author's own devising and was short by three, which is
		# *measure with the instrument, not by hand* at one remove. Two of the nine sit inside quotation
		# marks, in prose about an earlier grave, and this arm counts them: a scanner reads tokens, not
		# quotation marks, and a false claim in a quotation is still on the page.
		#
		# Population caveat, since a yield figure invites the wrong reading: this arm sees **added
		# lines**, so those 9 standing claims are ungated until a diff touches them. Both catches were
		# lines this PR added.
		#
		# **Every claim on the number, not the first one, and this PR's own body is why.** The first
		# draft took `head -1`. A population can carry two contradictory claims about one number — a
		# mutation table reporting *both* directions of this check does exactly that, so the report
		# about the check was the specimen — and `head -1` compares one, passes or fails on it, and
		# says nothing about the other. Two opposite claims about one issue is itself a defect worth a
		# verdict, and the arm that would hide it is the arm that looks like it is working. Caught by
		# the coverage line below, which read `3 of 4` on this PR's body: *coverage is a claim*, and it
		# is the one instrument that can see an arm silently declining to ask.
		for claimed in $(printf '%s\n' "$state_claims" | awk -v n="$n" '$1 == n { print $2 }'); do
			state_seen="${state_seen}${n} ${claimed}
"
			if [ "$claimed" = "$state" ]; then
				echo "ok    #$n -> the sentence says $claimed and the tracker says $state: $title"
			else
				echo "FAIL  #$n -> the sentence says $claimed; the tracker says $state: $title"
				echo "      A citation resolving is not the same fact as the sentence around it being"
				echo "      true. This is grave #434: a paragraph named #328 as the live carrier of open"
				echo "      work the day after #328 closed, two bounds from the zero that #328's own"
				echo "      closure produced. Both premises came from a paragraph when the tracker was"
				echo "      one query away, and the second was written into the repair of the first —"
				echo "      **the lesson had been applied to the fact and not to the sourcing.**"
				echo "      Remedies, and they are different: if the work really is outstanding, the"
				echo "      issue is closed wrongly and reopening it is the fix; if it is done, the"
				echo "      sentence is stale and what replaces it must be sourced from the tracker,"
				echo "      not from another paragraph. Do not repoint the number to something open"
				echo "      that happens to be nearby — that is the same defect with a fresh subject."
				fail=1
			fi
		done
		# Check 4, before the kind-specific arms: a self-citation resolves like any other and is
		# wrong whatever kind it names, so the comparison belongs above the branch rather than
		# duplicated inside both of its arms.
		if [ -n "$selfpr" ] && [ "$n" = "$selfpr" ]; then
			if printf '%s\n' "$prose_nums" | grep -qx "$selfpr"; then
				echo "FAIL  #$n -> is the PR under review: $title"
				echo "      A citation cannot name the artifact it is written in. This is what a number"
				echo "      guessed before its PR existed looks like once the guess comes true: the"
				echo "      sentence reads correctly, the number resolves, and it points at itself."
				echo "      Cite the artifact that actually carries the ruling, or drop the number."
				fail=1
			else
				# Not a pass reported as silence: the token is there, and the reason it is allowed is
				# the sentence below. Doctoring the quoted block to remove it would be the fix that
				# fabricates evidence, which is why this arm exists rather than a stricter check.
				echo "note  #$n -> names this PR, but only inside a fenced block: quoted evidence, not"
				echo "      a claim. Check 4's population is the body's prose; see this script's header."
			fi
			continue
		fi
		if [ "$kind" = grave ]; then
			case ",$labels," in
			*,type:grave,*)
				if [ "$what" != issue ]; then
					echo "FAIL  grave #$n -> resolves to a PR, not an issue: $title"
					fail=1
				else
					echo "ok    grave #$n -> [$labels] $title"
				fi
				;;
			*)
				echo "FAIL  grave #$n -> $what without type:grave [${labels:-no labels}]: $title"
				echo "      Cited as a grave; the graveyard is \`label:type:grave\` and this is not in it."
				echo "      If it is a grave, label it — \`gh issue edit $n --add-label type:grave\` —"
				echo "      and put the lesson in the closing comment, since a tombstone with no"
				echo "      inscription reads as closed to every query that asks. If it is not a grave,"
				echo "      drop the word: cite it as a plain \`#$n\`."
				fail=1
				;;
			esac
		else
			echo "ok    #$n -> $what [${labels:-no labels}] $title"
		fi
	done
fi

# Check 7, in its own loop: it needs no per-citation request, so it is not welded into the resolution
# loop above, and a body that cites nothing can still make a false claim about its own diff.
#
# **The loop reads from a here-document rather than from a pipe, and grave #365's sibling is why.** A
# `while read` on the right of a `|` runs in a subshell, so `fail=1` and the three counters would die
# with it — the script would print its FAIL paragraph and exit 0, which is the shape this file has
# already paid for twice (the C1 note in the header, and grave #449 in phase 1). `closecheck.sh:246` is
# the legitimate version of that absence, and it is legitimate only because its verdict keys on a count
# that survives.
ndclaims=0
ndunres=0
ndexempt=0
if [ "$prmode" -eq 1 ]; then
	dtab="$(printf '\t')"
	while IFS="$dtab" read -r dkind dpath dsent; do
		[ -n "$dkind" ] || continue
		case "$dkind" in
		claim)
			ndclaims=$((ndclaims + 1))
			if printf '%s\n' "$prfiles" | grep -qxF "$dpath"; then
				echo "ok    $dpath -> a discharge claim names it and PR #$selfpr changes it: \"$dsent\""
			else
				echo "FAIL  $dpath -> a discharge claim in the body says the work landed here, and PR #$selfpr does not change it."
				echo "      The sentence: \"$dsent\""
				echo "      This is #493: two false claims shipped in #492, both about the author's own"
				echo "      diff, and one cause — a claim about the tree sourced from memory of having"
				echo "      typed it. Its sharp form is Scott's: an edit made after an amend is not in"
				echo "      the amend. Three remedies, and they are different: if the work belongs in"
				echo "      that file, do it and amend; if it landed somewhere else, name the file the"
				echo "      diff actually carries; if it is planned rather than done, say so in a tense"
				echo "      that claims it — \"to be recorded in X\" is exempt here and is honest."
				fail=1
			fi
			;;
		unres)
			ndunres=$((ndunres + 1))
			echo "note  a discharge claim whose location this check cannot resolve, so it is NOT checked:"
			echo "      \"$dsent\""
			echo "      The vocabulary is tracked paths, unique basenames, law families and ADR numbers;"
			echo "      \"at the site\" and \"in the changelog\" name no artifact. Printed rather than"
			echo "      dropped, because an under-match nobody sees is an under-match nobody prices."
			;;
		exempt)
			ndexempt=$((ndexempt + 1))
			echo "note  a discharge verb behind a modal, a negation or a past-tense copula — exempt, so"
			echo "      NOT checked: \"$dsent\""
			;;
		esac
	done <<DISCHARGE
$discharge_rows
DISCHARGE
fi

# The domain, printed whether or not anything failed. A checker that says OK without saying over
# what has made a silent claim about its own coverage.
if [ "$prmode" -eq 1 ]; then
	domain="PR #$selfpr (title and body)"
else
	domain="$(git rev-parse --short "$base")..$([ -n "$head" ] && git rev-parse --short "$head" || echo worktree)"
fi
printf 'citecheck %s: %d added lines, %d citation-shaped tokens (%d issue, %d grave, %d ADR, %d qualified, %d verb)\n' \
	"$domain" "$nlines" "$ncites" "$issues" "$graves" "$adrs" "$foreigns" "$verbs"

# Check 4's own domain, on its own line, because it runs in exactly one of the two modes and a
# reader of a diff-mode log would otherwise have no way to tell that it was not asked. *A skip is
# not a verdict*, and the diff-mode line is the sentence that keeps this one from reading as a pass.
if [ "$prmode" -eq 1 ]; then
	# Check 4's own population, printed: a fence-exclusion that collapsed to zero prose lines would
	# otherwise report a green from a check that asked nothing. *Coverage is a claim.*
	printf 'citecheck: self-citation check ran against PR #%s, over %d prose line(s) of %d (%d fence marker(s)).\n' \
		"$selfpr" "$nprose" "$nlines" "$nfences"
else
	echo "citecheck: self-citation check not applicable to a diff — a comment citing the PR it was" \
		"written in is this repo's attribution convention, so check 4's population is the body" \
		"alone. Run \`citecheck.sh --pr <n>\` for it; CI does, on the pull_request event."
fi

# Check 5's own domain, on its own line and for check 4's reason: it runs in exactly one of the two
# modes, and *a skip is not a verdict*. Zero claims is a legitimate state and is said in those words,
# because "no closure claim in this diff" and "no claim that was wrong" are different facts — the
# same distinction the zero-citations line at the bottom draws.
if [ "$prmode" -eq 1 ]; then
	echo "citecheck: closure-claim check not applicable to a PR body — a closing keyword there is" \
		"forbidden outright rather than verified (grave #314, closecheck.sh), so check 5's" \
		"population is the tree alone. It runs in the diff and --worktree modes; CI does both."
else
	# The extractor's count beside the resolver's, because they can disagree and the disagreement is
	# invisible otherwise: a claim whose number is dropped before check 5 runs — an unresolvable
	# lookup, a state field that failed validation — leaves a claim nobody asked about, and the
	# printed count would then be a claim about coverage this check does not have.
	nclaims="$(printf '%s' "$closing_nums" | grep -c '' || true)"
	nexempts="$(printf '%s' "$closing_exempt" | grep -c '' || true)"
	# Distinct on both sides — `sort -u` here because the extractor uses it there (grave #435).
	nclosing="$(printf '%s' "$closing_seen" | sort -u | grep -c '' || true)"
	nexempt="$(printf '%s' "$exempt_seen" | sort -u | grep -c '' || true)"
	printf 'citecheck: closure-claim check ran over %d of %d claim(s) in this diff, %d of %d exempt as conditional.\n' \
		"$nclosing" "$nclaims" "$nexempt" "$nexempts"
	if [ "$nclosing" -ne "$nclaims" ] || [ "$nexempt" -ne "$nexempts" ]; then
		echo "citecheck: check 5 did not reach every closure claim the extractor found — see the" \
			"failures above for which citation was dropped before its state could be read."
		# The pointer at the failures above is sound *because* both drop paths print one: an
		# unresolvable lookup and a `state` that failed the positional-field guard each emit a FAIL
		# before their `continue`. It was unfollowable only while this arm could also fire on a
		# repeat, which prints nothing and is not a drop (grave #435).
		fail=1
	fi
fi

# Check 6's own domain, on its own line and for check 4's and 5's reason. **Outside the mode branch,
# because this is the one network check that runs in both** — the grave's own specimen is PR #433's
# body, so a body is not a population to exempt here the way checks 4 and 5 exempt each other's. A
# state claim is a false sentence wherever it is written, and unlike a closing keyword it is not
# banned in a body, so there is nothing for a `--pr` run to defer to.
#
# Zero claims is a legitimate and frequent state: the narrow form is a copula, and most prose
# asserting a state paraphrases instead. That is the stated under-match, printed as a count so it
# cannot read as a pass — *a skip is not a verdict*, and neither is an empty population.
nstateclaims="$(printf '%s' "$state_claims" | grep -c '' || true)"
nstate="$(printf '%s' "$state_seen" | sort -u | grep -c '' || true)"
printf 'citecheck: state-claim check compared %d of %d stated claim(s) against the tracker.\n' \
	"$nstate" "$nstateclaims"
if [ "$nstate" -ne "$nstateclaims" ]; then
	echo "citecheck: check 6 did not reach every stated claim it extracted — a citation was dropped" \
		"before its state could be read, and the failures above say which."
	fail=1
fi

# Check 7's own domain, on its own line and for check 4's reason. The two note counts are printed
# **beside** the compared count rather than folded into it, because they are the check's stated
# under-match and a single total would hide it: 3 unresolvable locations and 0 compared claims is a
# green over a body that made three discharge claims, and that fact has to be legible from the log.
#
# **`dready` is why this is three sentences and not one.** A check whose oracle never answered prints
# *unavailable*, never `compared 0 claim(s)`: the second wording is the shape of a green computed over
# nothing, and the arm reaching it has already printed a FAIL saying which of the three mechanisms
# broke. The count of files is omitted there for the same reason — there is no such count.
if [ "$prmode" -eq 1 ] && [ "$dready" -eq 0 ]; then
	echo "citecheck: discharge-claim check UNAVAILABLE — its oracle (PR #$selfpr's changed-file list," \
		"or the derived location vocabulary) did not answer, so it compared nothing and asserts" \
		"nothing. The FAIL above says which. Not a pass, and not a claim about the body either."
elif [ "$prmode" -eq 1 ]; then
	printf 'citecheck: discharge-claim check compared %d claim(s) against PR #%s'"'"'s %d changed file(s); %d claim(s) named a location it could not resolve, %d exempt as conditional or past-tense.\n' \
		"$ndclaims" "$selfpr" "$nprfiles" "$ndunres" "$ndexempt"
else
	echo "citecheck: discharge-claim check not applicable to a diff — a comment saying \"recorded in" \
		"X\" is a claim about the tree, not about the diff it sits in, so check 7's population is" \
		"the body alone. Run \`citecheck.sh --pr <n>\` for it; CI does, on the pull_request event."
fi

if [ "$fail" -ne 0 ]; then
	# **Cause-neutral, and grave #435's second leg is why it used to name one.** This line read "at
	# least one citation does not resolve to the artifact it names" — true when there was one way to
	# fail, and false for four of the five that exist now: a wrong `type:grave` label, a closure claim
	# on an open issue, a coverage mismatch, a mechanism failure reading `.state`, and check 6's state
	# disagreement. Check 6 is the sharpest counter-example, since a citation that resolves perfectly
	# to an issue whose state contradicts the sentence around it is the *opposite* of not resolving,
	# and is the whole reason the check exists. Every check added since inherited the sentence without
	# touching the line, so no author read it against their own failure mode.
	#
	# The verdict channel does not need a cause and cannot source one from a shared flag: *an exit
	# code can't say why; output can't say whether.* The `FAIL` paragraphs above carry the why, each
	# with its own remedy, which is what they are for.
	echo "citecheck: at least one check above failed — the FAIL lines say which check and why."
	exit 1
fi
if [ "$ncites" -eq 0 ]; then
	echo "citecheck: this diff cites nothing — zero citations, which is not the same fact as zero failures."
fi
