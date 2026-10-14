package research

import (
	"context"
	"strings"
	"testing"
)

// scriptedExtractor returns fixed candidates, so a test can decide exactly what
// a model would have proposed.
type scriptedExtractor struct {
	candidates []Candidate
	called     int
	passages   []Passage
}

func (s *scriptedExtractor) Extract(_ context.Context, _ string, passages []Passage) ([]Candidate, error) {
	s.called++
	s.passages = passages
	return s.candidates, nil
}

// scriptedSynthesizer returns fixed findings and records the candidates that
// actually reached it.
type scriptedSynthesizer struct {
	findings []Finding
	got      []Candidate
	called   int
}

func (s *scriptedSynthesizer) Synthesize(_ context.Context, _ string, candidates []Candidate) ([]Finding, error) {
	s.called++
	s.got = candidates
	return s.findings, nil
}

// recklessSource has no permission logic at all: it hands back every document
// and every matching block, which is exactly the adapter bug the engine has to
// survive.
type recklessSource struct{ docs []Document }

func (r recklessSource) List(context.Context, Principal) ([]DocumentMeta, error) {
	metas := make([]DocumentMeta, 0, len(r.docs))
	for _, doc := range r.docs {
		metas = append(metas, doc.Meta())
	}
	return metas, nil
}

func (r recklessSource) Get(_ context.Context, _ Principal, id string, version int) (Document, error) {
	for _, doc := range r.docs {
		if doc.ID == id && (version == 0 || doc.Version == version) {
			return doc, nil
		}
	}
	return Document{}, ErrUnavailable
}

func (r recklessSource) Search(_ context.Context, _ Principal, query string, limit int) ([]Passage, error) {
	queryTokens := uniqueTokens(tokenize(query))
	var out []Passage
	for _, doc := range r.docs {
		for _, block := range splitBlocks(doc.Text) {
			block.DocumentID = doc.ID
			block.Version = doc.Version
			block.Title = doc.Title
			block.Owner = doc.Owner
			if score(queryTokens, block.Text) > 0 {
				out = append(out, block)
			}
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

const question = "Did the pilot reduce handling time?"

func engineWith(t *testing.T, src DocumentSource, candidates []Candidate, findings []Finding) (*Engine, *scriptedExtractor, *scriptedSynthesizer) {
	t.Helper()
	ex := &scriptedExtractor{candidates: candidates}
	sy := &scriptedSynthesizer{findings: findings}
	return New(src, ex, sy), ex, sy
}

func TestResearchRejectsAnEmptyQuestion(t *testing.T) {
	engine, _, _ := engineWith(t, testCorpus(t), nil, nil)
	if _, err := engine.Research(context.Background(), bob, ResearchRequest{}); err == nil {
		t.Fatal("Research accepted an empty question")
	}
}

func TestEvidenceMustQuoteTheCitedVersionVerbatim(t *testing.T) {
	src := testCorpus(t)
	candidates := []Candidate{
		{Ref: "pilot-notes@2", Quote: "The pilot reduced handling time by a third", Polarity: PolaritySupports},
		{Ref: "pilot-notes@2", Quote: "the pilot cut handling time", Polarity: PolaritySupports},
		{Ref: "ghost-doc@1", Quote: "The pilot reduced handling time by a third", Polarity: PolaritySupports},
		{Ref: "audit-2026@1", Quote: "The audit found no evidence that handling time fell.", Polarity: PolarityContradicts},
		{Ref: "ops-handbook@1", Quote: "Handling time is measured from the moment a case is opened.", Polarity: Polarity("maybe")},
		{Ref: "ops-handbook@1", Quote: "   ", Polarity: PolaritySupports},
	}
	findings := []Finding{{
		Statement:        "The pilot reduced handling time by a third.",
		CandidateIndexes: []int{0, 1, 2, 3, 4, 5},
		Confidence:       ConfidenceHigh,
	}}
	engine, _, synthesizer := engineWith(t, src, candidates, findings)

	answer, err := engine.Research(context.Background(), bob, ResearchRequest{Question: question})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(answer.Claims) != 1 {
		t.Fatalf("Research returned %d claims, want 1: %+v", len(answer.Claims), answer.Claims)
	}
	claim := answer.Claims[0]
	if !claim.Supported {
		t.Errorf("the claim with a verbatim quote is not supported: %+v", claim)
	}
	if len(claim.Evidence) != 1 {
		t.Fatalf("claim has %d evidence entries, want only the verbatim one: %+v", len(claim.Evidence), claim.Evidence)
	}
	if claim.Evidence[0].Ref != "pilot-notes@2" {
		t.Errorf("evidence ref = %q, want pilot-notes@2", claim.Evidence[0].Ref)
	}

	// The offsets address the quote inside the cited version's body.
	body, err := src.Get(context.Background(), bob, "pilot-notes", 2)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	runes := []rune(body.Text)
	evidence := claim.Evidence[0]
	if evidence.Start < 0 || evidence.End > len(runes) || evidence.Start >= evidence.End {
		t.Fatalf("evidence offsets %d..%d are outside a body of %d runes", evidence.Start, evidence.End, len(runes))
	}
	if got := string(runes[evidence.Start:evidence.End]); got != evidence.Quote {
		t.Errorf("offsets %d..%d address %q, want %q", evidence.Start, evidence.End, got, evidence.Quote)
	}

	// A candidate that never became evidence never reaches the synthesizer
	// either: the model only ever sees validated material.
	if len(synthesizer.got) != 1 {
		t.Errorf("the synthesizer received %d candidates, want 1: %+v", len(synthesizer.got), synthesizer.got)
	}

	// Every rejection is visible.
	if len(answer.Warnings) != 5 {
		t.Errorf("Research reported %d warnings, want 5: %v", len(answer.Warnings), answer.Warnings)
	}
	for _, want := range []string{
		"does not appear in pilot-notes@2",
		"is not available",
		"invalid polarity",
	} {
		found := false
		for _, warning := range answer.Warnings {
			if strings.Contains(warning, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no warning mentions %q: %v", want, answer.Warnings)
		}
	}
	if strings.Contains(strings.Join(answer.Warnings, " "), "The audit found no evidence that handling time fell.") {
		t.Errorf("a warning quotes a document bob may not read: %v", answer.Warnings)
	}
}

func TestQuoteOffsetsAreRuneExact(t *testing.T) {
	src, err := NewMemorySource([]Document{{
		ID: "unicode", Title: "Unicode", Owner: "ana", Tenant: "acme", Version: 1,
		Text: "Ceñía el café ☕ and handling time fell by a third.",
	}})
	if err != nil {
		t.Fatalf("NewMemorySource: %v", err)
	}
	quote := "handling time fell by a third"
	engine, _, _ := engineWith(t, src, []Candidate{
		{Ref: "unicode@1", Quote: quote, Polarity: PolaritySupports},
	}, []Finding{{
		Statement:        "Handling time fell.",
		CandidateIndexes: []int{0},
		Confidence:       ConfidenceMedium,
	}})

	answer, err := engine.Research(context.Background(), ana, ResearchRequest{Question: question})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(answer.Claims) != 1 || len(answer.Claims[0].Evidence) != 1 {
		t.Fatalf("Research returned %+v, want one claim with one evidence entry", answer.Claims)
	}
	evidence := answer.Claims[0].Evidence[0]
	body := "Ceñía el café ☕ and handling time fell by a third."
	runes := []rune(body)
	if got := string(runes[evidence.Start:evidence.End]); got != quote {
		t.Errorf("offsets %d..%d address %q, want %q", evidence.Start, evidence.End, got, quote)
	}
}

func TestClaimWithoutValidatedEvidenceIsReportedNotDropped(t *testing.T) {
	engine, _, _ := engineWith(t, testCorpus(t), nil, []Finding{
		{Statement: "Handling time fell by a third.", CandidateIndexes: []int{7}, Confidence: ConfidenceHigh},
		{Statement: "Agents want more training.", Confidence: ConfidenceMedium},
	})

	answer, err := engine.Research(context.Background(), bob, ResearchRequest{Question: question})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(answer.Claims) != 2 {
		t.Fatalf("Research returned %d claims, want the two unsupported proposals: %+v", len(answer.Claims), answer.Claims)
	}
	for _, claim := range answer.Claims {
		if claim.Supported {
			t.Errorf("a claim with no validated evidence is supported: %+v", claim)
		}
		if claim.Confidence != ConfidenceNone {
			t.Errorf("an unsupported claim carries confidence %q", claim.Confidence)
		}
		if claim.Reason == "" {
			t.Errorf("an unsupported claim has no reason: %+v", claim)
		}
	}
	if answer.Confidence != ConfidenceNone {
		t.Errorf("an answer whose claims are all unsupported reports %q", answer.Confidence)
	}
}

func TestNoRetrievedPassageMeansNoModelCall(t *testing.T) {
	engine, extractor, synthesizer := engineWith(t, testCorpus(t), []Candidate{
		{Ref: "pilot-notes@2", Quote: "The pilot reduced handling time by a third", Polarity: PolaritySupports},
	}, []Finding{{Statement: "anything", CandidateIndexes: []int{0}, Confidence: ConfidenceHigh}})

	answer, err := engine.Research(context.Background(), bob, ResearchRequest{Question: "photosynthesis in kelp"})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if extractor.called != 0 || synthesizer.called != 0 {
		t.Errorf("the model was consulted without evidence: extractor %d, synthesizer %d", extractor.called, synthesizer.called)
	}
	if len(answer.Claims) != 0 {
		t.Errorf("Research invented claims with no evidence: %+v", answer.Claims)
	}
	if answer.Confidence != ConfidenceNone {
		t.Errorf("an answer with no evidence reports confidence %q", answer.Confidence)
	}
	if !answer.Advisory {
		t.Error("the answer is not marked advisory")
	}
}

func TestEngineRechecksPermissionsOnARecklessSource(t *testing.T) {
	engine, _, _ := engineWith(t, recklessSource{docs: testDocuments()}, []Candidate{
		{Ref: "globex-plan@1", Quote: "Handling time is not measured at Globex yet.", Polarity: PolaritySupports},
	}, []Finding{{
		Statement:        "Handling time is not measured at Globex.",
		CandidateIndexes: []int{0},
		Confidence:       ConfidenceHigh,
	}})

	answer, err := engine.Research(context.Background(), bob, ResearchRequest{Question: question})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	for _, row := range answer.EvidenceTable {
		if row.Tenant != "acme" {
			t.Errorf("the evidence table carries a row from tenant %q: %+v", row.Tenant, row)
		}
	}
	for _, claim := range answer.Claims {
		for _, evidence := range claim.Evidence {
			if strings.HasPrefix(evidence.Ref, "globex-plan") || strings.HasPrefix(evidence.Ref, "audit-2026") {
				t.Errorf("a claim cites %s for bob: %+v", evidence.Ref, claim)
			}
		}
	}
	if len(answer.Warnings) == 0 {
		t.Error("the engine silently ignored what the reckless source returned")
	}
}

func TestResearchRestrictsToTheRequestedDocuments(t *testing.T) {
	engine, extractor, _ := engineWith(t, testCorpus(t), nil, nil)
	answer, err := engine.Research(context.Background(), bob, ResearchRequest{
		Question:     question,
		DocumentRefs: []string{"ops-handbook"},
	})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if !answer.Advisory {
		t.Error("the answer is not marked advisory")
	}
	for _, passage := range extractor.passages {
		if passage.DocumentID != "ops-handbook" {
			t.Errorf("a passage from %s was retrieved outside the requested scope", passage.DocumentID)
		}
	}
}
