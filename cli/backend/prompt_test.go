package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The UI parses fenced code blocks out of replies, so the prompt must keep
// asking for them. These guard the contract against accidental edits; they
// can't prove a model obeys it (only trying real models can).
func TestDefaultPromptAsksForTheFormatTheUIParses(t *testing.T) {
	p := DefaultSystemPrompt
	for name, want := range map[string]string{
		"fenced blocks":              "fenced code block",
		"one block when simple":      "exactly one fenced code block",
		"several blocks when needed": "several fenced code blocks",
		"which file each belongs in": "which file it belongs in",
		"how blocks relate":          "relates to the other blocks",
		"language on the fence":      "```go",
		"no nesting":                 "Never nest fences",
		"documented code":            "comment",
		"simple":                     "simple",
		"no markdown prose":          "no headings, bold text, bullet lists or tables",
		"not an agent":               "not an agent",
		"no questions back":          "instead of asking questions",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt no longer says %s (looking for %q)", name, want)
		}
	}
}

func TestDefaultPromptIsSentAsTheSystemMessage(t *testing.T) {
	var got chatRequest
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		sse(w, delta("x"), "[DONE]")
	})
	o := client(t, srv) // no System override
	drain(t, o.Research(context.Background(), "q"))
	if len(got.Messages) != 2 || got.Messages[0].Role != "system" || got.Messages[0].Content != DefaultSystemPrompt {
		t.Fatalf("the default prompt was not sent as the system message: %+v", got.Messages)
	}
}

func TestPromptDoesNotItselfContainAStrayFence(t *testing.T) {
	// The only fence in the prompt should be the one in the "```go" example; a
	// stray one could make some models treat the prompt as a code block.
	if n := strings.Count(DefaultSystemPrompt, "```"); n != 1 {
		t.Fatalf("expected exactly one fence mention, found %d", n)
	}
}
