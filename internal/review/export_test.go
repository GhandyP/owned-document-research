package review

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GhandyP/owned-document-research/internal/research"
)

func answerFixture() research.ResearchAnswer {
	return research.ResearchAnswer{
		Question: "Did the intake pilot reduce handling time?",
		Claims: []research.Claim{{
			ID:         "c1",
			Statement:  "The intake pilot reduced handling time by a third across the first quarter.",
			Supported:  true,
			Confidence: research.ConfidenceLow,
			Evidence: []research.Evidence{{
				Ref:      "pilot-notes@2",
				Quote:    "The intake pilot reduced handling time by a third across the first quarter.",
				Start:    0,
				End:      75,
				Polarity: research.PolaritySupports,
			}},
		}},
		EvidenceTable: []research.EvidenceRow{{
			ClaimID:  "c1",
			Ref:      "pilot-notes@2",
			Title:    "Intake pilot notes",
			Owner:    "ana@acme.test",
			Tenant:   "drive-acme",
			Quote:    "The intake pilot reduced handling time by a third across the first quarter.",
			Start:    0,
			End:      75,
			Polarity: research.PolaritySupports,
		}},
		Confidence: research.ConfidenceLow,
		Advisory:   true,
	}
}

func decisionFixture() Decision {
	return Approved(ActionExport, "ana@acme.test", time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC))
}

func TestAuthorizesOnlyItsOwnActionAndOnlyWhenApproved(t *testing.T) {
	approved := decisionFixture()
	if !approved.Authorizes(ActionExport) {
		t.Error("an approval does not authorize its own action")
	}
	if approved.Authorizes(Action("send_email")) {
		t.Error("an approval authorizes an action it was not given for")
	}
	rejected := Rejected(ActionExport, "ana@acme.test", time.Now(), "not now")
	if rejected.Authorizes(ActionExport) {
		t.Error("a rejection authorizes its own action")
	}
	if (Decision{}).Authorizes(ActionExport) {
		t.Error("an absent decision authorizes the action")
	}
}

func TestExportRefusesEveryDecisionThatIsNotThisApproval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "answer.md")

	tests := []struct {
		name     string
		decision Decision
	}{
		{name: "no decision at all", decision: Decision{}},
		{name: "a rejection", decision: Rejected(ActionExport, "ana@acme.test", time.Now(), "later")},
		{name: "an approval for another action", decision: Approved(Action("send_email"), "ana@acme.test", time.Now())},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Export(path, answerFixture(), tt.decision)
			if err == nil {
				t.Fatal("Export wrote an answer without an approval")
			}
			if !errors.Is(err, ErrNotApproved) {
				t.Errorf("Export returned %v, want an error wrapping ErrNotApproved", err)
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("Export created %s anyway", path)
			}
			if _, statErr := os.Stat(path + ".json"); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("Export created a sidecar anyway")
			}
		})
	}
}

func TestExportWritesTheAnswerAndItsReviewRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "answer.md")

	if err := Export(path, answerFixture(), decisionFixture()); err != nil {
		t.Fatalf("Export: %v", err)
	}

	markdown, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the export: %v", err)
	}
	text := string(markdown)
	for _, want := range []string{
		"Did the intake pilot reduce handling time?",
		"The intake pilot reduced handling time by a third across the first quarter.",
		"pilot-notes@2",
		"ana@acme.test",
		"low",
		"advisory",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the export does not mention %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "2026-04-03") {
		t.Errorf("the export does not record when the answer was approved:\n%s", text)
	}

	sidecar, err := os.ReadFile(path + ".json")
	if err != nil {
		t.Fatalf("read the sidecar: %v", err)
	}
	var decoded research.ResearchAnswer
	if err := json.Unmarshal(sidecar, &decoded); err != nil {
		t.Fatalf("the sidecar is not the answer: %v", err)
	}
	if decoded.Question != answerFixture().Question || len(decoded.Claims) != 1 {
		t.Errorf("the sidecar does not carry the answer: %+v", decoded)
	}
	if !strings.Contains(string(sidecar), "review") {
		t.Errorf("the sidecar does not carry the review record: %s", sidecar)
	}
}

func TestExportReportsAnUnwritableTargetWithoutLeavingHalfAnExport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "answer.md")

	err := Export(path, answerFixture(), decisionFixture())
	if err == nil {
		t.Fatal("Export wrote into a directory that does not exist")
	}
	if errors.Is(err, ErrNotApproved) {
		t.Errorf("Export blamed the approval for a write failure: %v", err)
	}
	if _, statErr := os.Stat(path + ".json"); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("Export left a half-written sidecar behind")
	}
}

func TestDenyAllRefusesEverything(t *testing.T) {
	gate := DenyAll{}
	decision, err := gate.Review(context.Background(), Request{Action: ActionExport, Summary: "export"})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if decision.Authorizes(ActionExport) {
		t.Error("the closed gate approved an action")
	}
}

func TestTerminalReadsAnExplicitYesAndRefusesAnythingElse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantOK  bool
		summary string
	}{
		{name: "yes", input: "yes\nana@acme.test\n", wantOK: true},
		{name: "an empty answer", input: "\n", wantOK: false},
		{name: "anything that is not yes", input: "sure\n", wantOK: false},
		{name: "a refusal", input: "no\n", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &strings.Builder{}
			gate := NewTerminal(strings.NewReader(tt.input), out, "ana@acme.test")
			decision, err := gate.Review(context.Background(), Request{
				Action:  ActionExport,
				Summary: "export the answer for \"did the pilot reduce handling time\"",
				Details: []string{"1 claim, 1 quote"},
			})
			if err != nil {
				t.Fatalf("Review: %v", err)
			}
			if got := decision.Authorizes(ActionExport); got != tt.wantOK {
				t.Errorf("Authorizes = %v, want %v", got, tt.wantOK)
			}
			prompt := out.String()
			if !strings.Contains(prompt, "export the answer") {
				t.Errorf("the question was not shown to the reviewer: %q", prompt)
			}
			if !strings.Contains(prompt, "1 claim, 1 quote") {
				t.Errorf("the details were not shown to the reviewer: %q", prompt)
			}
			if !strings.Contains(strings.ToLower(prompt), "yes") {
				t.Errorf("the reviewer was not told how to approve: %q", prompt)
			}
		})
	}
}
