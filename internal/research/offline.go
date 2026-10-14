package research

import (
	"context"
	"strings"
	"unicode"
)

// minimumMatchedTokens is how many distinct question words a sentence has to
// share with the question before it counts as an answer to it. One word is
// enough for a one-word question.
const minimumMatchedTokens = 2

// negationCues are the words that flip a sentence's stance towards the
// statement it shares its subject with.
var negationCues = map[string]bool{
	"absent":   true,
	"cannot":   true,
	"denied":   true,
	"denies":   true,
	"failed":   true,
	"lacks":    true,
	"never":    true,
	"no":       true,
	"not":      true,
	"unable":   true,
	"without":  true,
	"revoked":  true,
	"declined": true,
}

// RuleExtractor proposes quotes with rules instead of with a model: every
// sentence that shares enough words with the question becomes a candidate, and
// a negation cue in that sentence flips its polarity.
//
// It exists so the whole pipeline, and every test in this repository, can run
// with no network and no credential, and so the deterministic path is a real
// answer rather than a fallback that has never been executed.
type RuleExtractor struct{}

// Extract implements Extractor.
func (RuleExtractor) Extract(_ context.Context, question string, passages []Passage) ([]Candidate, error) {
	queryTokens := uniqueTokens(tokenize(question))
	if len(queryTokens) == 0 {
		return nil, nil
	}
	required := minimumMatchedTokens
	if len(queryTokens) < required {
		required = 1
	}
	var out []Candidate
	for _, passage := range passages {
		for _, sentence := range sentences(passage.Text) {
			if matchedTokens(sentence, queryTokens) < required {
				continue
			}
			out = append(out, Candidate{
				Ref:      passage.Ref(),
				Quote:    sentence,
				Polarity: polarityOf(sentence),
			})
		}
	}
	return out, nil
}

// RuleSynthesizer states the answer as the quote that shares the most words
// with the question, and attaches every other candidate to that same finding.
//
// Keeping one finding for the question is deliberate: it is what puts
// contradictory quotes side by side inside one claim, so the conflict layer
// reports the disagreement instead of a reader having to notice it across
// separate claims.
type RuleSynthesizer struct{}

// Synthesize implements Synthesizer.
func (RuleSynthesizer) Synthesize(_ context.Context, question string, candidates []Candidate) ([]Finding, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	indexes := make([]int, 0, len(candidates))
	for i := range candidates {
		indexes = append(indexes, i)
	}
	statement := candidates[primaryCandidate(question, candidates)].Quote
	return []Finding{{
		Statement:        statement,
		CandidateIndexes: indexes,
		Confidence:       ConfidenceMedium,
	}}, nil
}

// primaryCandidate picks the quote that answers the most of the question,
// preferring a supporting quote over a contradicting one and using the longer
// quote to break ties, so the choice does not depend on the order candidates
// arrived in.
func primaryCandidate(question string, candidates []Candidate) int {
	queryTokens := uniqueTokens(tokenize(question))
	best, bestScore, bestLength := 0, -1, -1
	for i, candidate := range candidates {
		score := matchedTokens(candidate.Quote, queryTokens)
		if candidate.Polarity != PolaritySupports {
			// A contradicting quote never states the answer, but it can still
			// be the only thing there is to say.
			score = -1
		}
		length := len([]rune(candidate.Quote))
		if score > bestScore || (score == bestScore && length > bestLength) {
			best, bestScore, bestLength = i, score, length
		}
	}
	return best
}

// sentences cuts a text at sentence boundaries and trims each one, so every
// quote is copied from the text rather than rebuilt.
func sentences(text string) []string {
	runes := []rune(text)
	var out []string
	start := 0
	for i, r := range runes {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		if i+1 < len(runes) && !unicode.IsSpace(runes[i+1]) {
			continue
		}
		if sentence := strings.TrimSpace(string(runes[start : i+1])); sentence != "" {
			out = append(out, sentence)
		}
		start = i + 1
	}
	if rest := strings.TrimSpace(string(runes[start:])); rest != "" {
		out = append(out, rest)
	}
	return out
}

// matchedTokens counts how many distinct question words appear in a text.
func matchedTokens(text string, queryTokens []string) int {
	words := tokenize(text)
	matched := 0
	for _, query := range queryTokens {
		for _, word := range words {
			if wordsMatch(query, word) {
				matched++
				break
			}
		}
	}
	return matched
}

// wordsMatch reports whether two words are the same word. A word also matches
// when one is a prefix of the other and both are at least four runes long,
// which is what lets "reduce" match "reduced" and "changed" match "change"
// without a stemmer.
func wordsMatch(a, b string) bool {
	if a == b {
		return true
	}
	if len([]rune(a)) < 4 || len([]rune(b)) < 4 {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// polarityOf reads a sentence's stance from its negation cues.
func polarityOf(sentence string) Polarity {
	for _, token := range tokenize(sentence) {
		if negationCues[token] {
			return PolarityContradicts
		}
	}
	return PolaritySupports
}
