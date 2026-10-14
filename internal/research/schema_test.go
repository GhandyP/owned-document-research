package research

import (
	"encoding/json"
	"testing"
)

func TestConfidenceWeakerKeepsTheLowest(t *testing.T) {
	order := []Confidence{ConfidenceHigh, ConfidenceMedium, ConfidenceLow, ConfidenceNone}
	for i, stronger := range order {
		for j, weaker := range order[i:] {
			if got := Weaker(stronger, weaker); got != weaker {
				t.Errorf("Weaker(%q, %q) = %q, want %q", stronger, weaker, got, weaker)
			}
			if i != j && Weaker(weaker, stronger) != weaker {
				t.Errorf("Weaker is not symmetric for %q and %q", stronger, weaker)
			}
		}
	}
}

func TestConfidenceAndPolarityValidation(t *testing.T) {
	for _, c := range []Confidence{ConfidenceHigh, ConfidenceMedium, ConfidenceLow, ConfidenceNone} {
		if !c.Valid() {
			t.Errorf("Confidence(%q).Valid() = false, want true", c)
		}
	}
	for _, c := range []Confidence{"", "HIGH", "certain", "unknown"} {
		if c.Valid() {
			t.Errorf("Confidence(%q).Valid() = true, want false", c)
		}
	}
	for _, p := range []Polarity{PolaritySupports, PolarityContradicts} {
		if !p.Valid() {
			t.Errorf("Polarity(%q).Valid() = false, want true", p)
		}
	}
	for _, p := range []Polarity{"", "supports-something", "neutral"} {
		if p.Valid() {
			t.Errorf("Polarity(%q).Valid() = true, want false", p)
		}
	}
}

func TestResearchRequestJSONContract(t *testing.T) {
	var req ResearchRequest
	raw := `{"question":"Does the pilot reduce handling time?","document_refs":["ops-handbook","pilot-notes@2"]}`
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal ResearchRequest: %v", err)
	}
	if req.Question != "Does the pilot reduce handling time?" {
		t.Errorf("Question = %q", req.Question)
	}
	want := []string{"ops-handbook", "pilot-notes@2"}
	if len(req.DocumentRefs) != len(want) {
		t.Fatalf("DocumentRefs = %v, want %v", req.DocumentRefs, want)
	}
	for i := range want {
		if req.DocumentRefs[i] != want[i] {
			t.Errorf("DocumentRefs[%d] = %q, want %q", i, req.DocumentRefs[i], want[i])
		}
	}

	encoded, err := json.Marshal(ResearchRequest{Question: "q"})
	if err != nil {
		t.Fatalf("marshal ResearchRequest: %v", err)
	}
	if string(encoded) != `{"question":"q"}` {
		t.Errorf("marshalled request = %s, want an omitted document_refs member", encoded)
	}
}

func TestResearchAnswerJSONContract(t *testing.T) {
	answer := ResearchAnswer{
		Question: "Does the pilot reduce handling time?",
		Claims: []Claim{{
			ID:         "c1",
			Statement:  "The pilot reduces handling time.",
			Supported:  true,
			Confidence: ConfidenceMedium,
			Evidence: []Evidence{{
				Ref:      "pilot-notes@2",
				Quote:    "handling time fell by a third",
				Start:    12,
				End:      43,
				Polarity: PolaritySupports,
			}},
		}},
		EvidenceTable: []EvidenceRow{{
			ClaimID:  "c1",
			Ref:      "pilot-notes@2",
			Title:    "Pilot notes",
			Owner:    "user:ana",
			Tenant:   "acme",
			Quote:    "handling time fell by a third",
			Start:    12,
			End:      43,
			Polarity: PolaritySupports,
		}},
		Conflicts: []Conflict{{
			Kind:       ConflictPolarity,
			Statement:  "The pilot reduces handling time.",
			Refs:       []string{"pilot-notes@2", "audit-2026@1"},
			Resolution: "unresolved: human review required",
		}},
		Gaps: []Gap{{
			Kind:   GapNoEvidence,
			Detail: "no excerpt addresses cost",
		}},
		Confidence: ConfidenceMedium,
		Advisory:   true,
	}

	encoded, err := json.Marshal(answer)
	if err != nil {
		t.Fatalf("marshal ResearchAnswer: %v", err)
	}

	var members map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &members); err != nil {
		t.Fatalf("unmarshal ResearchAnswer: %v", err)
	}
	for _, name := range []string{
		"question", "claims", "evidence_table", "conflicts", "gaps", "confidence", "advisory",
	} {
		if _, ok := members[name]; !ok {
			t.Errorf("ResearchAnswer JSON has no %q member: %s", name, encoded)
		}
	}

	var decoded ResearchAnswer
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if len(decoded.Claims) != 1 || decoded.Claims[0].Evidence[0].Quote != "handling time fell by a third" {
		t.Errorf("claims did not round trip: %+v", decoded.Claims)
	}
	if !decoded.Claims[0].Supported || decoded.Claims[0].Confidence != ConfidenceMedium {
		t.Errorf("claim flags did not round trip: %+v", decoded.Claims[0])
	}
	if len(decoded.EvidenceTable) != 1 || decoded.EvidenceTable[0].Owner != "user:ana" {
		t.Errorf("evidence table did not round trip: %+v", decoded.EvidenceTable)
	}
	if len(decoded.Conflicts) != 1 || decoded.Conflicts[0].Resolution != "unresolved: human review required" {
		t.Errorf("conflicts did not round trip: %+v", decoded.Conflicts)
	}
	if len(decoded.Gaps) != 1 || decoded.Gaps[0].Kind != GapNoEvidence {
		t.Errorf("gaps did not round trip: %+v", decoded.Gaps)
	}
}
