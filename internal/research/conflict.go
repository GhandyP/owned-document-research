package research

import "sort"

// detectConflicts reports every disagreement the answer refuses to resolve.
//
// Two shapes matter. Evidence that argues both ways on one statement is a
// polarity conflict. Evidence drawn from a version that a newer version
// replaced is a superseded-version conflict. Neither is resolved here: the
// conflict names both sides and says a human has to decide.
func detectConflicts(claims []Claim, superseded map[string]bool, replacedBy map[string]string) []Conflict {
	var conflicts []Conflict
	for _, claim := range claims {
		supports, contradicts := false, false
		refs := make(map[string]bool)
		staleRefs := make(map[string]bool)
		for _, evidence := range claim.Evidence {
			refs[evidence.Ref] = true
			if evidence.Polarity == PolarityContradicts {
				contradicts = true
			} else {
				supports = true
			}
			if superseded[evidence.Ref] {
				staleRefs[evidence.Ref] = true
			}
		}
		if supports && contradicts {
			conflicts = append(conflicts, Conflict{
				Kind:       ConflictPolarity,
				Statement:  claim.Statement,
				Refs:       sortedRefs(refs),
				Resolution: ResolutionUnresolved,
			})
		}
		if len(staleRefs) > 0 {
			named := make(map[string]bool, 2*len(staleRefs))
			for ref := range staleRefs {
				named[ref] = true
				if replacement := replacedBy[ref]; replacement != "" {
					named[replacement] = true
				}
			}
			conflicts = append(conflicts, Conflict{
				Kind:       ConflictSuperseded,
				Statement:  claim.Statement,
				Refs:       sortedRefs(named),
				Resolution: ResolutionUnresolved,
			})
		}
	}
	return conflicts
}

// sortedRefs returns the reference names in a stable order, so two runs over
// the same corpus produce the same answer.
func sortedRefs(set map[string]bool) []string {
	refs := make([]string, 0, len(set))
	for ref := range set {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

// replacementRefs maps a replaced reference to the version that replaced it.
func replacementRefs(metas []DocumentMeta) map[string]string {
	out := make(map[string]string)
	for _, meta := range metas {
		if meta.Supersedes != "" {
			out[meta.Supersedes] = meta.Ref()
		}
	}
	return out
}
