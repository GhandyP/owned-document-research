// Package research answers a research question from owned document excerpts,
// keeping the evidence, the conflicts, and the permission boundary on the table.
//
// The package is deliberately split in two halves:
//
//   - A deterministic half — documents, permissions, retrieval, quote
//     validation, conflicts, gaps — that owns every fact in the answer.
//   - A model half — the Extractor and Synthesizer seams — that only proposes
//     candidates. Nothing a model returns becomes a claim until the
//     deterministic half has matched it against the frozen document bytes.
//
// That boundary is why the package is testable offline: the deterministic half
// runs with no network, and the model half has a deterministic implementation
// for tests.
package research

import (
	"context"
	"fmt"
)

// Confidence is the confidence carried by a claim, or the floor of the
// confidences of the claims backing an answer.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
	// ConfidenceNone marks a claim that carries no supporting evidence.
	ConfidenceNone Confidence = "none"
)

// Valid reports whether c is one of the defined confidence levels.
func (c Confidence) Valid() bool {
	switch c {
	case ConfidenceHigh, ConfidenceMedium, ConfidenceLow, ConfidenceNone:
		return true
	default:
		return false
	}
}

// rank orders confidences from the strongest to the weakest.
func (c Confidence) rank() int {
	switch c {
	case ConfidenceHigh:
		return 0
	case ConfidenceMedium:
		return 1
	case ConfidenceLow:
		return 2
	default:
		return 3
	}
}

// Weaker returns the weaker of two confidences. Merging evidence may only lower
// confidence: a claim backed by one strong and one weak source is a weak claim.
func Weaker(a, b Confidence) Confidence {
	if a.rank() >= b.rank() {
		return a
	}
	return b
}

// Polarity is how a passage relates to the statement it is quoted for.
type Polarity string

const (
	PolaritySupports    Polarity = "supports"
	PolarityContradicts Polarity = "contradicts"
)

// Valid reports whether p is one of the defined polarities.
func (p Polarity) Valid() bool {
	return p == PolaritySupports || p == PolarityContradicts
}

// ConflictKind classifies why evidence disagrees.
type ConflictKind string

const (
	// ConflictPolarity marks two documents asserting opposite things.
	ConflictPolarity ConflictKind = "polarity"
	// ConflictSuperseded marks evidence drawn from a version that a newer
	// version has replaced.
	ConflictSuperseded ConflictKind = "superseded_version"
)

// GapKind classifies what the answer could not establish.
type GapKind string

const (
	// GapUnavailable marks a requested reference the caller cannot see. It is
	// returned for a document that does not exist and for one the caller may
	// not read alike, so the answer is never an existence oracle.
	GapUnavailable GapKind = "unavailable"
	// GapNoMatch marks a reference that resolved but matched no passage.
	GapNoMatch GapKind = "no_match"
	// GapNoEvidence marks a question no passage could support at all.
	GapNoEvidence GapKind = "no_evidence"
	// GapUnsupportedClaim marks a claim that no validated evidence survived for.
	GapUnsupportedClaim GapKind = "unsupported_claim"
)

// ResolutionUnresolved is the only resolution this package assigns to a
// conflict: it reports disagreements instead of silently picking a winner.
const ResolutionUnresolved = "unresolved: human review required"

// Passage is one retrieved slice of a document, already filtered by permission.
// Start is the rune offset of Text inside the document body.
type Passage struct {
	DocumentID string `json:"document_id"`
	Version    int    `json:"document_version"`
	Title      string `json:"title"`
	Owner      string `json:"owner"`
	Text       string `json:"text"`
	Start      int    `json:"start"`
}

// Ref returns the "<document id>@<version>" reference of the passage.
func (p Passage) Ref() string {
	return fmt.Sprintf("%s@%d", p.DocumentID, p.Version)
}

// ResearchRequest is the frozen input contract.
type ResearchRequest struct {
	Question string `json:"question"`
	// DocumentRefs restricts the research to these documents, as "<id>" or
	// "<id>@<version>". Empty means every document the principal may read.
	DocumentRefs []string `json:"document_refs,omitempty"`
}

// Evidence is one quote that backs a claim, tied to the exact document version
// it was read from. Start and End are rune offsets into that version's body.
type Evidence struct {
	Ref      string   `json:"ref"`
	Quote    string   `json:"quote"`
	Start    int      `json:"start"`
	End      int      `json:"end"`
	Polarity Polarity `json:"polarity"`
}

// Claim is one statement of the answer. A claim with no validated evidence is
// still reported, with Supported false and the reason it could not be
// supported.
type Claim struct {
	ID         string     `json:"id"`
	Statement  string     `json:"statement"`
	Supported  bool       `json:"supported"`
	Confidence Confidence `json:"confidence"`
	Evidence   []Evidence `json:"evidence,omitempty"`
	Reason     string     `json:"reason,omitempty"`
}

// EvidenceRow is one claim/evidence pair flattened for the evidence table, with
// the ownership fields a reader needs to judge the source.
type EvidenceRow struct {
	ClaimID  string   `json:"claim_id"`
	Ref      string   `json:"ref"`
	Title    string   `json:"title"`
	Owner    string   `json:"owner"`
	Tenant   string   `json:"tenant"`
	Quote    string   `json:"quote"`
	Start    int      `json:"start"`
	End      int      `json:"end"`
	Polarity Polarity `json:"polarity"`
	// Stale marks evidence read from a version a newer version replaced.
	Stale bool `json:"stale"`
}

// Conflict is a disagreement the answer refuses to resolve by itself.
type Conflict struct {
	Kind       ConflictKind `json:"kind"`
	Statement  string       `json:"statement"`
	Refs       []string     `json:"refs"`
	Resolution string       `json:"resolution"`
}

// Gap is something the answer could not establish, with the reason why.
type Gap struct {
	Kind   GapKind `json:"kind"`
	Ref    string  `json:"ref,omitempty"`
	Detail string  `json:"detail"`
}

// ResearchAnswer is the frozen output contract.
type ResearchAnswer struct {
	Question      string        `json:"question"`
	Claims        []Claim       `json:"claims"`
	EvidenceTable []EvidenceRow `json:"evidence_table"`
	Conflicts     []Conflict    `json:"conflicts"`
	Gaps          []Gap         `json:"gaps"`
	Confidence    Confidence    `json:"confidence"`
	// Advisory is always true: the answer informs a human decision, it never
	// makes one.
	Advisory bool `json:"advisory"`
	// Warnings records what was discarded while building the answer, so a
	// rejected proposal is visible instead of invisible.
	Warnings []string `json:"warnings,omitempty"`
}

// Candidate is one quote-level proposal from an extractor. It is not evidence
// until the engine has matched it against a document it retrieved.
type Candidate struct {
	Ref      string   `json:"ref"`
	Quote    string   `json:"quote"`
	Polarity Polarity `json:"polarity"`
}

// Finding is one claim proposal from a synthesizer, referring to the candidates
// it rests on by index.
type Finding struct {
	Statement        string     `json:"statement"`
	CandidateIndexes []int      `json:"candidate_indexes"`
	Confidence       Confidence `json:"confidence"`
}

// Extractor proposes quoted candidates for a question from retrieved passages.
type Extractor interface {
	Extract(ctx context.Context, question string, passages []Passage) ([]Candidate, error)
}

// Synthesizer proposes claims from the candidates that survived validation.
type Synthesizer interface {
	Synthesize(ctx context.Context, question string, candidates []Candidate) ([]Finding, error)
}
