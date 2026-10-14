package research

import (
	"context"
	"iter"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// stubModel stands in for Gemini: it answers with the canned JSON a model
// behind a native response schema returns, so the whole ADK pipeline can run
// offline, and it records what it was asked.
type stubModel struct {
	mu        sync.Mutex
	responses []string
	requests  []*model.LLMRequest
}

func (m *stubModel) Name() string { return "stub-model" }

func (m *stubModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.mu.Lock()
		m.requests = append(m.requests, req)
		text := ""
		if len(m.responses) > 0 {
			text = m.responses[0]
			m.responses = m.responses[1:]
		}
		m.mu.Unlock()

		yield(&model.LLMResponse{Content: &genai.Content{
			Role:  "model",
			Parts: []*genai.Part{{Text: text}},
		}}, nil)
	}
}

func (m *stubModel) recorded() []*model.LLMRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*model.LLMRequest{}, m.requests...)
}

func toolNames(req *model.LLMRequest) []string {
	names := make([]string, 0, len(req.Tools))
	for name := range req.Tools {
		names = append(names, name)
	}
	return names
}

func requestText(req *model.LLMRequest) string {
	var b strings.Builder
	for _, content := range req.Contents {
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if part != nil {
				b.WriteString(part.Text)
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

var (
	extractionReply = `{"candidates":[` +
		`{"ref":"pilot-notes@2","quote":"` + quoteCurrent + `","polarity":"supports"},` +
		`{"ref":"pilot-notes@2","quote":"the pilot cut handling time","polarity":"supports"}]}`
	synthesisReply = `{"findings":[{"statement":"The pilot reduced handling time.",` +
		`"candidate_indexes":[0,1],"confidence":"high"}]}`
)

func TestWorkflowRunsBothStagesInOneSession(t *testing.T) {
	stub := &stubModel{responses: []string{extractionReply, synthesisReply}}
	source := testCorpus(t)

	workflow, err := NewWorkflow(context.Background(), AgentConfig{
		Model:     stub,
		Engine:    New(source, nil, nil),
		Principal: bob,
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}

	engine := New(source, workflow, workflow)
	answer, err := engine.Research(context.Background(), bob, ResearchRequest{Question: question})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}

	if len(answer.Claims) != 1 {
		t.Fatalf("Research returned %d claims, want 1: %+v", len(answer.Claims), answer.Claims)
	}
	claim := answer.Claims[0]
	if !claim.Supported {
		t.Errorf("the verbatim quote did not survive: %+v", claim)
	}
	if len(claim.Evidence) != 1 {
		t.Errorf("claim cites %d quotes, want only the verbatim one: %+v", len(claim.Evidence), claim.Evidence)
	}
	if len(answer.Warnings) != 1 || !strings.Contains(answer.Warnings[0], "does not appear") {
		t.Errorf("the fabricated quote was not reported: %v", answer.Warnings)
	}

	requests := stub.recorded()
	if len(requests) != 2 {
		t.Fatalf("the model was called %d times, want one extraction and one synthesis", len(requests))
	}
	extractionTools := strings.Join(toolNames(requests[0]), ",")
	if !strings.Contains(extractionTools, ToolSearchOwnedDocuments) {
		t.Errorf("the extraction agent holds %q, so it cannot search the corpus", extractionTools)
	}
	if synthesisTools := strings.Join(toolNames(requests[1]), ","); strings.Contains(synthesisTools, ToolSearchOwnedDocuments) {
		t.Errorf("the synthesis agent holds %q, so it can pull in unvalidated documents", synthesisTools)
	}

	// Both stages share one session: the synthesis request carries the
	// extraction turn, so the second stage can see what the first was told.
	synthesisRequest := requestText(requests[1])
	if !strings.Contains(synthesisRequest, question) {
		t.Errorf("the synthesis request does not carry the question:\n%s", synthesisRequest)
	}
	if !strings.Contains(synthesisRequest, quoteCurrent) {
		t.Errorf("the synthesis request does not carry the extracted candidate:\n%s", synthesisRequest)
	}
	if len(requests[1].Contents) < 3 {
		t.Errorf("the synthesis request has %d contents, want the extraction turn and its own prompt", len(requests[1].Contents))
	}
}

func TestWorkflowRejectsAModelThatIgnoresTheSchema(t *testing.T) {
	stub := &stubModel{responses: []string{"I could not find anything.", synthesisReply}}
	workflow, err := NewWorkflow(context.Background(), AgentConfig{
		Model:     stub,
		Engine:    New(testCorpus(t), nil, nil),
		Principal: bob,
		SessionID: "broken-schema",
	})
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	engine := New(testCorpus(t), workflow, workflow)
	if _, err := engine.Research(context.Background(), bob, ResearchRequest{Question: question}); err == nil {
		t.Fatal("Research accepted a reply that does not match the output schema")
	}
}
