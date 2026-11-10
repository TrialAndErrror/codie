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
	Activity
	// Approval asks the user for permission before the first file read of a
	// question. Text says what is being asked. The consumer must send exactly
	// one value on Reply (it is buffered, so sending never blocks); the
	// producer waits for it, or for ctx to be cancelled.
	Approval
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

	Reply chan<- bool // for Approval: true allows the reads
}

// Researcher answers a question as a stream.
//
// Contract:
//   - zero or more Text/Activity/Approval events, then exactly one Done or Error, then
//     the channel is closed;
//   - if ctx is cancelled the implementation stops promptly and closes the
//     channel, possibly without a terminal event;
//   - implementations must never block forever on a consumer that has gone
//     away (they select on ctx.Done when sending).
type Researcher interface {
	Research(ctx context.Context, question string) <-chan Event
}

// Turn is one finished question and its answer, the unit of a conversation.
type Turn struct {
	Question string
	Answer   string
}

// Conversational is a Researcher that can continue a conversation: Converse is
// Research with the earlier turns (oldest first) as context. Both real and demo
// backends implement it; a Researcher that doesn't simply answers each
// question on its own.
type Conversational interface {
	Researcher
	Converse(ctx context.Context, history []Turn, question string) <-chan Event
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
