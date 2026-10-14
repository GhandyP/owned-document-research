package research

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const (
	quoteCurrent = "The pilot reduced handling time by a third"
	quoteStale   = "The pilot did not change handling time"
	quoteAudit   = "The audit found no evidence that handling time fell."
)

func TestConflictingEvidenceIsReportedAndNeverResolved(t *testing.T) {
	candidates := []Candidate{
		{Ref: "pilot-notes@2", Quote: quoteCurrent, Polarity: PolaritySupports},
		{Ref: "pilot-notes@1", Quote: quoteStale, Polarity: PolarityContradicts},
		{Ref: "ops-handbook@1", Quote: "Handling time is measured from the moment a case is opened.", Polarity: PolaritySupports},
	}
	findings := []Finding{{
		Statement:        "The pilot reduced handling time.",
		CandidateIndexes: []int{0, 1, 2},
		Confidence:       ConfidenceHigh,
	}}
	engine, _, _ := engineWith(t, testCorpus(t), candidates, findings)

	answer, err := engine.Research(context.Background(), bob, ResearchRequest{Question: question})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(answer.Claims) != 1 {
		t.Fatalf("Research returned %d claims, want 1: %+v", len(answer.Claims), answer.Claims)
	}
	claim := answer.Claims[0]
	if !claim.Supported {
		t.Errorf("a claim with one supporting quote is unsupported: %+v", claim)
	}
	if claim.Confidence != ConfidenceLow {
		t.Errorf("a claim whose evidence argues both ways carries %q, want low", claim.Confidence)
	}
	if len(claim.Evidence) != 3 {
		t.Errorf("claim has %d evidence entries, want 3: %+v", len(claim.Evidence), claim.Evidence)
	}

	kinds := make(map[ConflictKind][]Conflict)
	for _, conflict := range answer.Conflicts {
		if conflict.Resolution != ResolutionUnresolved {
			t.Errorf("conflict resolved itself: %+v", conflict)
		}
		kinds[conflict.Kind] = append(kinds[conflict.Kind], conflict)
	}
	if len(kinds[ConflictPolarity]) != 1 {
		t.Errorf("Research reported %d polarity conflicts, want 1: %+v", len(kinds[ConflictPolarity]), answer.Conflicts)
	}
	if len(kinds[ConflictSuperseded]) != 1 {
		t.Errorf("Research reported %d superseded-version conflicts, want 1: %+v", len(kinds[ConflictSuperseded]), answer.Conflicts)
	}

	polarity := kinds[ConflictPolarity][0]
	if polarity.Statement != "The pilot reduced handling time." {
		t.Errorf("polarity conflict statement = %q", polarity.Statement)
	}
	refs := strings.Join(polarity.Refs, ",")
	for _, want := range []string{"ops-handbook@1", "pilot-notes@1", "pilot-notes@2"} {
		if !strings.Contains(refs, want) {
			t.Errorf("polarity conflict refs %q do not mention %q", refs, want)
		}
	}

	stale := kinds[ConflictSuperseded][0]
	staleRefs := strings.Join(stale.Refs, ",")
	if !strings.Contains(staleRefs, "pilot-notes@1") || !strings.Contains(staleRefs, "pilot-notes@2") {
		t.Errorf("superseded conflict must name the replaced version and its replacement, got %q", staleRefs)
	}

	if answer.Confidence != ConfidenceLow {
		t.Errorf("an answer with an unresolved conflict reports %q, want low", answer.Confidence)
	}

	staleRows := 0
	for _, row := range answer.EvidenceTable {
		if row.Ref == "pilot-notes@1" {
			staleRows++
			if !row.Stale {
				t.Errorf("the row for the superseded version is not marked stale: %+v", row)
			}
		}
	}
	if staleRows != 1 {
		t.Errorf("evidence table has %d rows for pilot-notes@1, want 1", staleRows)
	}
}

func TestASupersededVersionCannotSupportALiveClaim(t *testing.T) {
	engine, _, _ := engineWith(t, testCorpus(t), []Candidate{
		{Ref: "pilot-notes@1", Quote: quoteStale, Polarity: PolaritySupports},
	}, []Finding{{
		Statement:        "The pilot left handling time unchanged.",
		CandidateIndexes: []int{0},
		Confidence:       ConfidenceHigh,
	}})

	answer, err := engine.Research(context.Background(), bob, ResearchRequest{Question: question})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(answer.Claims) != 1 {
		t.Fatalf("Research returned %d claims, want 1: %+v", len(answer.Claims), answer.Claims)
	}
	claim := answer.Claims[0]
	if claim.Supported {
		t.Errorf("a claim standing only on a superseded version is supported: %+v", claim)
	}
	if claim.Confidence != ConfidenceNone {
		t.Errorf("the claim carries confidence %q, want none", claim.Confidence)
	}
	if !strings.Contains(claim.Reason, "superseded") {
		t.Errorf("the claim reason does not explain the superseded source: %q", claim.Reason)
	}
	if len(claim.Evidence) != 1 {
		t.Errorf("the stale quote is still shown as evidence: %+v", claim.Evidence)
	}
	if answer.Confidence != ConfidenceNone {
		t.Errorf("answer confidence = %q, want none", answer.Confidence)
	}

	found := false
	for _, conflict := range answer.Conflicts {
		if conflict.Kind == ConflictSuperseded {
			found = true
		}
	}
	if !found {
		t.Errorf("no superseded-version conflict was reported: %+v", answer.Conflicts)
	}
	found = false
	for _, gap := range answer.Gaps {
		if gap.Kind == GapUnsupportedClaim {
			found = true
		}
	}
	if !found {
		t.Errorf("an unsupported claim produced no gap: %+v", answer.Gaps)
	}
}

func TestGapsNameUnavailableAndIrrelevantDocuments(t *testing.T) {
	engine, _, _ := engineWith(t, testCorpus(t), []Candidate{
		{Ref: "ops-handbook@1", Quote: "Handling time is measured from the moment a case is opened.", Polarity: PolaritySupports},
	}, []Finding{{
		Statement:        "Handling time is measured from the moment a case is opened.",
		CandidateIndexes: []int{0},
		Confidence:       ConfidenceMedium,
	}})

	answer, err := engine.Research(context.Background(), bob, ResearchRequest{
		Question:     question,
		DocumentRefs: []string{"ops-handbook", "ghost-doc", "audit-2026", "empty-stub", "!!"},
	})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}

	byKind := make(map[GapKind][]Gap)
	for _, gap := range answer.Gaps {
		byKind[gap.Kind] = append(byKind[gap.Kind], gap)
	}
	if len(byKind[GapUnavailable]) != 3 {
		t.Errorf("want 3 unavailable gaps (absent, forbidden, malformed), got %+v", byKind[GapUnavailable])
	}
	details := make(map[string]bool)
	for _, gap := range byKind[GapUnavailable] {
		if gap.Ref == "!!" {
			continue
		}
		details[gap.Detail] = true
	}
	if len(details) != 1 {
		t.Errorf("an absent document and a forbidden one are reported differently: %+v", byKind[GapUnavailable])
	}
	if len(byKind[GapNoMatch]) != 1 {
		t.Errorf("want exactly 1 no-match gap for the empty stub, got %+v", byKind[GapNoMatch])
	}
	for _, gap := range byKind[GapNoMatch] {
		if !strings.Contains(gap.Ref, "empty-stub") {
			t.Errorf("the no-match gap names %q, want the empty stub", gap.Ref)
		}
	}
	if len(answer.Claims) != 1 || !answer.Claims[0].Supported {
		t.Errorf("the resolvable document produced no supported claim: %+v", answer.Claims)
	}
	if answer.Confidence != ConfidenceMedium {
		t.Errorf("answer confidence = %q, want medium", answer.Confidence)
	}
}

func TestNoEvidenceIsAnOutcomeNotAnEmptyAnswer(t *testing.T) {
	engine, _, _ := engineWith(t, testCorpus(t), nil, nil)
	answer, err := engine.Research(context.Background(), bob, ResearchRequest{Question: "photosynthesis in kelp"})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	kinds := make(map[GapKind]int)
	for _, gap := range answer.Gaps {
		kinds[gap.Kind]++
	}
	if kinds[GapNoEvidence] != 1 {
		t.Errorf("want one no-evidence gap, got %+v", answer.Gaps)
	}
	if answer.Confidence != ConfidenceNone {
		t.Errorf("answer confidence = %q, want none", answer.Confidence)
	}
	if !answer.Advisory {
		t.Error("the answer is not advisory")
	}
}

func TestAForbiddenReferenceAnswersExactlyLikeAnAbsentOne(t *testing.T) {
	// bob is in acme/ops: audit-2026 exists but is restricted to acme/legal.
	forbidden, err := engineWith(t, testCorpus(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	absent, err := engineWith(t, testCorpus(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	forbiddenAnswer, err := forbidden.Research(context.Background(), bob, ResearchRequest{
		Question:     question,
		DocumentRefs: []string{"audit-2026"},
	})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	absentAnswer, err := absent.Research(context.Background(), bob, ResearchRequest{
		Question:     question,
		DocumentRefs: []string{"audit-2026-does-not-exist"},
	})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}

	forbiddenJSON, err := json.Marshal(forbiddenAnswer)
	if err != nil {
		t.Fatal(err)
	}
	absentJSON, err := json.Marshal(absentAnswer)
	if err != nil {
		t.Fatal(err)
	}
	normalised := strings.ReplaceAll(string(forbiddenJSON), "audit-2026", "REF")
	normalisedAbsent := strings.ReplaceAll(string(absentJSON), "audit-2026-does-not-exist", "REF")
	if normalised != normalisedAbsent {
		t.Errorf("a forbidden reference is distinguishable from an absent one:\n%s\n%s", normalised, normalisedAbsent)
	}
}
