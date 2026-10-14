package research

import "fmt"

// unavailableDetail is deliberately the same text whether the reference names
// a document that does not exist or one the principal may not read: the answer
// must never become an existence oracle.
const unavailableDetail = "no readable document matches this reference"

// resolveScope turns the requested references into the documents a run may
// search, and reports every reference it could not resolve.
//
// A reference that names a document without a version resolves to every
// readable version of it. That is deliberate: a document says things across its
// versions, and a version that a newer one replaced has to stay reachable for
// the disagreement to be reported instead of disappearing the moment the corpus
// is asked about the document by name.
func resolveScope(scoped []DocumentMeta, refs []string) ([]DocumentMeta, []Gap) {
	if len(refs) == 0 {
		return scoped, nil
	}
	var out []DocumentMeta
	var gaps []Gap
	taken := make(map[string]bool, len(scoped))

	for _, ref := range refs {
		id, version, err := ParseRef(ref)
		if err != nil {
			gaps = append(gaps, Gap{Kind: GapUnavailable, Ref: ref, Detail: "the reference is malformed"})
			continue
		}
		matched := false
		for _, meta := range scoped {
			if meta.ID != id {
				continue
			}
			if version != 0 && meta.Version != version {
				continue
			}
			matched = true
			if taken[meta.Ref()] {
				continue
			}
			taken[meta.Ref()] = true
			out = append(out, meta)
		}
		if !matched {
			gaps = append(gaps, Gap{Kind: GapUnavailable, Ref: ref, Detail: unavailableDetail})
		}
	}
	return out, gaps
}

// noMatchGaps reports the requested documents that resolved but contributed no
// passage, which is what makes an irrelevant document visible instead of
// silently ignored.
func noMatchGaps(inScope []DocumentMeta, passages []Passage, refs []string) []Gap {
	withPassage := make(map[string]bool, len(passages))
	for _, passage := range passages {
		withPassage[passage.Ref()] = true
	}
	reported := make(map[string]bool, len(inScope))
	var gaps []Gap
	for _, ref := range refs {
		id, version, err := ParseRef(ref)
		if err != nil {
			continue
		}
		matched, contributed := false, false
		for _, meta := range inScope {
			if meta.ID != id {
				continue
			}
			if version != 0 && meta.Version != version {
				continue
			}
			matched = true
			if withPassage[meta.Ref()] {
				contributed = true
			}
		}
		if !matched || contributed || reported[ref] {
			continue
		}
		reported[ref] = true
		gaps = append(gaps, Gap{
			Kind:   GapNoMatch,
			Ref:    ref,
			Detail: "no passage of this document matches the question",
		})
	}
	return gaps
}

// unsupportedGaps makes every statement the answer could not back visible as a
// gap, so "insufficient evidence" is a first-class outcome rather than a claim
// a reader has to doubt.
func unsupportedGaps(claims []Claim) []Gap {
	var gaps []Gap
	for _, claim := range claims {
		if claim.Supported {
			continue
		}
		detail := fmt.Sprintf("claim %s is not supported by any validated quote", claim.ID)
		if claim.Reason != "" {
			detail += ": " + claim.Reason
		}
		gaps = append(gaps, Gap{Kind: GapUnsupportedClaim, Detail: detail})
	}
	return gaps
}
