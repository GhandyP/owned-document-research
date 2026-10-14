package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/GhandyP/owned-document-research/internal/research"
)

// fixtureExpectation is what a request in testdata/requests.json must produce.
type fixtureExpectation struct {
	Claims          int      `json:"claims"`
	SupportedClaims int      `json:"supported_claims"`
	MinEvidence     int      `json:"min_evidence"`
	ConflictKinds   []string `json:"conflict_kinds"`
	GapKinds        []string `json:"gap_kinds"`
	Confidence      string   `json:"confidence"`
}

type requestFixture struct {
	Name         string             `json:"name"`
	Question     string             `json:"question"`
	DocumentRefs []string           `json:"document_refs"`
	Expect       fixtureExpectation `json:"expect"`
}

func loadFixtures(t *testing.T) []requestFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/requests.json")
	if err != nil {
		t.Fatalf("read the fixtures: %v", err)
	}
	var parsed struct {
		Requests []requestFixture `json:"requests"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse the fixtures: %v", err)
	}
	if len(parsed.Requests) == 0 {
		t.Fatal("the fixture file holds no request")
	}
	return parsed.Requests
}

// runCLI drives the command line and returns its exit code and streams.
func runCLI(args ...string) (int, string, string) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := run(args, stdout, stderr)
	return code, stdout.String(), stderr.String()
}

func TestEveryFixtureProducesTheAnswerItPromises(t *testing.T) {
	for _, fixture := range loadFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			args := []string{"research", "-question", fixture.Question, "-json"}
			if len(fixture.DocumentRefs) > 0 {
				args = append(args, "-docs", strings.Join(fixture.DocumentRefs, ","))
			}
			code, stdout, stderr := runCLI(args...)
			if code != exitOK {
				t.Fatalf("exit %d, stderr: %s", code, stderr)
			}

			var answer research.ResearchAnswer
			if err := json.Unmarshal([]byte(stdout), &answer); err != nil {
				t.Fatalf("-json printed something that is not the answer: %v\n%s", err, stdout)
			}
			if answer.Question != fixture.Question {
				t.Errorf("answer question = %q, want %q", answer.Question, fixture.Question)
			}
			if !answer.Advisory {
				t.Error("the answer is not advisory")
			}
			if len(answer.Claims) != fixture.Expect.Claims {
				t.Errorf("claims = %d, want %d: %+v", len(answer.Claims), fixture.Expect.Claims, answer.Claims)
			}

			supported, evidence := 0, 0
			for _, claim := range answer.Claims {
				if claim.Supported {
					supported++
				}
				evidence += len(claim.Evidence)
			}
			if supported != fixture.Expect.SupportedClaims {
				t.Errorf("supported claims = %d, want %d", supported, fixture.Expect.SupportedClaims)
			}
			if evidence < fixture.Expect.MinEvidence {
				t.Errorf("evidence entries = %d, want at least %d", evidence, fixture.Expect.MinEvidence)
			}

			if got, want := kindList(conflictKinds(answer)), kindList(fixture.Expect.ConflictKinds); got != want {
				t.Errorf("conflict kinds = %q, want %q: %+v", got, want, answer.Conflicts)
			}
			if got, want := kindList(gapKinds(answer)), kindList(fixture.Expect.GapKinds); got != want {
				t.Errorf("gap kinds = %q, want %q: %+v", got, want, answer.Gaps)
			}
			if string(answer.Confidence) != fixture.Expect.Confidence {
				t.Errorf("confidence = %q, want %q", answer.Confidence, fixture.Expect.Confidence)
			}

			// An answer is only worth reading if its quotes are in the table.
			if len(answer.EvidenceTable) != evidence {
				t.Errorf("the evidence table has %d rows for %d quotes", len(answer.EvidenceTable), evidence)
			}
			for _, claim := range answer.Claims {
				if !claim.Supported {
					continue
				}
				if len(claim.Evidence) == 0 {
					t.Errorf("a supported claim carries no evidence: %+v", claim)
				}
				for _, item := range claim.Evidence {
					if strings.TrimSpace(item.Quote) == "" {
						t.Errorf("a supported claim carries an empty quote: %+v", item)
					}
				}
			}
		})
	}
}

func conflictKinds(answer research.ResearchAnswer) []string {
	kinds := make([]string, 0, len(answer.Conflicts))
	for _, conflict := range answer.Conflicts {
		kinds = append(kinds, string(conflict.Kind))
	}
	return kinds
}

func gapKinds(answer research.ResearchAnswer) []string {
	kinds := make([]string, 0, len(answer.Gaps))
	for _, gap := range answer.Gaps {
		kinds = append(kinds, string(gap.Kind))
	}
	return kinds
}

// kindList sorts and de-duplicates a list of kinds, so a fixture states which
// kinds must appear without depending on their order.
func kindList(kinds []string) string {
	unique := make(map[string]bool, len(kinds))
	list := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		if unique[kind] {
			continue
		}
		unique[kind] = true
		list = append(list, kind)
	}
	sort.Strings(list)
	return strings.Join(list, ",")
}

func TestResearchNeverPrintsADocumentThePrincipalMayNotRead(t *testing.T) {
	auditText := "The audit found no evidence that handling time fell"

	// The restricted document is named outright.
	code, stdout, _ := runCLI("research", "-question", demoQuestion, "-docs", "audit-2026", "-json")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(stdout, auditText) {
		t.Fatalf("a restricted document was printed:\n%s", stdout)
	}
	var answer research.ResearchAnswer
	if err := json.Unmarshal([]byte(stdout), &answer); err != nil {
		t.Fatalf("parse the answer: %v", err)
	}
	if len(answer.Gaps) == 0 || answer.Gaps[0].Kind != research.GapUnavailable {
		t.Errorf("a restricted reference produced %+v, want an unavailable gap", answer.Gaps)
	}

	// And it is not reachable by asking about it.
	code, stdout, _ = runCLI("research", "-question", "What did the audit find about handling time?", "-json")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(stdout, auditText) {
		t.Fatalf("a restricted document was printed:\n%s", stdout)
	}
}

func TestUsageErrorsAreReportedAsUsageErrors(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		after string
	}{
		{name: "no command at all", args: nil},
		{name: "an unknown command", args: []string{"researchh"}},
		{name: "research without a question", args: []string{"research"}, after: "-question is required"},
		{name: "export without a target", args: []string{"export", "-question", "x"}, after: "-out is required"},
		{name: "export approved by nobody", args: []string{"export", "-question", "x", "-out", "y.md", "-approve"}, after: "-reviewer"},
		{name: "a flag that does not exist", args: []string{"research", "-nope"}, after: "flag provided"},
		{name: "an argument nobody reads", args: []string{"research", "-question", "x", "extra"}, after: "unexpected argument"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(tt.args...)
			if code != exitUsage {
				t.Errorf("exit = %d, want %d", code, exitUsage)
			}
			if !strings.Contains(stderr, "Usage:") {
				t.Errorf("the usage text was not printed:\n%s", stderr)
			}
			if tt.after != "" && !strings.Contains(stderr, tt.after) {
				t.Errorf("stderr does not explain the problem (%q):\n%s", tt.after, stderr)
			}
			if stdout != "" {
				t.Errorf("a usage error wrote to stdout: %q", stdout)
			}
		})
	}
}

func TestHelpIsNotAUsageError(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}} {
		code, stdout, _ := runCLI(args...)
		if code != exitOK {
			t.Errorf("%v exited %d, want %d", args, code, exitOK)
		}
		if !strings.Contains(stdout, "Usage:") {
			t.Errorf("%v printed no usage text", args)
		}
	}
}

func TestExportWritesOnlyWhenAReviewerApproves(t *testing.T) {
	dir := t.TempDir()
	markdown := filepath.Join(dir, "answer.md")

	code, stdout, stderr := runCLI("export", "-question", demoQuestion, "-out", markdown)
	if code != exitFailure {
		t.Fatalf("an unapproved export exited %d, want %d (stdout %q)", code, exitFailure, stdout)
	}
	if !strings.Contains(stderr, "not approved") {
		t.Errorf("stderr does not explain the refusal: %s", stderr)
	}
	if _, err := os.Stat(markdown); !os.IsNotExist(err) {
		t.Errorf("the refusal wrote %s anyway", markdown)
	}
	if _, err := os.Stat(markdown + ".json"); !os.IsNotExist(err) {
		t.Errorf("the refusal wrote a sidecar anyway")
	}

	code, stdout, stderr = runCLI("export",
		"-question", demoQuestion,
		"-out", markdown,
		"-approve", "-reviewer", "ana@acme.test",
	)
	if code != exitOK {
		t.Fatalf("an approved export exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "approved by ana@acme.test") {
		t.Errorf("the export did not report its reviewer: %s", stdout)
	}
	written, err := os.ReadFile(markdown)
	if err != nil {
		t.Fatalf("read the export: %v", err)
	}
	if !strings.Contains(string(written), "**Reviewed by:** ana@acme.test") {
		t.Errorf("the export does not carry the review record:\n%s", written)
	}
	if !strings.Contains(string(written), "Conflicts") {
		t.Errorf("the demo question has conflicts to show, and the export hid them:\n%s", written)
	}
}

func TestVerifyReportsWhatIsMissing(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	code, stdout, stderr := runCLI("verify")
	if code != exitFailure {
		t.Errorf("verify without a key exited %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stdout, "offline:     ready") {
		t.Errorf("verify does not report the offline path as ready:\n%s", stdout)
	}
	if !strings.Contains(stderr, "GOOGLE_API_KEY") {
		t.Errorf("stderr does not name the missing variable: %s", stderr)
	}

	t.Setenv("GOOGLE_API_KEY", "not-a-real-key")
	code, stdout, _ = runCLI("verify")
	if code != exitOK {
		t.Errorf("verify with a key present exited %d, want %d:\n%s", code, exitOK, stdout)
	}
	if !strings.Contains(stdout, "GOOGLE_API_KEY: present") {
		t.Errorf("verify does not report the key as present:\n%s", stdout)
	}
}

func TestTheAgentRouteFailsClosedWithoutAKey(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	code, stdout, stderr := runCLI("research", "-question", demoQuestion, "-agent")
	if code != exitFailure {
		t.Errorf("the agent route without a key exited %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr, "GOOGLE_API_KEY") {
		t.Errorf("the refusal does not name the missing variable: %s", stderr)
	}
	if stdout != "" {
		t.Errorf("the refusal wrote to stdout: %q", stdout)
	}
}

func TestACLListsOnlyWhatTheIdentityMayRead(t *testing.T) {
	code, stdout, stderr := runCLI("acl")
	if code != exitOK {
		t.Fatalf("acl exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "pilot-notes@2") {
		t.Errorf("the owner cannot see the current pilot notes:\n%s", stdout)
	}
	if !strings.Contains(stdout, "pilot-notes@1") || !strings.Contains(stdout, "superseded") {
		t.Errorf("the superseded version is not marked:\n%s", stdout)
	}
	if strings.Contains(stdout, "audit-2026") {
		t.Errorf("a legal-only document is listed to the owner:\n%s", stdout)
	}
	if strings.Contains(stdout, "globex-benchmark") {
		t.Errorf("another tenant's document is listed:\n%s", stdout)
	}

	code, stdout, _ = runCLI("acl", "-principal", "eve@globex.test", "-tenant", "drive-globex", "-groups", "")
	if code != exitOK {
		t.Fatalf("acl for another tenant exited %d", code)
	}
	if !strings.Contains(stdout, "globex-benchmark@1") || strings.Contains(stdout, "pilot-notes") {
		t.Errorf("another tenant sees the wrong documents:\n%s", stdout)
	}
}

func TestDemoRunsOfflineAndShowsTheDisagreement(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	code, stdout, stderr := runCLI("demo")
	if code != exitOK {
		t.Fatalf("demo exited %d: %s", code, stderr)
	}
	for _, want := range []string{
		demoQuestion,
		"advisory",
		"## Conflicts",
		"polarity",
		"superseded_version",
		"## Gaps",
		"no_match",
		"## Evidence table",
		"pilot-notes@2",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the demo does not show %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "The audit found no evidence") {
		t.Errorf("the demo printed a restricted document:\n%s", stdout)
	}
}

func TestTheBuiltBinaryRunsTheDemo(t *testing.T) {
	if testing.Short() {
		t.Skip("building the binary is skipped in short mode")
	}
	binary := filepath.Join(t.TempDir(), "owned-document-research")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}

	command := exec.Command(binary, "demo")
	stdout, err := command.Output()
	if err != nil {
		t.Fatalf("the built binary failed: %v (%s)", err, stdout)
	}
	if !strings.Contains(string(stdout), "Research answer") {
		t.Errorf("the built binary printed:\n%s", stdout)
	}

	exit := exec.Command(binary, "research")
	err = exit.Run()
	if err == nil {
		t.Fatal("the built binary accepted a research call with no question")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != exitUsage {
		t.Fatalf("the built binary exited %v, want %d", err, exitUsage)
	}
}
