# Case brief

This repository implements the brief below. It is the plan that the project was
built from, kept here so the goal stays auditable even as the implementation
grows past it.

The brief came from a case directory of a private Go + ADK cookbook and is
reproduced verbatim.

---

## 21 — Research over owned documents

### Goal

Answer a research question from supplied document excerpts while exposing
evidence conflicts, missing information, and source ownership.

### Run the current prototype

From this case directory:

```bash
export GOOGLE_API_KEY="your-key"
go run .
```

`internal/adkhelper.Run` uses ADK Go v2.2.0, Gemini, and `gemini-flash-latest`;
set `ADK_MODEL` to override the model.

Sample prompt:

```text
Compare these excerpts and answer the research question with an evidence table.
```

### What exists now

- `main.go (exists)` sends excerpts and a question to one focused Gemini ADK
  agent.
- The result is free-form text; document IDs, quotes, claim polarity, conflicts,
  and confidence are not typed.
- This is intentionally text-only: it does not index owned documents, retrieve
  passages, or enforce document permissions.

### Missing elements

- **Contract:** Define `ResearchRequest{question, documentRefs[]}` ->
  `ResearchAnswer{claims[], evidenceTable[], conflicts[], gaps[], confidence}`.
- **ADK workflow:** Add evidence-extraction and synthesis sub-agents with schemas
  and a research session; a `search_owned_documents` tool must return
  ACL-filtered passages.
- **Domain boundary:** Add document index, metadata, ownership/ACL, citation, and
  version adapters.
- **Fixtures and validation:** Include mutually supporting, conflicting, stale,
  irrelevant, and empty excerpts; test quote fidelity and "insufficient
  evidence".
- **Safety:** Preserve tenant/owner permissions and require a human review for
  high-impact research conclusions.

### Target structure

```text
21-owned-document-research/
├── main.go (exists)
├── README.md (exists)
├── workflow.go (next)
├── schema.go (next)
├── adapters/document_index.go (next)
├── testdata/excerpts.json (next)
└── main_test.go (next)
```

### Implementation order

1. Define claim, evidence, conflict, gap, and citation schemas.
2. Build an in-memory evidence table with source ownership fields.
3. Add ACL-filtered retrieval and extraction/synthesis sessions.
4. Test conflicts, stale versions, irrelevant excerpts, and no evidence.
5. Add reviewed export for research answers only after permission tests pass.

### Done when

- [ ] Every claim has a quote, source ID, and confidence or is marked unsupported.
- [ ] Conflicting and missing evidence is visible in the final answer.
- [ ] Fixtures prove ACL, stale-version, and no-match behavior.
- [ ] High-impact conclusions remain advisory and human-reviewed.

### Next improvement

Make the evidence table typed and permission-aware before adding document
indexing.

---

## Appendix: the prototype this repository replaces

The case shipped a fifteen-line `main.go` that handed a prompt to one free-form
model call through a cookbook helper. It could not be reused: the helper lived in
another module's `internal/` tree, which Go forbids crossing, and a free-form
answer carries none of the contract, evidence, or permissions the brief asks
for. Both original files are dropped; the brief above is the part worth keeping.

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/GhandyP/adk-go-enterprise-cookbook/internal/adkhelper"
)

func main() {
	if err := adkhelper.Run(
		context.Background(),
		os.Args[1:],
		"case_21_owned_document_research",
		"A text-only starting point for research over owned documents.",
		"Answer the research question using only the document excerpts supplied by the user. Separate evidence, conflicts, gaps, and conclusions; do not claim to search owned files.",
	); err != nil {
		log.Fatal(err)
	}
}
```
