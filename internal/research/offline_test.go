package research

import (
	"context"
	"strings"
	"testing"
)

func TestRuleExtractorQuotesSentencesAndReadsNegation(t *testing.T) {
	passages := []Passage{
		{
			DocumentID: "pilot-notes", Version: 2, Title: "Pilot notes", Owner: "ana",
			Text: "The pilot reduced handling time by a third across the first quarter.", Start: 0,
		},
		{
			DocumentID: "pilot-notes", Version: 1, Title: "Pilot notes", Owner: "ana",
			Text: "The pilot did not change handling time, according to the early read.", Start: 0,
		},
		{
			DocumentID: "canteen", Version: 1, Title: "Canteen", Owner: "ana",
			Text: "Lunch is served at noon.", Start: 0,
		},
	}
	candidates, err := RuleExtractor{}.Extract(context.Background(), question, passages)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("Extract returned %d candidates, want 2: %+v", len(candidates), candidates)
	}

	byRef := make(map[string]Candidate)
	for _, candidate := range candidates {
		if !strings.Contains(passages[0].Text+passages[1].Text, candidate.Quote) {
			t.Errorf("candidate quote is not copied from a passage: %+v", candidate)
		}
		if !candidate.Polarity.Valid() {
			t.Errorf("candidate polarity = %q", candidate.Polarity)
		}
		byRef[candidate.Ref] = candidate
	}
	if got := byRef["pilot-notes@2"]; got.Polarity != PolaritySupports {
		t.Errorf("the plain sentence was read as %q", got.Polarity)
	}
	if got := byRef["pilot-notes@1"]; got.Polarity != PolarityContradicts {
		t.Errorf("the negated sentence was read as %q", got.Polarity)
	}
	for _, candidate := range candidates {
		if candidate.Ref == "canteen@1" {
			t.Errorf("an irrelevant sentence produced a candidate: %+v", candidate)
		}
	}
}

func TestRuleSynthesizerStatesTheStrongestSupportingQuote(t *testing.T) {
	candidates := []Candidate{
		{Ref: "pilot-notes@2", Quote: quoteCurrent, Polarity: PolaritySupports},
		{Ref: "pilot-notes@1", Quote: quoteStale, Polarity: PolarityContradicts},
		{Ref: "ops-handbook@1", Quote: "Handling time is measured from the moment a case is opened.", Polarity: PolaritySupports},
	}
	findings, err := RuleSynthesizer{}.Synthesize(context.Background(), question, candidates)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("Synthesize returned %d findings, want 1: %+v", len(findings), findings)
	}
	finding := findings[0]
	if finding.Statement != quoteCurrent {
		t.Errorf("statement = %q, want the strongest supporting quote %q", finding.Statement, quoteCurrent)
	}
	if len(finding.CandidateIndexes) != 3 {
		t.Errorf("the finding cites %v, want every candidate", finding.CandidateIndexes)
	}
	if finding.Confidence != ConfidenceMedium {
		t.Errorf("the rule path reported confidence %q, want medium", finding.Confidence)
	}

	empty, err := RuleSynthesizer{}.Synthesize(context.Background(), question, nil)
	if err != nil {
		t.Fatalf("Synthesize with no candidates: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("Synthesize invented %d findings from no candidates", len(empty))
	}
}

func TestOfflineResearchAnswersFromTheCorpusWithoutAModel(t *testing.T) {
	engine := New(testCorpus(t), RuleExtractor{}, RuleSynthesizer{})

	answer, err := engine.Research(context.Background(), bob, ResearchRequest{Question: question})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(answer.Claims) != 1 {
		t.Fatalf("Research returned %d claims, want 1: %+v", len(answer.Claims), answer.Claims)
	}
	claim := answer.Claims[0]
	if !claim.Supported {
		t.Errorf("the offline answer has no supported claim: %+v", claim)
	}
	if len(claim.Evidence) < 2 {
		t.Errorf("the offline answer cites %d quotes, want several: %+v", len(claim.Evidence), claim.Evidence)
	}
	if len(answer.Conflicts) == 0 {
		t.Errorf("the offline answer hid the disagreement in the corpus: %+v", answer.Claims)
	}
	if answer.Confidence != ConfidenceLow {
		t.Errorf("an answer with conflicts reports %q, want low", answer.Confidence)
	}
	if len(answer.Warnings) != 0 {
		t.Errorf("the offline path discarded something: %v", answer.Warnings)
	}
	for _, row := range answer.EvidenceTable {
		if row.Tenant != "acme" {
			t.Errorf("the answer leaked a row from tenant %q", row.Tenant)
		}
	}
}
