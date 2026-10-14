package research

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// validated is a proposal that survived the deterministic checks, together with
// the evidence it produced.
type validated struct {
	candidate Candidate
	evidence  Evidence
	meta      DocumentMeta
}

// validate turns proposals into evidence.
//
// A proposal has to earn its place: it must name a reference that was actually
// retrieved for this principal, name an exact version, carry a valid polarity
// and a non-empty quote, and that quote must appear verbatim in the body of the
// cited version. Anything else is discarded and reported, never quietly
// repaired.
func (e *Engine) validate(ctx context.Context, p Principal, candidates []Candidate, passages map[string]Passage, metas map[string]DocumentMeta) ([]validated, []string) {
	var out []validated
	var warnings []string
	bodies := make(map[string]Document)

	for _, candidate := range candidates {
		id, version, err := ParseRef(candidate.Ref)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("dropped a quote for a malformed reference: %q", candidate.Ref))
			continue
		}
		if version == 0 {
			warnings = append(warnings, fmt.Sprintf("dropped a quote that does not name a version: %q", candidate.Ref))
			continue
		}
		if _, retrieved := passages[candidate.Ref]; !retrieved {
			warnings = append(warnings, unavailableWarning(candidate.Ref))
			continue
		}
		if !candidate.Polarity.Valid() {
			warnings = append(warnings, fmt.Sprintf("dropped evidence with an invalid polarity: %q", candidate.Polarity))
			continue
		}
		quote := strings.TrimSpace(candidate.Quote)
		if quote == "" {
			warnings = append(warnings, "dropped an empty quote")
			continue
		}

		body, cached := bodies[candidate.Ref]
		if !cached {
			fetched, err := e.source.Get(ctx, p, id, version)
			if err != nil || !fetched.ReadableBy(p) {
				warnings = append(warnings, unavailableWarning(candidate.Ref))
				continue
			}
			body = fetched
			bodies[candidate.Ref] = body
		}

		start, found := runeIndexOf(body.Text, quote)
		if !found {
			warnings = append(warnings, fmt.Sprintf("dropped a quote that does not appear in %s: %q", candidate.Ref, truncate(quote, 120)))
			continue
		}

		out = append(out, validated{
			candidate: Candidate{Ref: candidate.Ref, Quote: quote, Polarity: candidate.Polarity},
			evidence: Evidence{
				Ref:      candidate.Ref,
				Quote:    quote,
				Start:    start,
				End:      start + len([]rune(quote)),
				Polarity: candidate.Polarity,
			},
			meta: metas[candidate.Ref],
		})
	}
	return out, warnings
}

func unavailableWarning(ref string) string {
	return fmt.Sprintf("dropped a quote for a reference that is not available: %s", ref)
}

// runeIndexOf returns the rune offset of quote inside body. Offsets are runes,
// never bytes: a quote must be addressable in the same units a reader counts.
func runeIndexOf(body, quote string) (int, bool) {
	byteIndex := strings.Index(body, quote)
	if byteIndex < 0 {
		return 0, false
	}
	return len([]rune(body[:byteIndex])), true
}

// truncate shortens a text for a warning, on a rune boundary.
func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// buildClaims turns findings into claims and evidence rows.
//
// A finding that cannot be wired to validated evidence is kept as an
// unsupported claim rather than dropped: a reader is entitled to see what the
// model asserted and why it did not stand.
func buildClaims(findings []Finding, validated []validated, metas map[string]DocumentMeta, superseded map[string]bool) ([]Claim, []EvidenceRow, []string) {
	var claims []Claim
	var warnings []string
	seenStatements := make(map[string]int)

	for _, finding := range findings {
		statement := strings.TrimSpace(finding.Statement)
		if statement == "" {
			warnings = append(warnings, "dropped a finding with no statement")
			continue
		}

		var collected []Evidence
		dedup := make(map[string]bool)
		unknownIndex := false
		for _, idx := range finding.CandidateIndexes {
			if idx < 0 || idx >= len(validated) {
				unknownIndex = true
				continue
			}
			evidence := validated[idx].evidence
			key := fmt.Sprintf("%s|%d|%d", evidence.Ref, evidence.Start, evidence.End)
			if dedup[key] {
				continue
			}
			dedup[key] = true
			collected = append(collected, evidence)
		}
		sort.SliceStable(collected, func(i, j int) bool {
			if collected[i].Ref != collected[j].Ref {
				return collected[i].Ref < collected[j].Ref
			}
			return collected[i].Start < collected[j].Start
		})

		supports, contradicts, stale := false, false, false
		for _, evidence := range collected {
			if evidence.Polarity == PolarityContradicts {
				contradicts = true
			} else {
				supports = true
			}
			if superseded[evidence.Ref] {
				stale = true
			}
		}

		claim := Claim{ID: "", Statement: statement, Confidence: ConfidenceNone}
		switch {
		case len(collected) == 0 && unknownIndex:
			claim.Reason = "the proposal cited evidence that is not in the retrieved set"
		case len(collected) == 0:
			claim.Reason = "the proposal cited no evidence"
		case !supports:
			claim.Reason = "the retrieved evidence contradicts this statement"
		default:
			claim.Supported = true
			claim.Evidence = collected
			claim.Confidence = Weaker(validConfidence(finding.Confidence), evidenceCap(stale, contradicts))
		}

		// Merging repeats must never raise confidence, and must never turn an
		// unsupported statement into a supported one.
		if at, merged := seenStatements[normalizeStatement(statement)]; merged {
			existing := &claims[at]
			existing.Evidence = mergeEvidence(existing.Evidence, claim.Evidence)
			existing.Supported = existing.Supported || claim.Supported
			if existing.Supported {
				existing.Reason = ""
				existing.Confidence = Weaker(validConfidence(existing.Confidence), Weaker(claim.Confidence, validConfidence(finding.Confidence)))
			}
			continue
		}
		seenStatements[normalizeStatement(statement)] = len(claims)
		claims = append(claims, claim)
	}

	for i := range claims {
		if claims[i].ID == "" {
			claims[i].ID = fmt.Sprintf("c%d", i+1)
		}
	}
	return claims, evidenceRows(claims, metas, superseded), warnings
}

// validConfidence keeps a proposal's confidence inside the defined levels. A
// supported claim never carries none, and an undefined level reads as low.
func validConfidence(c Confidence) Confidence {
	if !c.Valid() || c == ConfidenceNone {
		return ConfidenceLow
	}
	return c
}

// evidenceCap is the ceiling the evidence imposes on a claim's confidence: a
// superseded source, or evidence arguing both ways, cannot support a strong
// claim.
func evidenceCap(stale, contradicts bool) Confidence {
	if stale || contradicts {
		return ConfidenceLow
	}
	return ConfidenceHigh
}

// normalizeStatement collapses the wording of a statement so repeats can be
// recognised as the same statement.
func normalizeStatement(statement string) string {
	return strings.Join(strings.Fields(strings.ToLower(statement)), " ")
}

// mergeEvidence appends evidence that is not already listed, keeping the
// deterministic ref-then-offset order.
func mergeEvidence(existing, added []Evidence) []Evidence {
	seen := make(map[string]bool, len(existing))
	for _, evidence := range existing {
		seen[fmt.Sprintf("%s|%d|%d", evidence.Ref, evidence.Start, evidence.End)] = true
	}
	for _, evidence := range added {
		key := fmt.Sprintf("%s|%d|%d", evidence.Ref, evidence.Start, evidence.End)
		if seen[key] {
			continue
		}
		seen[key] = true
		existing = append(existing, evidence)
	}
	sort.SliceStable(existing, func(i, j int) bool {
		if existing[i].Ref != existing[j].Ref {
			return existing[i].Ref < existing[j].Ref
		}
		return existing[i].Start < existing[j].Start
	})
	return existing
}

// evidenceRows flattens the claims into the evidence table, carrying the
// ownership fields a reader needs to weigh each source.
func evidenceRows(claims []Claim, metas map[string]DocumentMeta, superseded map[string]bool) []EvidenceRow {
	var rows []EvidenceRow
	for _, claim := range claims {
		for _, evidence := range claim.Evidence {
			meta := metas[evidence.Ref]
			rows = append(rows, EvidenceRow{
				ClaimID:  claim.ID,
				Ref:      evidence.Ref,
				Title:    meta.Title,
				Owner:    meta.Owner,
				Tenant:   meta.Tenant,
				Quote:    evidence.Quote,
				Start:    evidence.Start,
				End:      evidence.End,
				Polarity: evidence.Polarity,
				Stale:    superseded[evidence.Ref],
			})
		}
	}
	return rows
}

// answerConfidence reports the confidence of the whole answer: no stronger than
// its weakest supported claim, and no stronger than the unsupported statements
// it still carries.
func answerConfidence(claims []Claim) Confidence {
	confidence := ConfidenceNone
	supported := 0
	unsupported := false
	for _, claim := range claims {
		if !claim.Supported {
			unsupported = true
			continue
		}
		if supported == 0 {
			confidence = claim.Confidence
		} else {
			confidence = Weaker(confidence, claim.Confidence)
		}
		supported++
	}
	if supported == 0 {
		return ConfidenceNone
	}
	if unsupported {
		return Weaker(confidence, ConfidenceMedium)
	}
	return confidence
}

// capWarnings keeps the discarded-proposal list bounded while still disclosing
// how much was discarded.
func capWarnings(warnings []string) []string {
	if len(warnings) <= maxWarnings {
		return warnings
	}
	kept := append([]string{}, warnings[:maxWarnings]...)
	kept = append(kept, fmt.Sprintf("and %d more discarded proposals", len(warnings)-maxWarnings))
	return kept
}
