package research

import (
	"sort"
	"strings"
	"unicode"
)

// DefaultSearchLimit bounds a search that does not ask for a size.
const DefaultSearchLimit = 8

var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "by": true, "did": true, "do": true, "does": true, "for": true,
	"from": true, "in": true, "is": true, "it": true, "of": true, "on": true,
	"or": true, "that": true, "the": true, "this": true, "to": true, "was": true,
	"were": true, "what": true, "which": true, "with": true,
}

// tokenize lowercases a text and splits it into word tokens, dropping the
// stopwords that carry no retrieval signal in a question.
func tokenize(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		if stopwords[field] {
			continue
		}
		tokens = append(tokens, field)
	}
	return tokens
}

// uniqueTokens returns the distinct query tokens in their first-seen order.
func uniqueTokens(tokens []string) []string {
	seen := make(map[string]bool, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	return out
}

// splitBlocks cuts a document body into blank-line separated blocks and records
// the rune offset of each one, so a passage can always be addressed back to the
// exact document bytes.
func splitBlocks(text string) []Passage {
	var blocks []Passage
	start := 0
	for _, block := range strings.Split(text, "\n\n") {
		if strings.TrimSpace(block) != "" {
			blocks = append(blocks, Passage{Text: block, Start: start})
		}
		start += len([]rune(block)) + len([]rune("\n\n"))
	}
	return blocks
}

// score counts how many times the distinct query tokens occur in a text.
func score(queryTokens []string, text string) int {
	counts := make(map[string]int)
	for _, token := range tokenize(text) {
		counts[token]++
	}
	total := 0
	for _, token := range queryTokens {
		total += counts[token]
	}
	return total
}

// scoredPassage carries the retrieval score and the staleness of a passage
// while a search is being assembled.
type scoredPassage struct {
	passage Passage
	score   int
	stale   bool
}

// rankPassages orders passages deterministically: the highest score first, then
// passages that are not superseded, then the newest version, then the document
// id and the offset. Deterministic order is what makes the fixture tests
// reproducible.
func rankPassages(scored []scoredPassage) []Passage {
	sort.SliceStable(scored, func(i, j int) bool {
		a, b := scored[i], scored[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.stale != b.stale {
			return !a.stale
		}
		if a.passage.DocumentID != b.passage.DocumentID {
			return a.passage.DocumentID < b.passage.DocumentID
		}
		if a.passage.Version != b.passage.Version {
			return a.passage.Version > b.passage.Version
		}
		return a.passage.Start < b.passage.Start
	})
	out := make([]Passage, 0, len(scored))
	for _, s := range scored {
		out = append(out, s.passage)
	}
	return out
}
