# Design notes

This document records the invariants this repository enforces, why each one
exists, how it is enforced, and what is deliberately not defended. It is written
for someone deciding whether to trust the tool, and for someone about to change
it.

## The one structural decision

Every fact in an answer is produced by deterministic code. The model is a seam
that proposes candidates, and a proposal has no path into an answer except
through validation against the bytes of a document version.

That is why the package has two halves, why `Extractor` and `Synthesizer` are
interfaces rather than calls, and why the offline path can replace them with
rules without changing anything else. It is also the reason the whole test suite
runs with no network and no credential: nothing in the deterministic half ever
reaches out.

## Invariants

Each invariant names how it is enforced at run time and which test pins it.

### 1. A supported claim carries a quote that exists in the cited version

**Enforced in** `internal/research/evidence.go`: a candidate must name a
retrieved reference, an exact version, a valid polarity and a non-empty quote;
the quote is located in the body fetched for that version; the evidence records
rune offsets into that body.

**Pinned by** `TestEvidenceMustQuoteTheCitedVersionVerbatim` (five kinds of bad
proposal are rejected and reported), `TestQuoteOffsetsAreRuneExact` (a byte
offset would fail on non-ASCII text).

**Why**: an answer whose quotes cannot be located is an opinion with citations
painted on.

### 2. Rejection is visible

**Enforced in** `internal/research/engine.go` and `evidence.go`: every discarded
proposal, passage or finding produces a warning; the warning list is capped at
twenty entries and then says how many more were discarded.

**Pinned by** `TestEvidenceMustQuoteTheCitedVersionVerbatim`,
`TestEngineRechecksPermissionsOnARecklessSource`, and the workflow test that
asserts a fabricated quote is reported.

**Why**: a silently dropped proposal is indistinguishable from one that was
never made, and a reader cannot judge an answer by what it hides.

### 3. Confidence can only fall

**Enforced in** `schema.go` (`Weaker`), `evidence.go` (`evidenceCap`,
`answerConfidence`): merging takes the weaker confidence, a claim whose evidence
argues both ways is capped at low, any conflict caps the answer at low, and an
unsupported claim caps it at medium.

**Pinned by** `TestConflictingEvidenceIsReportedAndNeverResolved`,
`TestConfidenceWeakerKeepsTheLowest`, and the fixture suite.

**Why**: confidence that rises as evidence accumulates is the failure mode that
makes a research agent dangerous. It should degrade as the corpus disagrees.

### 4. Disagreement is reported, never settled

**Enforced in** `internal/research/conflict.go`: opposite polarities on one
statement produce a `polarity` conflict; evidence from a replaced version
produces a `superseded_version` conflict naming both the replaced version and
its replacement. The only resolution text this package can produce says a human
has to decide.

**Pinned by** `TestConflictingEvidenceIsReportedAndNeverResolved`.

**Why**: picking a winner is the one thing a tool with no authority must not do,
and averaging the two is picking the average.

### 5. A replaced version cannot support a live claim

**Enforced in** `evidence.go`: a claim whose every quote comes from a superseded
version is reported as unsupported with a reason, and its quotes stay visible
marked `superseded`.

**Pinned by** `TestASupersededVersionCannotSupportALiveClaim` and the
`a superseded version on its own` fixture.

**Why**: "the early read said nothing changed" and "nothing changed" are
different statements, and the first one is only true in the past tense.

### 6. The tenant boundary is absolute

**Enforced in** `internal/research/acl.go`: `readableBy` refuses before it looks
at the allow list when the tenants differ, so no `user:` entry, group or
wildcard can cross it.

**Pinned by** `TestReadableByEnforcesTenantThenOwnershipThenAllow`,
`TestDriveSourceNeverReturnsAnotherTenant`, and the corpus adapter test.

**Why**: sharing mistakes are normal; a permission model where a single wrong
entry leaks another customer's documents is not.

### 7. A restricted reference is indistinguishable from an absent one

**Enforced in** `source.go` (`ErrUnavailable` for a missing document, a
forbidden one and a missing version alike), `gaps.go` (one `unavailableDetail`
string for both), and the engine, which reports the same gap either way.

**Pinned by** `TestGetReportsMissingAndForbiddenAlike`,
`TestAForbiddenReferenceAnswersExactlyLikeAnAbsentOne` (the two marshalled
answers are byte-identical once the caller's own reference is normalised),
`TestDriveSourceHidesForbiddenAbsentAndStaleRevisionsAlike`.

**Why**: any observable difference turns the answer into an oracle for the
existence of documents the caller has no right to know about.

### 8. Permissions are checked on what the adapter returns, not only inside it

**Enforced in** `engine.go` (`scope`, `passagesFor`) and `evidence.go`: metadata
from `List`, passages from `Search` and bodies from `Get` are all re-checked
against the permission rule before use.

**Pinned by** `TestEngineRechecksPermissionsOnARecklessSource`, which drives an
adapter that has no permission logic at all and asserts that nothing from
another tenant reaches the answer.

**Why**: the boundary has to hold even when the component that was supposed to
hold it is wrong.

### 9. The model cannot widen its own access

**Enforced in** `internal/research/tools.go`: the principal is bound when the
search tool is constructed, `SearchArgs` has no identity or permission field,
and the tool is read-only. It is the only tool the agents hold.

**Pinned by** `TestSearchToolStaysInsideThePrincipalScope`,
`TestSearchToolBoundsTheLimit`, and the workflow test that asserts the synthesis
agent holds no search tool.

**Why**: a permission that can be requested in a prompt is not a permission.

### 10. Nothing is published without an attributable approval

**Enforced in** `internal/review/gate.go`: `Authorizes` requires an approval for
exactly this action, with a reviewer and a moment. `Export` checks it before it
writes anything, and removes the sidecar if the second write fails.

**Pinned by** `TestExportRefusesEveryDecisionThatIsNotThisApproval`,
`TestExportWritesOnlyWhenAReviewerApproves` (end to end through the command
line, asserting the files do not exist after a refusal).

**Why**: an advisory answer that can be published by accident is not advisory.

## Threat model

| threat | handled by |
| --- | --- |
| a model inventing a quote, a reference or an index | invariants 1, 2 |
| a model quietly widening its own scope | invariant 9 |
| an adapter that forgets to filter | invariant 8 |
| two versions of the same document disagreeing | invariants 4, 5 |
| a caller probing for documents it may not read | invariant 7 |
| a corpus file whose keys are misspelled | the adapter refuses unknown JSON fields |
| a runaway model flooding an answer | warnings are capped and counted; passages per document are capped; the tool's page size is capped |
| an unreviewed answer being published | invariant 10 |

Deliberately out of scope: protecting the corpus file itself, rate limiting,
identity federation, and anything about who may run the binary. This is a
library and a command line, not a deployment.

## Known limits

- **Lexical retrieval.** Matching is word-based with a prefix rule for
  stemming, and a passage is the best blank-line block of a document. It is
  predictable and traceable, which is what the validation layer needs, and it is
  not a good semantic retriever.
- **One claim per question on the rule path.** `RuleSynthesizer` states the
  question's answer as its strongest quote and attaches every other quote to it.
  That is what puts disagreement inside one claim, and it means a multi-part
  question needs the agent path.
- **Supersession is explicit.** A version is stale only when another version
  names it in `supersedes`. The Drive adapter cannot produce that link, so Drive
  documents do not get staleness conflicts.
- **The live model and Drive paths are unexecuted here.** Everything else was
  observed; those two need a credential this repository does not carry.
