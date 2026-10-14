package research

import (
	"context"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// Tool names exposed to the model.
const (
	// ToolSearchOwnedDocuments is the only tool the research agents hold. It is
	// read-only: nothing in this repository gives a model a way to write.
	ToolSearchOwnedDocuments = "search_owned_documents"
)

// maxToolPassages bounds what one tool call may return.
const maxToolPassages = 10

// SearchArgs is what the model asks for.
//
// It carries no principal and no permission field on purpose: the caller's
// identity is bound when the tool is built, so a model cannot widen its own
// access, and no prompt can talk it into another tenant's documents.
type SearchArgs struct {
	Query string `json:"query" jsonschema:"the words to look for, in the language of the documents"`
	Limit int    `json:"limit,omitempty" jsonschema:"how many passages to return at most"`
}

// SearchResult is what the model gets back: quoted passages with the document
// reference and version they came from, or a note explaining an empty result.
type SearchResult struct {
	Passages []Passage `json:"passages"`
	Note     string    `json:"note,omitempty"`
}

// searchHandler is the tool body, kept separate from the ADK wrapper so it can
// be exercised directly.
func searchHandler(engine *Engine, p Principal) func(agent.Context, SearchArgs) (SearchResult, error) {
	return func(ctx agent.Context, args SearchArgs) (SearchResult, error) {
		query := strings.TrimSpace(args.Query)
		if query == "" {
			return SearchResult{Note: "an empty query matches nothing"}, nil
		}
		limit := args.Limit
		if limit <= 0 || limit > maxToolPassages {
			limit = maxToolPassages
		}
		callCtx := context.Background()
		if ctx != nil {
			callCtx = ctx
		}
		passages, err := engine.Search(callCtx, p, query, limit)
		if err != nil {
			return SearchResult{}, err
		}
		if len(passages) == 0 {
			return SearchResult{Note: "no passage in the documents this caller may read matches the query"}, nil
		}
		return SearchResult{Passages: passages}, nil
	}
}

// NewSearchTool binds the read-only search tool to one principal.
func NewSearchTool(engine *Engine, p Principal) (tool.Tool, error) {
	return functiontool.New[SearchArgs, SearchResult](functiontool.Config{
		Name: ToolSearchOwnedDocuments,
		Description: "Search the documents this caller may read and return quoted passages. " +
			"Every passage carries the document reference and version it came from. " +
			"Quote only from the passages this tool returns; it never returns a document the caller may not read.",
	}, searchHandler(engine, p))
}
