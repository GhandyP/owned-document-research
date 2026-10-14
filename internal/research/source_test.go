package research

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// testCorpus is the shared fixture set for the deterministic half of the
// package. It deliberately contains two tenants, a superseded version, a
// document with no body, and an irrelevant document.
func testCorpus(t *testing.T) *MemorySource {
	t.Helper()
	src, err := NewMemorySource([]Document{
		{
			ID: "pilot-notes", Title: "Pilot notes", Owner: "ana", Tenant: "acme",
			Allow: []string{"group:ops", "user:carla"}, Version: 2, Supersedes: "pilot-notes@1",
			Text: "The pilot reduced handling time by a third across the first quarter.\n\n" +
				"Agents asked for more training on the new intake form.",
		},
		{
			ID: "pilot-notes", Title: "Pilot notes", Owner: "ana", Tenant: "acme",
			Allow: []string{"group:ops", "user:carla"}, Version: 1,
			Text: "The pilot did not change handling time, according to the early read.",
		},
		{
			ID: "ops-handbook", Title: "Operations handbook", Owner: "ana", Tenant: "acme",
			Allow: []string{"*"}, Version: 1,
			Text: "Handling time is measured from the moment a case is opened.",
		},
		{
			ID: "audit-2026", Title: "Audit 2026", Owner: "dan", Tenant: "acme",
			Allow: []string{"group:legal"}, Version: 1,
			Text: "The audit found no evidence that handling time fell.",
		},
		{
			ID: "globex-plan", Title: "Globex plan", Owner: "eve", Tenant: "globex",
			Allow: []string{"*"}, Version: 1,
			Text: "Handling time is not measured at Globex yet.",
		},
		{
			ID: "empty-stub", Title: "Empty stub", Owner: "ana", Tenant: "acme",
			Allow: []string{"group:ops"}, Version: 1, Text: "",
		},
	})
	if err != nil {
		t.Fatalf("NewMemorySource: %v", err)
	}
	return src
}

var (
	ana   = Principal{ID: "ana", Tenant: "acme"}
	bob   = Principal{ID: "bob", Tenant: "acme", Groups: []string{"ops"}}
	carla = Principal{ID: "carla", Tenant: "acme"}
	dan   = Principal{ID: "dan", Tenant: "acme", Groups: []string{"legal"}}
	eve   = Principal{ID: "eve", Tenant: "globex"}
)

func TestNewMemorySourceRejectsInvalidDocuments(t *testing.T) {
	if _, err := NewMemorySource([]Document{{ID: "", Tenant: "acme", Owner: "ana", Version: 1}}); err == nil {
		t.Fatal("NewMemorySource accepted a document with no id")
	}
	if _, err := NewMemorySource([]Document{{ID: "a", Tenant: "acme", Owner: "ana", Version: 0}}); err == nil {
		t.Fatal("NewMemorySource accepted a document with no version")
	}
}

func TestListReturnsOnlyReadableReferences(t *testing.T) {
	src := testCorpus(t)
	ctx := context.Background()

	tests := []struct {
		name      string
		principal Principal
		want      []string
	}{
		{
			name:      "the owner sees both versions of their own document",
			principal: ana,
			want:      []string{"empty-stub@1", "ops-handbook@1", "pilot-notes@1", "pilot-notes@2"},
		},
		{
			name:      "a group member sees the group documents",
			principal: bob,
			want:      []string{"empty-stub@1", "ops-handbook@1", "pilot-notes@1", "pilot-notes@2"},
		},
		{
			name:      "a named reader sees only what is named for them",
			principal: carla,
			want:      []string{"ops-handbook@1", "pilot-notes@1", "pilot-notes@2"},
		},
		{
			name:      "the legal group sees the audit",
			principal: dan,
			want:      []string{"audit-2026@1", "ops-handbook@1"},
		},
		{
			name:      "another tenant sees only its own documents",
			principal: eve,
			want:      []string{"globex-plan@1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := src.List(ctx, tt.principal)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var refs []string
			for _, meta := range got {
				refs = append(refs, meta.Ref())
			}
			if strings.Join(refs, ",") != strings.Join(tt.want, ",") {
				t.Errorf("List(%s) = %v, want %v", tt.principal.ID, refs, tt.want)
			}
		})
	}
}

func TestGetReportsMissingAndForbiddenAlike(t *testing.T) {
	src := testCorpus(t)
	ctx := context.Background()

	got, err := src.Get(ctx, ana, "pilot-notes", 2)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Text == "" {
		t.Error("Get returned a document without its body")
	}

	latest, err := src.Get(ctx, ana, "pilot-notes", 0)
	if err != nil {
		t.Fatalf("Get without a version: %v", err)
	}
	if latest.Version != 2 {
		t.Errorf("Get without a version returned version %d, want the latest (2)", latest.Version)
	}

	_, missingErr := src.Get(ctx, ana, "does-not-exist", 0)
	_, forbiddenErr := src.Get(ctx, bob, "audit-2026", 1)
	if missingErr == nil || forbiddenErr == nil {
		t.Fatalf("Get returned no error for a missing document (%v) or a forbidden one (%v)", missingErr, forbiddenErr)
	}
	if missingErr.Error() != forbiddenErr.Error() {
		t.Errorf("a forbidden document is distinguishable from a missing one: %q vs %q", forbiddenErr, missingErr)
	}
	if !errors.Is(forbiddenErr, ErrUnavailable) {
		t.Errorf("Get error = %v, want it to wrap ErrUnavailable", forbiddenErr)
	}
	if _, versionErr := src.Get(ctx, ana, "pilot-notes", 9); versionErr == nil {
		t.Error("Get accepted a version that does not exist")
	} else if versionErr.Error() != missingErr.Error() {
		t.Errorf("an absent version is distinguishable from an absent document: %q vs %q", versionErr, missingErr)
	}
}

func TestSearchRanksReadablePassagesAndNeverLeaksOthers(t *testing.T) {
	src := testCorpus(t)
	ctx := context.Background()

	passages, err := src.Search(ctx, bob, "handling time", 8)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(passages) == 0 {
		t.Fatal("Search returned no passages for a query the corpus answers")
	}
	for _, p := range passages {
		if p.DocumentID == "audit-2026" || p.DocumentID == "globex-plan" {
			t.Errorf("Search leaked %s to bob: %+v", p.DocumentID, p)
		}
	}

	first := passages[0]
	if !strings.Contains(strings.ToLower(first.Text), "handling time") {
		t.Errorf("the best passage does not contain the query: %q", first.Text)
	}
	for _, p := range passages {
		if strings.Contains(p.Text, "not measured at Globex") {
			t.Errorf("Search leaked a passage of another tenant: %+v", p)
		}
	}

	// A superseded version is still returned, because the engine needs it to
	// report the disagreement, but the current version ranks above it.
	v1Index, v2Index := -1, -1
	for i, p := range passages {
		if p.DocumentID != "pilot-notes" {
			continue
		}
		switch p.Version {
		case 1:
			v1Index = i
		case 2:
			v2Index = i
		}
	}
	if v1Index == -1 || v2Index == -1 {
		t.Fatalf("Search returned versions %v, want both versions of pilot-notes", passages)
	}
	if v2Index > v1Index {
		t.Errorf("the superseded version ranks above the current one: %d before %d", v1Index, v2Index)
	}

	limited, err := src.Search(ctx, bob, "handling time", 1)
	if err != nil {
		t.Fatalf("Search with a limit: %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("Search with limit 1 returned %d passages", len(limited))
	}

	none, err := src.Search(ctx, bob, "photosynthesis", 8)
	if err != nil {
		t.Fatalf("Search with no match: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("Search returned %d passages for a query nothing answers", len(none))
	}

	empty, err := src.Search(ctx, bob, "", 8)
	if err != nil {
		t.Fatalf("Search with an empty query: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("Search returned %d passages for an empty query", len(empty))
	}
}

func TestPassageOffsetsAreRuneOffsets(t *testing.T) {
	src, err := NewMemorySource([]Document{{
		ID: "unicode", Title: "Unicode", Owner: "ana", Tenant: "acme", Version: 1,
		Text: "Ceñía el café ☕.\n\nLater, handling time fell by 30%.",
	}})
	if err != nil {
		t.Fatalf("NewMemorySource: %v", err)
	}
	passages, err := src.Search(context.Background(), ana, "handling time", 8)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(passages) != 1 {
		t.Fatalf("Search returned %d passages, want 1", len(passages))
	}
	body, err := src.Get(context.Background(), ana, "unicode", 1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	runes := []rune(body.Text)
	got := passages[0]
	if got.Start < 0 || got.Start+len([]rune(got.Text)) > len(runes) {
		t.Fatalf("passage offsets %d..%d are outside the body of %d runes", got.Start, got.Start+len([]rune(got.Text)), len(runes))
	}
	if want := string(runes[got.Start : got.Start+len([]rune(got.Text))]); want != got.Text {
		t.Errorf("rune offset %d does not address the passage: got %q, want %q", got.Start, want, got.Text)
	}
}
