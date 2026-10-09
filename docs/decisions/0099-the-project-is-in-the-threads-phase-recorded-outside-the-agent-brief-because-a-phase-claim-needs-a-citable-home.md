# 0099 — The project is in the threads phase, recorded outside the agent brief because a phase claim needs a citable home

Date: 2026-10-08 · Status: **accepted** — Scott's ruling on the #534 review, confirmed 2026-10-08 (*"cut release, record what is needed"*) when he authorised recording it
Ratio-Class: ordered

Recorded by the actor the ruling was given to, which is not independent provenance — *durability is not independence*. The class is `ordered` rather than `carried` because this is a principal's decision on a stated question rather than a mechanism choice, which is the distinction [ADR 0093](0093-a-speculative-gate-class-for-features-ahead-of-the-spec-never-default-on-off-the-board-and-labelled-where-it-leaks.md) and ADR 0085's amendment 1 drew.

## Context

**The project is in the v1 threads-and-safepoints phase. The problem was never which phase — it was that nothing outside the agent's own brief said so.**

`CLAUDE.md` has recorded *"Current phase: **v1**, since the signed `v0.4.0` tag of 2026-08-28"* for some time, on Scott's ruling on the #534 review. That ruling is real. But three things about where it lived made the claim unusable by anyone checking it:

1. **`CLAUDE.md` is the agent brief**, written by the actor the ruling was given to. A phase is a claim the *project* makes about itself; resting it on the implementation agent's own notes is the shape *durability is not independence* names.
2. **The `v0.4.0` release block says the opposite, in writing, and is not marked otherwise.** Its own words: *"**What this release is not:** it is not v1. … v0 closing means v0's conditions are discharged, not that the next phase has begun."* True when written — the §§2–5 artifacts had not started — and left standing across all the work that falsified it.
3. So a reader outside `CLAUDE.md` who went looking found **a release note denying it and nothing affirming it.** That is worse than an unrecorded phase: it is a contradiction in the record with no marker saying which half is current.

It surfaced the way these things do. The README's "Where this is" heading was repaired from *"v0, the interpreter phase"* — stale by a phase and a release — to *"v1, the threads phase, since the signed `v0.4.0` tag"*, and the chair's pre-merge check asked where that was recorded. The answer was: **the tag's notes say it is not v1.** The repair had cited, for the phase change, the one document that denies it. The README was then changed to state only v0's closure and to decline to name a phase at all, which is the correct interim state and not a resting place — a project that cannot say which phase it is in has a records problem, not a phase problem.

## Decision

**The project is in the v1 threads-and-safepoints phase**, and this ADR is where that is recorded.

The authority is Scott's, twice: the ruling on the #534 review that no value naming v0 is true, and his 2026-10-08 confirmation authorising this record. Nothing here is the actor's judgement about which phase the project is in — only about the fact that the claim needed a home with a number.

**What advanced the phase line, stated because the obvious candidates are both wrong.** Not the `v0.4.0` tag: its notes deny it. Not the discharge of v0's closure conditions on its own: discharging a phase's conditions is not the next phase beginning, which is exactly what that release note says. What advanced it is that **v0's conditions are discharged and its milestone is closed**, so no value naming v0 is true — Scott's reasoning on #534, which dissolved #527 rather than adjudicating it, since both of that issue's readings turned on which phase was current.

**Three consequences, each a place the record changes:**

1. **The `v0.4.0` changelog block gains a superseded marker**, pointing here. The original sentence stays — a release note is a record of what was claimed at the time, and rewriting it would destroy that. What it gains is a reader's way out of the contradiction.
2. **The README may name the phase, citing this ADR.** It declined to while the only home was the agent brief.
3. **`CLAUDE.md` stops being the only place the phase is recorded.** It is not edited by this ADR; its phase paragraph becomes a pointer to a citable decision rather than the sole authority.

## Consequences

- **A phase claim is now checkable by someone who has not read the agent brief**, which is the whole point. The test a reader applies — *where is this recorded?* — has an answer with a number.
- **The contradiction is marked, not erased.** Anyone reading the `v0.4.0` notes still meets *"it is not v1"*, and now also meets the pointer saying what overtook it. That is the honest shape: the release note was true about the artifacts when written, and is overtaken rather than wrong.
- **This ADR does not move the phase, and must not be read as doing so.** It records a ruling already given. If the project's phase changes again, that is a principal's decision and will need its own record — and this ADR is then the thing to supersede, which is the property it exists to provide.
- **`v1.0.0` remains reserved and is untouched.** ADR 0004's row, as restated by its 2026-09-18 amendment (#795), gates `v1.0.0` on requirements this ADR says nothing about — being *in* the threads phase is not the same as having finished it. In particular §4's litmus battery beyond what spawn needed is outstanding and, by Scott's #670 ruling, **not currently scheduled**. A reader who takes "the threads phase" to mean the threads work is done would be wrong, and that is worth the sentence.
- **No code changes.** This is a records decision, so the one thing to check is that the record now agrees with itself: `CLAUDE.md`, this ADR, the README, and the `v0.4.0` block with its marker.
