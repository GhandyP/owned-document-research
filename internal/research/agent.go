package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"
)

// DefaultModel is the Gemini model the workflow runs on unless another is named.
const DefaultModel = "gemini-flash-latest"

// DefaultAppName names the ADK application the two agents belong to.
const DefaultAppName = "owned_document_research"

const extractionInstruction = `You extract candidate evidence for a research question.

You are given the question and the passages that retrieval found for it. Each passage carries the document reference it came from.

Return only candidates that speak to the question, and copy every quote character for character from a passage you were given. Set polarity to "contradicts" when the quote argues against the answer the question implies, and to "supports" otherwise.

Never invent a reference, never edit a quote, and return an empty candidate list when the passages do not answer the question. If they are not enough, use the search_owned_documents tool to look for more and quote only from what it returns.`

const synthesisInstruction = `You turn checked evidence into claims.

You are given the question and the quotes that have already been verified against the documents they came from.

Return one finding per statement, naming the indexes of the candidates it rests on. Set confidence to "high", "medium" or "low" from the strength of those quotes and the agreement between them. If a statement rests on no candidate, still return it with an empty index list: the caller reports it as unsupported rather than dropping it.

Never write a quote of your own, never cite an index that was not given, and never settle a disagreement between quotes. Report it.`

// AgentConfig configures the two-stage workflow.
type AgentConfig struct {
	// Model is the language model both agents share.
	Model model.LLM
	// Engine is the retrieval engine the search tool belongs to.
	Engine *Engine
	// Principal is the identity every tool call runs as. It is bound here, once,
	// and never taken from the model.
	Principal Principal
	// AppName, UserID and SessionID default to sensible values.
	AppName   string
	UserID    string
	SessionID string
}

// Workflow is the ADK half of the package: an extraction agent that may search
// the corpus, and a synthesis agent that may not.
//
// Both stages run through one in-memory session service and one session id, so
// the second stage sees what the first was told. The workflow only proposes:
// everything it returns still has to survive the engine's validation, which is
// why a model that fabricates a quote, a reference or an index cannot put it in
// an answer.
type Workflow struct {
	extraction *runner.Runner
	synthesis  *runner.Runner
	userID     string
	sessionID  string
}

// NewWorkflow builds both agents and their runners.
func NewWorkflow(ctx context.Context, cfg AgentConfig) (*Workflow, error) {
	if cfg.Model == nil {
		return nil, errors.New("research: a model is required")
	}
	if cfg.Engine == nil {
		return nil, errors.New("research: an engine is required")
	}

	appName := cfg.AppName
	if appName == "" {
		appName = DefaultAppName
	}
	userID := cfg.UserID
	if userID == "" {
		userID = cfg.Principal.ID
	}
	sessionID := cfg.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("research-%d", time.Now().UnixNano())
	}

	search, err := NewSearchTool(cfg.Engine, cfg.Principal)
	if err != nil {
		return nil, fmt.Errorf("research: search tool: %w", err)
	}

	// Mode is deliberately left at its default. ADK also offers a task mode
	// that collects the schema through an injected finish_task tool, but that
	// path is driven by a workflow node, not by runner.Run: with task mode set,
	// a plain runner run yields no model call at all (verified against
	// adk/v2 v2.2.0). In chat mode the framework puts the agent's OutputSchema
	// on the request as a native response schema, so the model answers with
	// JSON and the runner hands it back as structured output.
	extractionAgent, err := llmagent.New(llmagent.Config{
		Name:         "evidence_extraction",
		Description:  "Finds quoted evidence for a research question in the documents the caller may read.",
		Model:        cfg.Model,
		Instruction:  extractionInstruction,
		Tools:        []tool.Tool{search},
		OutputSchema: candidatesSchema(),
	})
	if err != nil {
		return nil, fmt.Errorf("research: extraction agent: %w", err)
	}

	synthesisAgent, err := llmagent.New(llmagent.Config{
		Name:         "evidence_synthesis",
		Description:  "Turns verified quotes into claims, and reports disagreement instead of settling it.",
		Model:        cfg.Model,
		Instruction:  synthesisInstruction,
		OutputSchema: findingsSchema(),
	})
	if err != nil {
		return nil, fmt.Errorf("research: synthesis agent: %w", err)
	}

	// One session service for both runners is what makes the two stages one
	// bounded research session instead of two unrelated conversations.
	sessions := session.InMemoryService()
	extractionRunner, err := runner.New(runner.Config{
		AppName:           appName,
		Agent:             extractionAgent,
		SessionService:    sessions,
		AutoCreateSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("research: extraction runner: %w", err)
	}
	synthesisRunner, err := runner.New(runner.Config{
		AppName:           appName,
		Agent:             synthesisAgent,
		SessionService:    sessions,
		AutoCreateSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("research: synthesis runner: %w", err)
	}

	return &Workflow{
		extraction: extractionRunner,
		synthesis:  synthesisRunner,
		userID:     userID,
		sessionID:  sessionID,
	}, nil
}

// SessionID is the session both stages write to.
func (w *Workflow) SessionID() string { return w.sessionID }

// Extract implements Extractor.
func (w *Workflow) Extract(ctx context.Context, question string, passages []Passage) ([]Candidate, error) {
	output, err := w.run(ctx, w.extraction, extractionPrompt(question, passages))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Candidates []Candidate `json:"candidates"`
	}
	if err := decodeOutput(output, &parsed); err != nil {
		return nil, err
	}
	return parsed.Candidates, nil
}

// Synthesize implements Synthesizer.
func (w *Workflow) Synthesize(ctx context.Context, question string, candidates []Candidate) ([]Finding, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	output, err := w.run(ctx, w.synthesis, synthesisPrompt(question, candidates))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Findings []Finding `json:"findings"`
	}
	if err := decodeOutput(output, &parsed); err != nil {
		return nil, err
	}
	return parsed.Findings, nil
}

// run sends one turn to one agent and returns its structured output.
//
// The framework surfaces a parsed value in event.Output only on the workflow
// node path. On the runner path the model's schema-constrained JSON arrives as
// the final text turn, so both shapes are accepted here and this package still
// parses the result through its own types.
func (w *Workflow) run(ctx context.Context, r *runner.Runner, prompt string) (any, error) {
	message := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: prompt}},
	}
	var output any
	var finalText string
	for event, err := range r.Run(ctx, w.userID, w.sessionID, message, agent.RunConfig{}) {
		if err != nil {
			return nil, fmt.Errorf("research: agent run: %w", err)
		}
		if event == nil {
			continue
		}
		if event.Output != nil {
			output = event.Output
		}
		if text, ok := finalModelText(event); ok {
			finalText = text
		}
	}
	switch {
	case output != nil:
		return output, nil
	case strings.TrimSpace(finalText) != "":
		return finalText, nil
	default:
		return nil, errors.New("research: the agent returned no structured output")
	}
}

// finalModelText returns the text of a final model turn: the plain, complete
// answer, as opposed to a partial event or a turn that asks to call a tool.
func finalModelText(event *session.Event) (string, bool) {
	if event.Partial || event.Content == nil || event.Content.Role != "model" {
		return "", false
	}
	var b strings.Builder
	for _, part := range event.Content.Parts {
		if part == nil || part.Thought {
			continue
		}
		if part.FunctionCall != nil {
			return "", false
		}
		b.WriteString(part.Text)
	}
	text := b.String()
	return text, strings.TrimSpace(text) != ""
}

// decodeOutput re-reads the agent's answer through this package's own types, so
// a reply that does not match the schema fails here rather than becoming half
// an answer.
func decodeOutput(output, target any) error {
	var raw []byte
	if text, isText := output.(string); isText {
		raw = []byte(text)
	} else {
		encoded, err := json.Marshal(output)
		if err != nil {
			return fmt.Errorf("research: the agent output could not be read: %w", err)
		}
		raw = encoded
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("research: the agent output does not match its schema: %w", err)
	}
	return nil
}

func extractionPrompt(question string, passages []Passage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\n\nPassages retrieval found:\n", question)
	for _, passage := range passages {
		fmt.Fprintf(&b, "- ref: %s | title: %s | owner: %s\n  text: %s\n", passage.Ref(), passage.Title, passage.Owner, passage.Text)
	}
	return b.String()
}

func synthesisPrompt(question string, candidates []Candidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\n\nVerified quotes:\n", question)
	for i, candidate := range candidates {
		fmt.Fprintf(&b, "[%d] ref: %s | polarity: %s | quote: %s\n", i, candidate.Ref, candidate.Polarity, candidate.Quote)
	}
	return b.String()
}

func candidatesSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"candidates": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"ref": {
							Type:        genai.TypeString,
							Description: "the document reference exactly as it was given, for example ops-handbook@3",
						},
						"quote": {
							Type:        genai.TypeString,
							Description: "a passage copied character for character from the document version named by ref",
						},
						"polarity": {
							Type:        genai.TypeString,
							Enum:        []string{string(PolaritySupports), string(PolarityContradicts)},
							Description: "whether the quote supports or contradicts the answer the question implies",
						},
					},
					Required: []string{"ref", "quote", "polarity"},
				},
			},
		},
		Required: []string{"candidates"},
	}
}

func findingsSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"findings": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"statement": {
							Type:        genai.TypeString,
							Description: "one statement answering the question",
						},
						"candidate_indexes": {
							Type:        genai.TypeArray,
							Items:       &genai.Schema{Type: genai.TypeInteger},
							Description: "the indexes of the quotes the statement rests on, from the list you were given",
						},
						"confidence": {
							Type:        genai.TypeString,
							Enum:        []string{string(ConfidenceHigh), string(ConfidenceMedium), string(ConfidenceLow)},
							Description: "how strong the statement is, given the quotes behind it",
						},
					},
					Required: []string{"statement", "candidate_indexes", "confidence"},
				},
			},
		},
		Required: []string{"findings"},
	}
}

// NewGeminiModel builds the Gemini model the workflow runs on.
//
// The key is supplied by the caller: nothing in this package reads the
// environment, so a credential can never leak into a test or a fixture.
func NewGeminiModel(ctx context.Context, name string, cfg *genai.ClientConfig) (model.LLM, error) {
	if name == "" {
		name = DefaultModel
	}
	if cfg == nil {
		cfg = &genai.ClientConfig{}
	}
	return gemini.NewModel(ctx, name, cfg)
}
