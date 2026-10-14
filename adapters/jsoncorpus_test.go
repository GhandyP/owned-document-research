package adapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GhandyP/owned-document-research/internal/research"
)

// corpusPath is the corpus the command line demonstrates with, loaded through
// the adapter under test rather than through a second fixture.
const corpusPath = "../testdata/corpus.json"

func TestLoadJSONCorpusAppliesThePermissionRules(t *testing.T) {
	source, err := LoadJSONCorpus(corpusPath)
	if err != nil {
		t.Fatalf("LoadJSONCorpus: %v", err)
	}
	ctx := context.Background()

	ana := research.Principal{ID: "ana@acme.test", Tenant: "drive-acme"}
	metas, err := source.List(ctx, ana)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	refs := make([]string, 0, len(metas))
	for _, meta := range metas {
		refs = append(refs, meta.Ref())
	}
	joined := strings.Join(refs, ",")
	for _, want := range []string{"pilot-notes@1", "pilot-notes@2", "ops-handbook@1", "canteen-memo@1", "empty-stub@1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the owner cannot see %s: %v", want, refs)
		}
	}
	if strings.Contains(joined, "audit-2026") {
		t.Errorf("a legal-only document is listed to the owner: %v", refs)
	}
	if strings.Contains(joined, "globex-benchmark") {
		t.Errorf("another tenant's document is listed: %v", refs)
	}

	globex := research.Principal{ID: "eve@globex.test", Tenant: "drive-globex"}
	otherTenant, err := source.List(ctx, globex)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(otherTenant) != 1 || otherTenant[0].Ref() != "globex-benchmark@1" {
		t.Errorf("the other tenant sees %+v, want only its own document", otherTenant)
	}

	// A forbidden reference and an absent one are the same answer.
	_, forbidden := source.Get(ctx, ana, "audit-2026", 0)
	_, absent := source.Get(ctx, ana, "nothing-here", 0)
	if !errors.Is(forbidden, research.ErrUnavailable) || !errors.Is(absent, research.ErrUnavailable) {
		t.Fatalf("Get returned %v for a forbidden document and %v for an absent one", forbidden, absent)
	}
	if forbidden.Error() != absent.Error() {
		t.Errorf("a forbidden document is distinguishable from an absent one: %q vs %q", forbidden, absent)
	}
}

func TestLoadJSONCorpusRefusesAMalformedFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "a misspelled field",
			body: `{"documents":[{"id":"a","title":"A","owner":"ana@acme.test","tenant":"drive-acme","version":1,"allows":["*"]}]}`,
			want: "allows",
		},
		{
			name: "no documents",
			body: `{"documents":[]}`,
			want: "no documents",
		},
		{
			name: "a document with no version",
			body: `{"documents":[{"id":"a","title":"A","owner":"ana@acme.test","tenant":"drive-acme"}]}`,
			want: "version",
		},
		{
			name: "a malformed allow entry",
			body: `{"documents":[{"id":"a","title":"A","owner":"ana@acme.test","tenant":"drive-acme","version":1,"allow":["Everyone"]}]}`,
			want: "allow entry",
		},
		{
			name: "not json at all",
			body: `documents: none`,
			want: "corpus",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadJSONCorpus(write("corpus.json", tt.body))
			if err == nil {
				t.Fatalf("LoadJSONCorpus accepted %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}

	if _, err := LoadJSONCorpus(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("LoadJSONCorpus accepted a path that does not exist")
	}
}
