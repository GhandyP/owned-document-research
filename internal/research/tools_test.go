package research

import "testing"

func TestSearchToolStaysInsideThePrincipalScope(t *testing.T) {
	handler := searchHandler(New(testCorpus(t), nil, nil), bob)

	result, err := handler(nil, SearchArgs{Query: "handling time"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Passages) == 0 {
		t.Fatal("the search tool returned nothing for a query the corpus answers")
	}
	for _, passage := range result.Passages {
		if passage.DocumentID == "audit-2026" || passage.DocumentID == "globex-plan" {
			t.Errorf("the search tool returned %s to bob: %+v", passage.DocumentID, passage)
		}
	}

	none, err := handler(nil, SearchArgs{Query: "photosynthesis in kelp"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(none.Passages) != 0 {
		t.Errorf("the search tool invented passages: %+v", none.Passages)
	}
	if none.Note == "" {
		t.Error("an empty search result carries no explanation")
	}
}

func TestSearchToolBoundsTheLimit(t *testing.T) {
	handler := searchHandler(New(testCorpus(t), nil, nil), bob)

	huge, err := handler(nil, SearchArgs{Query: "handling time", Limit: 1_000_000})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(huge.Passages) > maxToolPassages {
		t.Errorf("the search tool returned %d passages, more than the bound of %d", len(huge.Passages), maxToolPassages)
	}

	negative, err := handler(nil, SearchArgs{Query: "handling time", Limit: -3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(negative.Passages) == 0 {
		t.Error("a negative limit turned into an empty result instead of the default")
	}
}
