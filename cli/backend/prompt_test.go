package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The UI parses fenced code blocks out of replies and scrolls each step into
// view with the sentences before it, so the prompt must keep asking for a
// walkthrough: words, block, words, block. These guard that contract against
// accidental edits; they cannot prove a model obeys it (only trying real models
// can).
func TestDefaultPromptAsksForAStepByStepWalkthrough(t *testing.T) {
	p := DefaultSystemPrompt
	for name, want := range map[string]string{
		"step by step":                  "step by step",
		"opens with words":              "Start with two or three plain sentences",
		"alternates words and blocks":   "alternate: one fenced code block, then two or three plain sentences, then the next fenced code block",
		"each block is one step":        "Each block is one step of the solution",
		"sentences lead into next step": "lead into the next step",
		"ends on a code block":          "End with a code block",
		"no closing summary":            "closing summary",
		"short blocks":                  "5 to 25 lines",
		"split long code into steps":    "split anything longer into steps",
		"still introduced when small":   "it still gets its introduction",
		"which file a step belongs in":  "which file it belongs in",
		"how blocks relate":             "relates to the other blocks",
		"language on the fence":         "```go",
		"no nesting":                    "Never nest fences",
		"plain prose only":              "No headings, bold text, bullet lists, numbered lists or tables",
		"2-3 sentences, explain why":    "Explain why as well as what",
		"sentences carry explanation":   "Let the sentences carry the explanation",
		"comments stay short":           "comments inside the code short",
		"simple code":                   "as simple as it can be",
		"not an agent":                  "not an agent",
		"no questions back":             "instead of asking questions",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt no longer says %s (looking for %q)", name, want)
		}
	}
}

// The previous prompt asked for code only, in a single block when "simple". Those
// instructions contradict a walkthrough, so they must not creep back in.
func TestDefaultPromptNoLongerAsksForCodeOnly(t *testing.T) {
	p := strings.ToLower(DefaultSystemPrompt)
	for _, banned := range []string{
		"exactly one fenced code block",
		"and nothing else",
		"reply with code only",
		"write no text before the first block",
		"no summary, sign-off or apology after the last one",
		"put any explanation in code comments",
		"document it: put a short comment above each function",
	} {
		if strings.Contains(p, banned) {
			t.Errorf("prompt still contains the old code-only instruction %q", banned)
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
