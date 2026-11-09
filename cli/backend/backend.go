// Package backend talks to the AI. It knows nothing about the UI: the TUI asks
// a Researcher a question and reads a stream of Events back.
package backend

import (
	"context"
	"errors"
	"strings"
)

// Kind says what an Event carries.
type Kind int

const (
	// Text is the next chunk of the answer.
	Text Kind = iota
	// Activity is a status line for the user, such as "reading main.go".
	// (Nothing emits these until file tools arrive; the UI already shows them.)
	Activity
	// Done means the answer finished normally. It is always the last event.
	Done
	// Error means the answer failed. It is always the last event. Any Text
	// already delivered is a partial answer and must not be treated as complete.
	Error
)

// Event is one item in the stream a Researcher produces.
type Event struct {
	Kind Kind
	Text string // for Text and Activity
	Err  error  // for Error
}

// Researcher answers a question as a stream.
//
// Contract:
//   - zero or more Text/Activity events, then exactly one Done or Error, then
//     the channel is closed;
//   - if ctx is cancelled the implementation stops promptly and closes the
//     channel, possibly without a terminal event;
//   - implementations must never block forever on a consumer that has gone
//     away (they select on ctx.Done when sending).
type Researcher interface {
	Research(ctx context.Context, question string) <-chan Event
}

// Collect drains a stream and returns the full text. It is a convenience for
// callers (and tests) that don't need incremental output.
func Collect(ch <-chan Event) (string, error) {
	var b strings.Builder
	for ev := range ch {
		switch ev.Kind {
		case Text:
			b.WriteString(ev.Text)
		case Done:
			return b.String(), nil
		case Error:
			return b.String(), ev.Err
		}
	}
	return b.String(), errors.New("stream closed without a result")
}
