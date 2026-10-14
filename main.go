// Command owned-document-research answers a research question from owned
// document excerpts while exposing evidence conflicts, missing information,
// and source ownership.
//
// The command line is the only place the two halves of the package meet: the
// deterministic rules and the ADK agents are both wired here, and the export
// is the only path that writes anything, behind its own approval.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/GhandyP/owned-document-research/adapters"
	"github.com/GhandyP/owned-document-research/internal/research"
	"github.com/GhandyP/owned-document-research/internal/review"
)

const usageText = `owned-document-research — research over documents you own

Usage:
  owned-document-research demo [flags]
  owned-document-research research -question TEXT [flags]
  owned-document-research acl [flags]
  owned-document-research verify [flags]
  owned-document-research export -question TEXT -out FILE [flags]

Flags shared by every command:
  -corpus PATH      corpus file to read (default "` + defaultCorpusPath + `")
  -principal ID     the identity the research runs as (default "ana@acme.test")
  -groups LIST      comma separated groups of that identity (default "ops")
  -tenant NAME      tenant of that identity (default "drive-acme")

research flags:
  -question TEXT    what to research (required, except for demo)
  -docs LIST        restrict the research to these references, as id or id@version
  -json             print the answer as JSON instead of Markdown
  -agent            use the ADK agents instead of the deterministic rules
  -model NAME       model for -agent (default ADK_MODEL or ` + research.DefaultModel + `)

export flags:
  -out PATH         where to write the reviewed answer (required)
  -approve          record a human approval for this export
  -reviewer NAME    who approves (required with -approve)

Exit codes: 0 success, 1 failure, 2 usage error.

The offline path needs no credential: demo and research quote the corpus with
rules. The agent path needs GOOGLE_API_KEY and is reported as unverified here,
because a key is not part of this repository.
`

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2

	// defaultCorpusPath is the corpus this repository ships.
	defaultCorpusPath = "testdata/corpus.json"

	// demoQuestion is what demo asks.
	demoQuestion = "Did the intake pilot reduce handling time?"
)

// demoRefs are the three documents the demo names: the ones that disagree and
// one that has nothing to say about the question, so the answer shows a
// conflict and a gap as well as its evidence table.
var demoRefs = []string{"pilot-notes", "ops-handbook", "canteen-memo"}

// usageError marks a problem with how the command was called.
type usageError struct{ message string }

func (e *usageError) Error() string { return e.message }

func usagef(format string, args ...any) error {
	return &usageError{message: fmt.Sprintf(format, args...)}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the whole command line, so a test can drive it without a process.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return exitUsage
	}

	var err error
	switch args[0] {
	case "demo":
		err = runDemo(args[1:], stdout, stderr)
	case "research":
		err = runResearch(args[1:], stdout, stderr)
	case "acl":
		err = runACL(args[1:], stdout, stderr)
	case "verify":
		err = runVerify(args[1:], stdout, stderr)
	case "export":
		err = runExport(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usageText)
		return exitUsage
	}

	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, flag.ErrHelp):
		return exitOK
	}
	var misuse *usageError
	if errors.As(err, &misuse) {
		fmt.Fprintf(stderr, "%s\n\n%s", misuse.Error(), usageText)
		return exitUsage
	}
	fmt.Fprintf(stderr, "error: %v\n", err)
	return exitFailure
}

// options are the flags every command shares.
type options struct {
	corpus    string
	principal string
	groups    string
	tenant    string
}

func (o *options) bind(fs *flag.FlagSet) {
	fs.StringVar(&o.corpus, "corpus", defaultCorpusPath, "corpus file to read")
	fs.StringVar(&o.principal, "principal", "ana@acme.test", "the identity the research runs as")
	fs.StringVar(&o.groups, "groups", "ops", "comma separated groups of that identity")
	fs.StringVar(&o.tenant, "tenant", "drive-acme", "tenant of that identity")
}

// identity is the principal every request in this run is made as.
func (o *options) identity() research.Principal {
	groups := make([]string, 0, 2)
	for _, group := range strings.Split(o.groups, ",") {
		if trimmed := strings.TrimSpace(group); trimmed != "" {
			groups = append(groups, trimmed)
		}
	}
	return research.Principal{
		ID:     strings.TrimSpace(o.principal),
		Groups: groups,
		Tenant: strings.TrimSpace(o.tenant),
	}
}

func (o *options) source() (*research.MemorySource, error) {
	source, err := adapters.LoadJSONCorpus(o.corpus)
	if err != nil {
		return nil, err
	}
	return source, nil
}

// parse reads a command's flags, turning anything that is not a help request
// into a usage error.
func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usagef("%s: %v", fs.Name(), err)
	}
	if fs.NArg() > 0 {
		return usagef("%s: unexpected argument %q", fs.Name(), fs.Arg(0))
	}
	return nil
}

func runDemo(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := &options{}
	opts.bind(fs)
	asJSON := fs.Bool("json", false, "print the answer as JSON")
	if err := parse(fs, args); err != nil {
		return err
	}

	source, err := opts.source()
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "demo: %q over %s as %s (offline rules, no model)\n", demoQuestion, opts.corpus, opts.identity().ID)
	fmt.Fprintf(stderr, "demo: naming %s\n", strings.Join(demoRefs, ", "))

	engine := research.New(source, research.RuleExtractor{}, research.RuleSynthesizer{})
	answer, err := engine.Research(context.Background(), opts.identity(), research.ResearchRequest{
		Question:     demoQuestion,
		DocumentRefs: demoRefs,
	})
	if err != nil {
		return err
	}
	return writeAnswer(stdout, answer, *asJSON)
}

func runResearch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("research", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := &options{}
	opts.bind(fs)
	question := fs.String("question", "", "what to research")
	docs := fs.String("docs", "", "comma separated document references")
	asJSON := fs.Bool("json", false, "print the answer as JSON")
	useAgent := fs.Bool("agent", false, "use the ADK agents instead of the deterministic rules")
	modelName := fs.String("model", "", "model for -agent")
	if err := parse(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*question) == "" {
		return usagef("research: -question is required")
	}

	source, err := opts.source()
	if err != nil {
		return err
	}
	engine, err := buildEngine(context.Background(), source, opts.identity(), *useAgent, *modelName, stderr)
	if err != nil {
		return err
	}
	answer, err := engine.Research(context.Background(), opts.identity(), research.ResearchRequest{
		Question:     *question,
		DocumentRefs: splitList(*docs),
	})
	if err != nil {
		return err
	}
	return writeAnswer(stdout, answer, *asJSON)
}

func runACL(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("acl", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := &options{}
	opts.bind(fs)
	if err := parse(fs, args); err != nil {
		return err
	}

	source, err := opts.source()
	if err != nil {
		return err
	}
	identity := opts.identity()
	metas, err := source.List(context.Background(), identity)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s (tenant %s, groups %s) may read %d document versions:\n",
		identity.ID, identity.Tenant, strings.Join(identity.Groups, ","), len(metas))
	for _, meta := range metas {
		version := fmt.Sprintf("%s@%d", meta.ID, meta.Version)
		state := "current"
		for _, other := range metas {
			if other.Supersedes == version {
				state = "superseded"
			}
		}
		fmt.Fprintf(stdout, "  %-24s %-30s owned by %-18s %s\n", version, meta.Title, meta.Owner, state)
	}
	if len(metas) == 0 {
		fmt.Fprintln(stdout, "  (nothing: this identity reads no document in this corpus)")
	}
	return nil
}

func runVerify(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := &options{}
	opts.bind(fs)
	if err := parse(fs, args); err != nil {
		return err
	}

	source, err := opts.source()
	if err != nil {
		return err
	}
	identity := opts.identity()
	metas, err := source.List(context.Background(), identity)
	if err != nil {
		return err
	}

	apiKey := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
	modelName := modelFromEnvironment()

	fmt.Fprintf(stdout, "corpus:      %s (%d document versions)\n", opts.corpus, len(source.Documents()))
	fmt.Fprintf(stdout, "principal:   %s (tenant %s, groups %s) reads %d of them\n",
		identity.ID, identity.Tenant, strings.Join(identity.Groups, ","), len(metas))
	fmt.Fprintf(stdout, "offline:     ready — demo and research quote the corpus with rules\n")
	fmt.Fprintf(stdout, "agent:       %s\n", research.DefaultAppName)
	fmt.Fprintf(stdout, "model:       %s (-agent, ADK_MODEL overrides it)\n", modelName)
	if apiKey == "" {
		fmt.Fprintf(stdout, "GOOGLE_API_KEY: missing\n")
		return errors.New("the agent route needs GOOGLE_API_KEY; the offline path does not")
	}
	fmt.Fprintf(stdout, "GOOGLE_API_KEY: present\n")
	return nil
}

func runExport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := &options{}
	opts.bind(fs)
	question := fs.String("question", "", "what to research")
	docs := fs.String("docs", "", "comma separated document references")
	out := fs.String("out", "", "where to write the reviewed answer")
	approve := fs.Bool("approve", false, "record a human approval for this export")
	reviewer := fs.String("reviewer", "", "who approves")
	useAgent := fs.Bool("agent", false, "use the ADK agents instead of the deterministic rules")
	modelName := fs.String("model", "", "model for -agent")
	if err := parse(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*question) == "" {
		return usagef("export: -question is required")
	}
	if strings.TrimSpace(*out) == "" {
		return usagef("export: -out is required")
	}

	source, err := opts.source()
	if err != nil {
		return err
	}
	engine, err := buildEngine(context.Background(), source, opts.identity(), *useAgent, *modelName, stderr)
	if err != nil {
		return err
	}
	answer, err := engine.Research(context.Background(), opts.identity(), research.ResearchRequest{
		Question:     *question,
		DocumentRefs: splitList(*docs),
	})
	if err != nil {
		return err
	}

	var decision review.Decision
	if *approve {
		if strings.TrimSpace(*reviewer) == "" {
			return usagef("export: -approve needs -reviewer; an approval nobody signed is not a review")
		}
		decision = review.Approved(review.ActionExport, strings.TrimSpace(*reviewer), time.Now())
	}
	if err := review.Export(*out, answer, decision); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s and %s (approved by %s)\n", *out, *out+".json", decision.By)
	fmt.Fprintf(stdout, "claims %d, conflicts %d, gaps %d, confidence %s\n",
		len(answer.Claims), len(answer.Conflicts), len(answer.Gaps), answer.Confidence)
	return nil
}

// buildEngine wires the engine to either the deterministic rules or the ADK
// agents. The agent route fails closed and says what is missing.
func buildEngine(ctx context.Context, source research.DocumentSource, identity research.Principal, useAgent bool, modelName string, stderr io.Writer) (*research.Engine, error) {
	if !useAgent {
		return research.New(source, research.RuleExtractor{}, research.RuleSynthesizer{}), nil
	}
	apiKey := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
	if apiKey == "" {
		return nil, errors.New("-agent needs GOOGLE_API_KEY; run without it to quote the corpus with rules")
	}
	if modelName == "" {
		modelName = modelFromEnvironment()
	}
	model, err := research.NewGeminiModel(ctx, modelName, &genai.ClientConfig{APIKey: apiKey})
	if err != nil {
		return nil, fmt.Errorf("build the model: %w", err)
	}
	workflow, err := research.NewWorkflow(ctx, research.AgentConfig{
		Model:     model,
		Engine:    research.New(source, nil, nil),
		Principal: identity,
	})
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(stderr, "agent: %s with %s in session %s\n", research.DefaultAppName, modelName, workflow.SessionID())
	return research.New(source, workflow, workflow), nil
}

// modelFromEnvironment reads the model name from ADK_MODEL.
func modelFromEnvironment() string {
	if name := strings.TrimSpace(os.Getenv("ADK_MODEL")); name != "" {
		return name
	}
	return research.DefaultModel
}

// writeAnswer prints the answer as JSON or as the Markdown an export writes.
func writeAnswer(stdout io.Writer, answer research.ResearchAnswer, asJSON bool) error {
	if asJSON {
		encoded, err := json.MarshalIndent(answer, "", "  ")
		if err != nil {
			return fmt.Errorf("encode the answer: %w", err)
		}
		_, err = fmt.Fprintln(stdout, string(encoded))
		return err
	}
	fmt.Fprint(stdout, review.RenderMarkdown(review.ExportRecord{Answer: answer}))
	return nil
}

// splitList reads a comma separated flag value.
func splitList(value string) []string {
	items := make([]string, 0, 4)
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	if len(items) == 0 {
		return nil
	}
	return items
}
