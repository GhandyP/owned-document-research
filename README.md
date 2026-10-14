# owned-document-research

Answer a research question from your own documents, with the evidence, the
conflicts, and the permissions on the table.

Built with **Go 1.26** and **Google ADK Go v2** (Gemini).

A research agent is easy to write and hard to trust. Two failure modes dominate
once one of them is used for real work:

1. **A quote you cannot check.** The answer reads well, and nobody can tell
   which sentence of which document, in which version, produced it.
2. **A document that should not have been read.** Permissions are enforced in
   the UI, or in the retrieval layer, or hopefully both — and a passage from a
   restricted document ends up in an answer that a model will happily repeat.

This repository is built so both are structurally hard. Every claim carries
verified quotes with rune offsets into a named document version, the tool
reports disagreement instead of averaging it away, a restricted reference is
indistinguishable from an absent one, and nothing is published without an
attributable human approval.

## Quickstart

No credential is needed for any of the following: the default route quotes the
corpus with deterministic rules.

```bash
# A complete answer over the bundled corpus: conflict, gap, evidence table.
go run . demo

# Ask something of your own, optionally naming the documents to use.
go run . research -question "How is handling time measured?" -docs ops-handbook

# The same answer as JSON, for a machine.
go run . research -question "Did the intake pilot reduce handling time?" -json

# Who may read what, which is the first thing to check when an answer looks off.
go run . acl

# What this installation can and cannot do.
go run . verify

# Publish a reviewed answer. Without an approval, it writes nothing.
go run . export -question "Did the intake pilot reduce handling time?" \
  -out out/answer.md -approve -reviewer ana@acme.test
```

Exit codes: `0` success, `1` failure, `2` usage error.

`demo` names three documents, one of which has nothing to say about the
question. Here is what it prints, abridged:

```
**Question:** Did the intake pilot reduce handling time?
**Confidence:** low
This answer is advisory: it informs a human decision, it does not make one.

### c1 — The intake pilot reduced handling time by a third across the first quarter.
- status: supported
- confidence: low
> The pilot did not change handling time, according to the early read of the first month.
> — `pilot-notes@1`, from "Intake pilot notes" owned by ana@acme.test, tenant drive-acme,
    contradicts the statement (from a superseded version)

## Conflicts
The tool does not choose between these. A human does.
- **polarity** on "The intake pilot reduced handling time by a third…":
  ops-handbook@1, pilot-notes@1, pilot-notes@2 — unresolved: human review required
- **superseded_version** on "The intake pilot reduced handling time by a third…":
  pilot-notes@1, pilot-notes@2 — unresolved: human review required

## Gaps
- **no_match** (canteen-memo): no passage of this document matches the question
```

## How it works

```
        question + the identity asking it
                     │
        ┌────────────▼─────────────┐
        │ permissions              │  tenant → owner → allow list
        │ (engine, on every read)  │  restricted ≡ absent
        └────────────┬─────────────┘
                     │  ACL-filtered passages
        ┌────────────▼─────────────┐
        │ extraction               │  ADK agent, holds search_owned_documents
        └────────────┬─────────────┘
                     │  candidate quotes (a proposal, nothing more)
        ┌────────────▼─────────────┐
        │ validation               │  the quote must appear verbatim in the
        │ (deterministic)          │  body of the version it cites
        └────────────┬─────────────┘
                     │  validated evidence with rune offsets
        ┌────────────▼─────────────┐
        │ synthesis                │  ADK agent, holds no tools
        └────────────┬─────────────┘
                     │  findings
        ┌────────────▼─────────────┐
        │ claims, conflicts, gaps  │  confidence floors at the weakest claim
        └────────────┬─────────────┘
                     │  advisory answer
        ┌────────────▼─────────────┐
        │ approved export          │  fails closed without a signed decision
        └──────────────────────────┘
```

The package is split in two halves on purpose:

- A **deterministic half** — documents, permissions, retrieval, quote
  validation, conflicts, gaps, confidence — owns every fact in an answer.
- A **model half** — `Extractor` and `Synthesizer` — only proposes. Nothing a
  model returns becomes a claim until the deterministic half has matched it
  against the frozen document bytes.

That boundary is why the whole suite runs offline with no network and no
credential, and why a model that hallucinates cannot put anything into an
answer. The default route replaces the model half with rules, so the repository
is useful before anyone has a key.

## Design decisions

**The model is a witness, not a judge.** It proposes quotes; the engine decides
what evidence is. A candidate must name a reference that was actually retrieved
for this principal, an exact version, a valid polarity, and a quote that appears
verbatim in that version's body. Everything else is discarded — and reported in
`warnings`, because a rejected proposal is a signal, not noise.

**Confidence can only go down.** Merging evidence takes the weaker of the two,
an answer is no stronger than its weakest supported claim, a claim whose
evidence argues both ways is capped at low, and any unresolved conflict caps the
whole answer at low. A claim that no validated quote survives is still reported,
marked unsupported, with the reason.

**Conflicts are never resolved.** Two documents that disagree produce a
`polarity` conflict naming both sides. Evidence from a version that a newer one
replaced produces a `superseded_version` conflict naming the old version and its
replacement. Neither is silently dropped and neither is automatically believed.

**A superseded version never supports a live claim.** Stale quotes stay in the
answer, marked `superseded`, but a statement standing only on them is
unsupported. This is the difference between "the early read said nothing
changed" and "nothing changed".

**Permissions are checked twice, and nothing leaks the difference.** The engine
filters by tenant, owner and allow list before extraction, and re-checks what
the adapter returns. A forbidden reference and one that does not exist produce
the same gap, the same detail text and the same error. A test asserts that the
two answers are byte-identical once the caller's own reference string is
normalised, so the answer can never be used as an existence oracle.

**The agent's tools take no identity.** `search_owned_documents` is bound to the
principal when it is built; there is no parameter a prompt could set. It is also
the only tool: nothing here gives a model a way to write.

**Publishing is a separate, approved action.** `Authorizes` fails closed four
ways at once: no decision, a rejection, an approval issued for a different
action, and an approval nobody signed or dated. The default gate denies
everything.

## The document model

| field | meaning |
| --- | --- |
| `id` | stable document identity, shared across versions |
| `version` | 1 or greater; a document is never overwritten in place |
| `supersedes` | `"<id>@<version>"` this version replaces |
| `tenant` | the boundary that cannot be crossed, whatever the allow list says |
| `owner` | always readable by its owner |
| `allow` | `"user:<id>"`, `"group:<name>"`, or `"*"` inside the tenant |
| `text` | the body a quote is validated against |

A corpus file is a list of those documents:

```json
{
  "documents": [
    {
      "id": "pilot-notes",
      "title": "Intake pilot notes",
      "owner": "ana@acme.test",
      "tenant": "drive-acme",
      "allow": ["group:ops", "user:carla@acme.test"],
      "version": 2,
      "supersedes": "pilot-notes@1",
      "updated_at": "2026-03-14T09:00:00Z",
      "text": "The intake pilot reduced handling time by a third across the first quarter."
    }
  ]
}
```

Unknown fields are refused rather than ignored: a misspelled key in a
permissions file would otherwise drop an owner or an allow entry, and the corpus
would quietly mean something other than what it says.

## Asking Google Drive instead of a file

`adapters/drive.go` implements the same port over the Drive API v3, so the
engine treats a shared drive and a JSON file alike. Two limits are stated in the
code rather than hidden:

- **Only the current revision is served.** A caller asking for an older version
  is answered exactly as if the document did not exist, rather than being served
  the current body under an old reference.
- **Supersession is left empty.** Drive exposes a revision counter, not a
  statement that one revision replaced another, so the adapter maps the counter
  into `version` and does not invent a link between files.

Its mapping is exercised offline over a fake transport — Drive sharing becomes
the allow list, the shared drive id becomes the tenant, a Google-native file is
exported as text, a binary file contributes metadata and no body. **The live
path has never been executed**: it needs user credentials that are not part of
this repository, and no claim here should be read as saying otherwise.

## The ADK wiring

Two agents, one bounded session:

- `evidence_extraction` holds `search_owned_documents` and an `OutputSchema` for
  candidate quotes.
- `evidence_synthesis` holds no tools and an `OutputSchema` for findings.

Both runners share one `session.InMemoryService()` and one session id, so the
synthesis stage sees what the extraction stage was told. Two behaviours were
established against the `adk/v2` v2.2.0 source rather than from documentation,
and both are recorded in the code:

- **Task mode is not usable through `runner.Run`.** With `Mode: ModeTask` the
  framework collects the schema through an injected `finish_task` tool, but that
  path is driven by a workflow node: a plain runner run yields no model call at
  all. In the default mode the framework puts the agent's `OutputSchema` on the
  request as a native response schema, which is what this package uses.
- **Structured output arrives in two shapes.** `event.Output` is populated on
  the workflow node path only. On the runner path the schema-constrained JSON
  arrives as the final text turn, so the workflow accepts either shape and
  parses it through this package's own types.

The agent route needs `GOOGLE_API_KEY`:

```bash
export GOOGLE_API_KEY="..."
go run . research -question "Did the intake pilot reduce handling time?" -agent
```

It fails closed without a key, naming the missing variable, and `verify` reports
the model it would use (`ADK_MODEL` overrides `gemini-flash-latest`).

## Fixtures

`testdata/requests.json` states what seven requests must produce, and the CLI
tests check each one end to end:

| fixture | what it proves |
| --- | --- |
| conflicting and superseded evidence | a polarity conflict and a superseded conflict, both unresolved |
| mutually supporting evidence | several quotes for one claim, confidence medium |
| a superseded version on its own | never a supported claim; the gap explains why |
| an irrelevant document | no claim is forced out of a document that says nothing |
| a document this principal may not read | an `unavailable` gap, and no trace of its text |
| an empty document | absence of content is an outcome, not an error |
| no evidence at all | `no_evidence`, zero claims, no invented answer |

## What this is not

- Not a vector store, and not a retrieval benchmark. Retrieval is deliberately
  simple lexical matching with rune offsets, so that every claim can be traced
  back to exact bytes; swapping in embeddings would not change the validation,
  conflict or permission layers.
- Not a Drive integration that has been run. See the two limits above.
- Not a service. There is no HTTP surface, no queue, no database, and no write
  path other than the reviewed export.
- Not a decision maker. Every answer is advisory, and every conflict says so.

## Verification status

Observed on this tree: `gofmt -l .` clean, `go vet ./...`, `go build ./...` and
`go test ./... -race -count=1` all exit 0, and one test builds the binary and
runs it so the process interface is covered too. The whole suite runs with no
network and no credential.

Never executed here, and therefore not claimed: the live Gemini path (no
`GOOGLE_API_KEY` exists in this environment) and the live Google Drive path (no
user credentials). Their mapping and their failure handling are covered offline;
their network behaviour is not.

## Repository layout

```
main.go                     the command line
main_test.go                end-to-end tests over the fixtures
adapters/
  jsoncorpus.go             a corpus file as a document source
  drive.go                  Google Drive as the same source
internal/research/
  schema.go                 the frozen contract
  document.go, acl.go       the document model and the permission rule
  source.go, retrieve.go    the port, the in-memory source, retrieval
  engine.go, evidence.go    orchestration and quote validation
  conflict.go, gaps.go      disagreement, missing information, confidence
  agent.go, tools.go        the ADK agents and the search tool
  offline.go                the rules that replace the model when there is no key
internal/review/
  gate.go                   approvals that fail closed
  export.go                 the reviewed Markdown and JSON export
testdata/
  corpus.json               the corpus the demo and the tests use
  requests.json             what seven requests must produce
docs/
  case-brief.md             the brief this repository implements
  design.md                 invariants, threats, and why each one is enforced
```

## License

MIT. See [LICENSE](LICENSE).
