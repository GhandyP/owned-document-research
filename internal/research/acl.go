package research

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Principal is the identity a research request runs as. Permissions are decided
// from it, never from anything a model returns.
type Principal struct {
	ID     string   `json:"id"`
	Groups []string `json:"groups,omitempty"`
	Tenant string   `json:"tenant,omitempty"`
}

// ReadableBy reports whether the principal may read this document version.
func (d Document) ReadableBy(p Principal) bool {
	return readableBy(d.Owner, d.Tenant, d.Allow, p)
}

// ReadableBy reports whether the principal may read this version according to
// the metadata alone, which is what lets the engine re-check permissions on a
// reference it only has metadata for.
func (m DocumentMeta) ReadableBy(p Principal) bool {
	return readableBy(m.Owner, m.Tenant, m.Allow, p)
}

// readableBy is the single permission rule of the package.
//
// The tenant boundary is absolute: a document is only readable inside its own
// tenant, however the allow list is written, so a stray "user:" or "*" entry can
// never open a document to another tenant. Inside the tenant, the owner always
// reads their document, and any other principal needs a matching allow entry.
func readableBy(owner, tenant string, allow []string, p Principal) bool {
	if tenant == "" || p.Tenant == "" || tenant != p.Tenant {
		return false
	}
	if p.ID != "" && p.ID == owner {
		return true
	}
	if p.ID == "" {
		return false
	}
	for _, entry := range allow {
		switch {
		case entry == "*":
			return true
		case entry == "user:"+p.ID:
			return true
		case strings.HasPrefix(entry, "group:"):
			name := strings.TrimPrefix(entry, "group:")
			for _, group := range p.Groups {
				if group == name {
					return true
				}
			}
		}
	}
	return false
}

var allowEntryPattern = regexp.MustCompile(`^(?:\*|(?:user|group):[a-z0-9][a-z0-9._-]*)$`)

// ValidAllowEntry reports whether an allow-list entry has one of the accepted
// shapes: "*", "user:<id>", or "group:<name>".
func ValidAllowEntry(entry string) error {
	if entry == "" {
		return fmt.Errorf("allow entry is empty")
	}
	if !allowEntryPattern.MatchString(entry) {
		return fmt.Errorf("allow entry %q must be \"*\", \"user:<id>\", or \"group:<name>\" in lower case", entry)
	}
	return nil
}

var refIDPattern = regexp.MustCompile(`^[^\s@]+$`)

// ParseRef splits a document reference into its id and version. "id" means any
// version, and is reported as version 0; "id@3" means that exact version.
func ParseRef(ref string) (string, int, error) {
	if ref == "" {
		return "", 0, fmt.Errorf("document reference is empty")
	}
	parts := strings.Split(ref, "@")
	switch len(parts) {
	case 1:
		if !refIDPattern.MatchString(parts[0]) {
			return "", 0, fmt.Errorf("document reference %q must name a document", ref)
		}
		return parts[0], 0, nil
	case 2:
		id, raw := parts[0], parts[1]
		if !refIDPattern.MatchString(id) {
			return "", 0, fmt.Errorf("document reference %q must name a document", ref)
		}
		if raw == "" {
			return "", 0, fmt.Errorf("document reference %q has no version after '@'", ref)
		}
		version, err := strconv.Atoi(raw)
		if err != nil {
			return "", 0, fmt.Errorf("document reference %q has a non-numeric version: %w", ref, err)
		}
		if version < 1 {
			return "", 0, fmt.Errorf("document reference %q must use version 1 or greater", ref)
		}
		return id, version, nil
	default:
		return "", 0, fmt.Errorf("document reference %q has more than one '@'", ref)
	}
}
