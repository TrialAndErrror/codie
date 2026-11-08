package tui

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"codie/backend"
)

// FakeResearcher is a backend.Researcher that needs no network or key. It
// ignores the question and streams the canned snippets one line at a time,
// cycling in order and wrapping around. It exists so the UI (and its tests)
// can be exercised without an AI behind it.
//
// Special questions exercise the failure paths:
//   - containing "midfail": streams about half of a snippet, then errors
//   - containing "fail":    errors before sending anything
//
// Failed requests don't consume a snippet.
type FakeResearcher struct {
	Delay      time.Duration // wait before the first chunk
	ChunkDelay time.Duration // wait between chunks
	next       atomic.Uint64
}

func (f *FakeResearcher) Research(ctx context.Context, question string) <-chan backend.Event {
	out := make(chan backend.Event)
	idx := f.next.Load()
	answer := snippets[idx%uint64(len(snippets))].Answer
	lines := strings.SplitAfter(answer, "\n")

	go func() {
		defer close(out)
		send := func(e backend.Event) bool {
			select {
			case out <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}
		wait := func(d time.Duration) bool {
			select {
			case <-time.After(d):
				return true
			case <-ctx.Done():
				return false
			}
		}

		if !wait(f.Delay) {
			return
		}
		midfail := strings.Contains(question, "midfail")
		if !midfail && strings.Contains(question, "fail") {
			send(backend.Event{Kind: backend.Error, Err: fmt.Errorf("research backend unavailable")})
			return
		}
		for i, line := range lines {
			if midfail && i == len(lines)/2 {
				send(backend.Event{Kind: backend.Error, Err: fmt.Errorf("connection to the research backend was lost")})
				return
			}
			if line == "" {
				continue
			}
			if !send(backend.Event{Kind: backend.Text, Text: line}) || !wait(f.ChunkDelay) {
				return
			}
		}
		f.next.Add(1)
		send(backend.Event{Kind: backend.Done})
	}()
	return out
}
