// Package review holds the human boundary of the research flow.
//
// A research answer is advisory. This package is what makes that true in code
// rather than in prose: publishing an answer is a separate action with its own
// approval, an approval is only valid for the action it was given for, and a
// refusal writes nothing.
package review

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ErrNotApproved is returned when an action is attempted without an approval
// that authorizes it. Nothing is written in that case.
var ErrNotApproved = errors.New("review: the action was not approved")

// Action names an operation that needs a human decision. Actions are separate
// on purpose: approving one never approves another.
type Action string

// ActionExport publishes a research answer.
const ActionExport Action = "export_research_answer"

// Request is what a reviewer is asked to approve.
type Request struct {
	Action  Action   `json:"action"`
	Summary string   `json:"summary"`
	Details []string `json:"details,omitempty"`
}

// Decision is a reviewer's answer, attributable to a person and a moment.
type Decision struct {
	Action   Action    `json:"action"`
	Approved bool      `json:"approved"`
	By       string    `json:"by"`
	At       time.Time `json:"at"`
	Note     string    `json:"note,omitempty"`
}

// Approved builds an affirmative decision.
func Approved(action Action, by string, at time.Time) Decision {
	return Decision{Action: action, Approved: true, By: by, At: at}
}

// Rejected builds a negative decision.
func Rejected(action Action, by string, at time.Time, note string) Decision {
	return Decision{Action: action, Approved: false, By: by, At: at, Note: note}
}

// Authorizes reports whether this decision permits the action.
//
// It fails closed on every count: a decision that was never given, a rejection,
// an approval for another action, and an approval nobody is willing to put a
// name and a moment to.
func (d Decision) Authorizes(action Action) bool {
	return d.Approved && d.Action == action && strings.TrimSpace(d.By) != "" && !d.At.IsZero()
}

// Gate asks a human whether an action may proceed.
type Gate interface {
	Review(ctx context.Context, req Request) (Decision, error)
}

// DenyAll approves nothing. It is the default gate: a caller that has not wired
// a reviewer gets refusals rather than silence.
type DenyAll struct{}

// Review implements Gate.
func (DenyAll) Review(_ context.Context, req Request) (Decision, error) {
	return Rejected(req.Action, "", time.Now(), "no reviewer is configured"), nil
}

// Terminal asks the reviewer at the command line.
//
// Anything that is not an explicit "yes" is a refusal, including an empty line
// and an answer that merely sounds positive.
type Terminal struct {
	reader *bufio.Reader
	out    io.Writer
	by     string
	now    func() time.Time
}

// NewTerminal builds a command-line gate for one reviewer.
func NewTerminal(in io.Reader, out io.Writer, by string) *Terminal {
	return &Terminal{reader: bufio.NewReader(in), out: out, by: by, now: time.Now}
}

// Review implements Gate.
func (t *Terminal) Review(_ context.Context, req Request) (Decision, error) {
	if strings.TrimSpace(t.by) == "" {
		return Rejected(req.Action, "", t.now(), "no reviewer identity is configured"), nil
	}
	fmt.Fprintf(t.out, "%s\n", req.Summary)
	for _, detail := range req.Details {
		fmt.Fprintf(t.out, "  - %s\n", detail)
	}
	fmt.Fprint(t.out, "Type \"yes\" to approve this action, anything else to refuse: ")

	line, err := t.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return Decision{}, fmt.Errorf("review: read the answer: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(line), "yes") {
		return Approved(req.Action, t.by, t.now()), nil
	}
	return Rejected(req.Action, t.by, t.now(), "the reviewer did not answer yes"), nil
}
