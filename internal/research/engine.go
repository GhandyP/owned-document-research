package research

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const (
	// maxPassagesPerDocument bounds how much of one document a question may
	// pull into a single research run.
	maxPassagesPerDocument = 2
	// maxWarnings bounds the discarded-proposal list, so a runaway model cannot
	// inflate an answer with its own noise.
	maxWarnings = 20
)

// NoopExtractor proposes nothing, which is how research runs without a model.
type NoopExtractor struct{}

// Extract implements Extractor.
func (NoopExtractor) Extract(context.Context, string, []Passage) ([]Candidate, error) {
	return nil, nil
}

// NoopSynthesizer proposes nothing, which is how research runs without a model.
type NoopSynthesizer struct{}

// Synthesize implements Synthesizer.
func (NoopSynthesizer) Synthesize(context.Context, string, []Candidate) ([]Finding, error) {
	return nil, nil
}

// Engine answers research questions from a document source.
//
// The engine owns every fact in an answer. The extractor and the synthesizer
// only propose: a proposal becomes evidence only after it has been matched
// against the bytes of a document version the principal may read, and it
// becomes a claim only if the synthesizer wires it to that validated evidence.
type Engine struct {
	source      DocumentSource
	extractor   Extractor
	synthesizer Synthesizer
}

// New builds an engine. A nil extractor or synthesizer proposes nothing, so
// research runs deterministically without a model.
func New(source DocumentSource, extractor Extractor, synthesizer Synthesizer) *Engine {
	if extractor == nil {
		extractor = NoopExtractor{}
	}
	if synthesizer == nil {
		synthesizer = NoopSynthesizer{}
	}
	return &Engine{source: source, extractor: extractor, synthesizer: synthesizer}
}

// Search returns the passages of the query the principal may read, with the
// same permission checks a research run applies.
func (e *Engine) Search(ctx context.Context, p Principal, query string, limit int) ([]Passage, error) {
	if e == nil || e.source == nil {
		return nil, fmt.Errorf("research: a document source is required")
	}
	scoped, err := e.scope(ctx, p)
	if err != nil {
		return nil, err
	}
	passages, _, err := e.passagesFor(ctx, p, query, scoped, limit)
	return passages, err
}

// Research answers req as p.
func (e *Engine) Research(ctx context.Context, p Principal, req ResearchRequest) (ResearchAnswer, error) {
	question := strings.TrimSpace(req.Question)
	if question == "" {
		return ResearchAnswer{}, fmt.Errorf("research: a question is required")
	}
	if e == nil || e.source == nil {
		return ResearchAnswer{}, fmt.Errorf("research: a document source is required")
	}

	scoped, err := e.scope(ctx, p)
	if err != nil {
		return ResearchAnswer{}, err
	}
	inScope, gaps := resolveScope(scoped, req.DocumentRefs)

	answer := ResearchAnswer{
		Question:   question,
		Gaps:       gaps,
		Confidence: ConfidenceNone,
		Advisory:   true,
	}

	passages, warnings, err := e.passagesFor(ctx, p, question, inScope, 0)
	if err != nil {
		return ResearchAnswer{}, err
	}
	answer.Warnings = append(answer.Warnings, warnings...)

	if len(req.DocumentRefs) > 0 {
		answer.Gaps = append(answer.Gaps, noMatchGaps(inScope, passages, req.DocumentRefs)...)
	}
	if len(passages) == 0 {
		answer.Gaps = append(answer.Gaps, Gap{
			Kind:   GapNoEvidence,
			Detail: "no readable passage in scope matches the question",
		})
		return answer, nil
	}

	candidates, err := e.extractor.Extract(ctx, question, passages)
	if err != nil {
		return ResearchAnswer{}, fmt.Errorf("research: extraction: %w", err)
	}

	byRef := make(map[string]Passage, len(passages))
	for _, passage := range passages {
		if _, taken := byRef[passage.Ref()]; !taken {
			byRef[passage.Ref()] = passage
		}
	}
	metas := make(map[string]DocumentMeta, len(inScope))
	for _, meta := range inScope {
		metas[meta.Ref()] = meta
	}

	validated, discarded := e.validate(ctx, p, candidates, byRef, metas)
	answer.Warnings = append(answer.Warnings, discarded...)

	proposals := make([]Candidate, 0, len(validated))
	for _, item := range validated {
		proposals = append(proposals, item.candidate)
	}

	findings, err := e.synthesizer.Synthesize(ctx, question, proposals)
	if err != nil {
		return ResearchAnswer{}, fmt.Errorf("research: synthesis: %w", err)
	}

	claims, rows, claimWarnings := buildClaims(findings, validated, metas, supersededSet(scoped))
	answer.Claims = claims
	answer.EvidenceTable = rows
	answer.Warnings = append(answer.Warnings, claimWarnings...)
	answer.Gaps = append(answer.Gaps, unsupportedGaps(claims)...)
	answer.Conflicts = detectConflicts(claims, supersededSet(scoped), replacementRefs(scoped))
	answer.Confidence = answerConfidence(claims, answer.Conflicts)
	answer.Warnings = capWarnings(answer.Warnings)
	return answer, nil
}

// scope lists the documents the principal may read, re-checking the permission
// rule on everything the source returns instead of trusting the adapter.
func (e *Engine) scope(ctx context.Context, p Principal) ([]DocumentMeta, error) {
	metas, err := e.source.List(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("research: list documents: %w", err)
	}
	scoped := make([]DocumentMeta, 0, len(metas))
	for _, meta := range metas {
		if !meta.ReadableBy(p) {
			continue
		}
		scoped = append(scoped, meta)
	}
	sort.Slice(scoped, func(i, j int) bool {
		if scoped[i].ID != scoped[j].ID {
			return scoped[i].ID < scoped[j].ID
		}
		return scoped[i].Version < scoped[j].Version
	})
	return scoped, nil
}

// passagesFor searches, then keeps only what the scope allows. A passage the
// scope does not cover is dropped with a warning: an adapter that returns too
// much is a bug to be visible, not a leak to be silent about.
func (e *Engine) passagesFor(ctx context.Context, p Principal, query string, inScope []DocumentMeta, limit int) ([]Passage, []string, error) {
	allowed := make(map[string]DocumentMeta, len(inScope))
	for _, meta := range inScope {
		allowed[meta.Ref()] = meta
	}
	found, err := e.source.Search(ctx, p, query, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("research: search: %w", err)
	}
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	var passages []Passage
	var warnings []string
	perDocument := make(map[string]int)
	for _, passage := range found {
		if len(passages) >= limit {
			break
		}
		meta, covered := allowed[passage.Ref()]
		if !covered || !meta.ReadableBy(p) {
			warnings = append(warnings, fmt.Sprintf("dropped a passage for a reference that is not available: %s", passage.Ref()))
			continue
		}
		if strings.TrimSpace(passage.Text) == "" {
			continue
		}
		if perDocument[passage.DocumentID] >= maxPassagesPerDocument {
			continue
		}
		perDocument[passage.DocumentID]++
		passages = append(passages, passage)
	}
	return passages, warnings, nil
}
