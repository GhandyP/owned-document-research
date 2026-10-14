package research

import (
	"fmt"
	"strings"
	"time"
)

// Document is one versioned, owned document held by a corpus.
//
// A document is never overwritten in place: a new version is added with a
// higher Version, and it names the version it replaces in Supersedes. Keeping
// every version is what lets the engine notice that a quote came from a version
// nobody should rely on any more.
type Document struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Owner      string    `json:"owner"`
	Tenant     string    `json:"tenant"`
	Allow      []string  `json:"allow,omitempty"` // "user:<id>" | "group:<name>" | "*"
	Version    int       `json:"version"`
	Supersedes string    `json:"supersedes,omitempty"` // "<id>@<version>"
	UpdatedAt  time.Time `json:"updated_at"`
	Tags       []string  `json:"tags,omitempty"`
	Text       string    `json:"text"`
}

// Ref returns the "<document id>@<version>" reference of this version.
func (d Document) Ref() string {
	return fmt.Sprintf("%s@%d", d.ID, d.Version)
}

// Validate reports whether the document is well formed enough to be indexed.
// An empty body is allowed on purpose: an owned document may be a stub, and the
// fixture set must contain one.
func (d Document) Validate() error {
	if strings.TrimSpace(d.ID) == "" {
		return fmt.Errorf("document: id is required")
	}
	if strings.ContainsAny(d.ID, " \t\n@") {
		return fmt.Errorf("document %q: id must not contain whitespace or '@'", d.ID)
	}
	if strings.TrimSpace(d.Tenant) == "" {
		return fmt.Errorf("document %q: tenant is required", d.ID)
	}
	if strings.TrimSpace(d.Owner) == "" {
		return fmt.Errorf("document %q: owner is required", d.ID)
	}
	if d.Version < 1 {
		return fmt.Errorf("document %q: version must be 1 or greater, got %d", d.ID, d.Version)
	}
	if d.Supersedes != "" {
		if _, _, err := ParseRef(d.Supersedes); err != nil {
			return fmt.Errorf("document %q: %w", d.ID, err)
		}
	}
	for _, entry := range d.Allow {
		if err := ValidAllowEntry(entry); err != nil {
			return fmt.Errorf("document %q: %w", d.ID, err)
		}
	}
	return nil
}
