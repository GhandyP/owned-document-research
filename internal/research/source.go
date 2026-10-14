package research

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// ErrUnavailable is returned for a document reference that cannot be served.
//
// A reference that does not exist and a reference the principal may not read
// return this same error, with the same message: the difference would otherwise
// turn any answer into an existence oracle for documents the caller has no
// right to know about.
var ErrUnavailable = errors.New("document is not available")

// DocumentMeta is a document reference without its body.
type DocumentMeta struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Owner      string   `json:"owner"`
	Tenant     string   `json:"tenant"`
	Allow      []string `json:"allow,omitempty"`
	Version    int      `json:"version"`
	Supersedes string   `json:"supersedes,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
	Tags       []string `json:"tags,omitempty"`
}

// Ref returns the "<document id>@<version>" reference of this version.
func (m DocumentMeta) Ref() string {
	return fmt.Sprintf("%s@%d", m.ID, m.Version)
}

// Meta returns the document without its body.
func (d Document) Meta() DocumentMeta {
	return DocumentMeta{
		ID:         d.ID,
		Title:      d.Title,
		Owner:      d.Owner,
		Tenant:     d.Tenant,
		Allow:      d.Allow,
		Version:    d.Version,
		Supersedes: d.Supersedes,
		Tags:       d.Tags,
	}
}

// DocumentSource is the port every corpus adapter implements.
//
// The contract is permission first: an implementation must never return a
// document, a body, or a passage the principal may not read, and must not let a
// caller tell a forbidden reference apart from an absent one. The engine
// re-checks readability on everything it receives, so an adapter that gets this
// wrong still cannot leak, but it is the adapter's first duty.
type DocumentSource interface {
	// List returns the reference metadata of every document the principal may
	// read, ordered by document id and version.
	List(ctx context.Context, p Principal) ([]DocumentMeta, error)
	// Get returns one document version with its body. A version of 0 means the
	// newest version. It returns an error wrapping ErrUnavailable when the
	// reference is absent, forbidden, or has no such version.
	Get(ctx context.Context, p Principal, id string, version int) (Document, error)
	// Search returns at most limit passages that match the query lexically,
	// best first, restricted to what the principal may read.
	Search(ctx context.Context, p Principal, query string, limit int) ([]Passage, error)
}

// MemorySource is the in-memory implementation of DocumentSource. It is the
// reference implementation of the permission rules: the local corpus adapter
// and the tests are built on it.
type MemorySource struct {
	docs []Document
}

// NewMemorySource validates every document and indexes it by reference.
func NewMemorySource(docs []Document) (*MemorySource, error) {
	seen := make(map[string]bool, len(docs))
	copied := make([]Document, 0, len(docs))
	for _, doc := range docs {
		if err := doc.Validate(); err != nil {
			return nil, err
		}
		ref := doc.Ref()
		if seen[ref] {
			return nil, fmt.Errorf("document %s appears twice", ref)
		}
		seen[ref] = true
		copied = append(copied, doc)
	}
	sort.Slice(copied, func(i, j int) bool {
		if copied[i].ID != copied[j].ID {
			return copied[i].ID < copied[j].ID
		}
		return copied[i].Version < copied[j].Version
	})
	return &MemorySource{docs: copied}, nil
}

// List returns the metadata of every readable document.
func (s *MemorySource) List(_ context.Context, p Principal) ([]DocumentMeta, error) {
	metas := make([]DocumentMeta, 0, len(s.docs))
	for _, doc := range s.docs {
		if !doc.ReadableBy(p) {
			continue
		}
		metas = append(metas, doc.Meta())
	}
	return metas, nil
}

// Get returns one readable document version.
//
// The newest version is resolved before readability is checked, so a document
// whose latest version is restricted is reported as unavailable instead of
// silently falling back to an older version the caller happens to be able to
// read: stale evidence must never be presented as the current document.
func (s *MemorySource) Get(_ context.Context, p Principal, id string, version int) (Document, error) {
	var candidates []Document
	for _, doc := range s.docs {
		if doc.ID == id {
			candidates = append(candidates, doc)
		}
	}
	if len(candidates) == 0 {
		return Document{}, ErrUnavailable
	}
	wanted := version
	if wanted == 0 {
		wanted = candidates[len(candidates)-1].Version
	}
	for _, doc := range candidates {
		if doc.Version != wanted {
			continue
		}
		if !doc.ReadableBy(p) {
			break
		}
		return doc, nil
	}
	return Document{}, ErrUnavailable
}

// Search returns the best matching passage per readable document.
func (s *MemorySource) Search(_ context.Context, p Principal, query string, limit int) ([]Passage, error) {
	queryTokens := uniqueTokens(tokenize(query))
	if len(queryTokens) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	superseded := supersededSet(s.metas())
	var scored []scoredPassage
	for _, doc := range s.docs {
		if !doc.ReadableBy(p) {
			continue
		}
		best := scoredPassage{score: -1}
		for _, block := range splitBlocks(doc.Text) {
			block.DocumentID = doc.ID
			block.Version = doc.Version
			block.Title = doc.Title
			block.Owner = doc.Owner
			got := score(queryTokens, block.Text)
			if got > best.score {
				best = scoredPassage{passage: block, score: got, stale: superseded[doc.Ref()]}
			}
		}
		if best.score > 0 {
			scored = append(scored, best)
		}
	}
	ranked := rankPassages(scored)
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked, nil
}

func (s *MemorySource) metas() []DocumentMeta {
	metas := make([]DocumentMeta, 0, len(s.docs))
	for _, doc := range s.docs {
		metas = append(metas, doc.Meta())
	}
	return metas
}

// supersededSet collects the references that some newer version says it
// replaces.
func supersededSet(metas []DocumentMeta) map[string]bool {
	out := make(map[string]bool)
	for _, meta := range metas {
		if meta.Supersedes != "" {
			out[meta.Supersedes] = true
		}
	}
	return out
}
