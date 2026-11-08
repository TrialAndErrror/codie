package tui

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// Researcher is the seam between the UI and whatever actually answers
// questions (an LLM, web search, local docs...). The TUI only knows this.
type Researcher interface {
	Research(ctx context.Context, question string) (string, error)
}

// FakeResearcher simulates a slow backend so the UI can be tried without one.
// It ignores the question and cycles through the canned snippets in order,
// wrapping around. A question containing "fail" returns an error instead
// (without advancing the cycle) so the error path can be exercised.
type FakeResearcher struct {
	Delay time.Duration
	next  atomic.Uint64
}

func (f *FakeResearcher) Research(ctx context.Context, question string) (string, error) {
	select {
	case <-time.After(f.Delay):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if strings.Contains(question, "fail") {
		return "", fmt.Errorf("research backend unavailable")
	}
	i := f.next.Add(1) - 1
	return snippets[i%uint64(len(snippets))].Code, nil
}
