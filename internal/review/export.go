package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/GhandyP/owned-document-research/internal/research"
)

// ExportRecord is what a reviewed export contains: the answer, and the decision
// that let it out. Both are written so a reader can tell what was approved and
// who approved it.
type ExportRecord struct {
	Review Decision                `json:"review"`
	Answer research.ResearchAnswer `json:"answer"`
}

// Export writes a reviewed answer to a Markdown file, with a JSON sidecar that
// carries the same answer and its approval.
//
// It writes nothing unless the decision authorizes exactly this action.
func Export(path string, answer research.ResearchAnswer, decision Decision) error {
	if !decision.Authorizes(ActionExport) {
		return fmt.Errorf("%w: %s", ErrNotApproved, ActionExport)
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("review: an export path is required")
	}

	record := ExportRecord{Review: decision, Answer: answer}
	sidecar, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("review: encode the answer: %w", err)
	}
	sidecar = append(sidecar, '\n')

	sidecarPath := path + ".json"
	if err := os.WriteFile(sidecarPath, sidecar, 0o600); err != nil {
		return fmt.Errorf("review: write %s: %w", sidecarPath, err)
	}
	if err := os.WriteFile(path, []byte(renderMarkdown(record)), 0o600); err != nil {
		// A sidecar without its answer would be a misleading half export.
		if removeErr := os.Remove(sidecarPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("review: write %s: %w (and %s could not be removed: %v)", path, err, sidecarPath, removeErr)
		}
		return fmt.Errorf("review: write %s: %w", path, err)
	}
	return nil
}

// renderMarkdown writes the answer for a human reader.
func renderMarkdown(record ExportRecord) string {
	answer := record.Answer
	decision := record.Review

	var b strings.Builder
	b.WriteString("# Research answer\n\n")
	fmt.Fprintf(&b, "**Question:** %s\n\n", answer.Question)
	fmt.Fprintf(&b, "**Confidence:** %s\n\n", answer.Confidence)
	b.WriteString("This answer is advisory: it informs a human decision, it does not make one.\n\n")
	fmt.Fprintf(&b, "**Reviewed by:** %s at %s\n\n", decision.By, decision.At.UTC().Format(time.RFC3339))
	if decision.Note != "" {
		fmt.Fprintf(&b, "**Review note:** %s\n\n", decision.Note)
	}

	b.WriteString("## Claims\n\n")
	if len(answer.Claims) == 0 {
		b.WriteString("No claim survived: the corpus did not answer the question.\n\n")
	}
	for _, claim := range answer.Claims {
		status := "unsupported"
		if claim.Supported {
			status = "supported"
		}
		fmt.Fprintf(&b, "### %s — %s\n\n", claim.ID, claim.Statement)
		fmt.Fprintf(&b, "- status: %s\n- confidence: %s\n", status, claim.Confidence)
		if claim.Reason != "" {
			fmt.Fprintf(&b, "- reason: %s\n", claim.Reason)
		}
		b.WriteString("\n")
		for _, evidence := range claim.Evidence {
			row := rowFor(answer.EvidenceTable, claim.ID, evidence.Ref)
			fmt.Fprintf(&b, "> %s\n>\n> — `%s`, %s, %s\n\n", evidence.Quote, evidence.Ref, ownershipOf(row), stanceOf(evidence.Polarity, row))
		}
	}

	if len(answer.Conflicts) > 0 {
		b.WriteString("## Conflicts\n\n")
		b.WriteString("The tool does not choose between these. A human does.\n\n")
		for _, conflict := range answer.Conflicts {
			fmt.Fprintf(&b, "- **%s** on \"%s\": %s — %s\n", conflict.Kind, conflict.Statement, strings.Join(conflict.Refs, ", "), conflict.Resolution)
		}
		b.WriteString("\n")
	}

	if len(answer.Gaps) > 0 {
		b.WriteString("## Gaps\n\n")
		for _, gap := range answer.Gaps {
			if gap.Ref != "" {
				fmt.Fprintf(&b, "- **%s** (%s): %s\n", gap.Kind, gap.Ref, gap.Detail)
				continue
			}
			fmt.Fprintf(&b, "- **%s**: %s\n", gap.Kind, gap.Detail)
		}
		b.WriteString("\n")
	}

	if len(answer.Warnings) > 0 {
		b.WriteString("## Discarded proposals\n\n")
		for _, warning := range answer.Warnings {
			fmt.Fprintf(&b, "- %s\n", warning)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Evidence table\n\n")
	b.WriteString("| claim | document | owner | tenant | polarity | version |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, row := range answer.EvidenceTable {
		version := "current"
		if row.Stale {
			version = "superseded"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", row.ClaimID, row.Ref, row.Owner, row.Tenant, row.Polarity, version)
	}
	return b.String()
}

// rowFor finds the evidence-table row of one piece of evidence.
func rowFor(rows []research.EvidenceRow, claimID, ref string) research.EvidenceRow {
	for _, row := range rows {
		if row.ClaimID == claimID && row.Ref == ref {
			return row
		}
	}
	return research.EvidenceRow{}
}

// ownershipOf describes where a quote came from.
func ownershipOf(row research.EvidenceRow) string {
	source := fmt.Sprintf("from %q owned by %s", row.Title, row.Owner)
	if row.Tenant != "" {
		source += fmt.Sprintf(", tenant %s", row.Tenant)
	}
	return source
}

// stanceOf describes what a quote does to the statement, including whether the
// version it came from has been superseded.
func stanceOf(polarity research.Polarity, row research.EvidenceRow) string {
	stance := "supports the statement"
	if polarity == research.PolarityContradicts {
		stance = "contradicts the statement"
	}
	if row.Stale {
		stance += " (from a superseded version)"
	}
	return stance
}
