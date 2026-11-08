package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

// OpenRouter is a Researcher backed by OpenRouter's chat completions API,
// streamed over server-sent events (SSE).
type OpenRouter struct {
	cfg Config

	// System is the system prompt. Defaults to DefaultSystemPrompt.
	System string
	// MaxTokens caps the answer length. Zero leaves it to the provider.
	MaxTokens int
	// IdleTimeout is how long the stream may go completely silent before we
	// give up. OpenRouter sends keep-alive comments, so a healthy stream is
	// never silent for long. Defaults to 60s.
	IdleTimeout time.Duration

	client *http.Client
}

var errIdle = errors.New("idle timeout")

// ErrTruncated is the error reported when a model stops because it reached its
// output limit (finish_reason "length"). Callers can test for it with
// errors.Is: the request itself worked, the reply just ran out of room, which is
// ordinary for models that spend tokens thinking before they answer.
var ErrTruncated = errors.New("the answer was cut off because the model hit its length limit")

// NewOpenRouter validates cfg and returns a ready client.
func NewOpenRouter(cfg Config) (*OpenRouter, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &OpenRouter{
		cfg:         cfg,
		System:      DefaultSystemPrompt,
		IdleTimeout: 60 * time.Second,
		client: &http.Client{
			// Never follow redirects: the API key rides along in a header, and
			// a redirect is never expected from this API. Treat it as an error.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	Stream    bool          `json:"stream"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// apiError is the error object OpenRouter returns, both as an HTTP error body
// and inside a stream when something fails after the answer has started.
type apiError struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
}

type chunk struct {
	Error   *apiError `json:"error"`
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		Message struct { // present instead of delta in non-streaming replies
			Content string `json:"content"`
		} `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// Research implements Researcher.
func (o *OpenRouter) Research(ctx context.Context, question string) <-chan Event {
	out := make(chan Event)
	go func() {
		defer close(out)
		send := func(e Event) bool {
			select {
			case out <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}
		o.run(ctx, question, send)
	}()
	return out
}

func (o *OpenRouter) run(ctx context.Context, question string, send func(Event) bool) {
	// rctx is cancelled by the caller (ctx) or by our idle watchdog.
	rctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	idle := o.IdleTimeout
	if idle <= 0 {
		idle = 60 * time.Second
	}
	watchdog := time.AfterFunc(idle, func() { cancel(errIdle) })
	defer watchdog.Stop()

	fail := func(format string, args ...any) {
		send(Event{Kind: Error, Err: o.errorf(format, args...)})
	}
	// stopped reports whether the request ended because the caller cancelled
	// (stay silent) or because the watchdog fired (report it).
	stopped := func() bool {
		if ctx.Err() != nil {
			return true // user cancelled: no event, the UI already moved on
		}
		if errors.Is(context.Cause(rctx), errIdle) {
			fail("OpenRouter stopped responding (no data for %s). Try again.", idle.Round(time.Second))
			return true
		}
		return false
	}

	payload, err := json.Marshal(chatRequest{
		Model:     o.cfg.Model,
		Stream:    true,
		MaxTokens: o.MaxTokens,
		Messages: []chatMessage{
			{Role: "system", Content: o.System},
			{Role: "user", Content: question},
		},
	})
	if err != nil {
		fail("could not build the request: %v", err)
		return
	}

	req, err := http.NewRequestWithContext(rctx, http.MethodPost, strings.TrimRight(o.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		fail("could not build the request: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+o.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Title", "codie")

	resp, err := o.client.Do(req)
	if err != nil {
		if !stopped() {
			fail("could not reach OpenRouter: %v", err)
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		send(Event{Kind: Error, Err: o.httpError(resp.StatusCode, body)})
		return
	}

	// Some models/providers ignore stream:true and reply with plain JSON.
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt == "application/json" {
		o.readWhole(resp.Body, send, fail, stopped)
		return
	}

	reader := bufio.NewReaderSize(resp.Body, 64<<10)
	var (
		finish  string
		sawDone bool
	)
loop:
	for {
		line, readErr := reader.ReadString('\n')
		watchdog.Reset(idle)

		if line = strings.TrimRight(line, "\r\n"); line != "" && !strings.HasPrefix(line, ":") {
			// Anything that isn't a "data:" line (event:, id:, retry:) is ignored.
			if data, ok := strings.CutPrefix(line, "data:"); ok {
				data = strings.TrimSpace(data)
				if data == "[DONE]" {
					sawDone = true
					break loop
				}
				var c chunk
				if err := json.Unmarshal([]byte(data), &c); err != nil {
					fail("OpenRouter sent data Codie couldn't read.")
					return
				}
				if c.Error != nil {
					fail("OpenRouter reported an error during the answer: %s", c.Error.describe())
					return
				}
				for _, ch := range c.Choices {
					if txt := Sanitize(ch.Delta.Content); txt != "" && !send(Event{Kind: Text, Text: txt}) {
						return
					}
					if ch.FinishReason != nil && *ch.FinishReason != "" {
						finish = *ch.FinishReason
					}
				}
			}
		}

		if readErr != nil {
			if stopped() {
				return
			}
			if readErr != io.EOF {
				fail("the connection to OpenRouter broke: %v", readErr)
				return
			}
			break loop // clean EOF: judged below
		}
	}

	send(o.terminal(finish, sawDone))
}

// terminal turns how the stream ended into the final event.
func (o *OpenRouter) terminal(finish string, sawDone bool) Event {
	switch finish {
	case "length":
		return Event{Kind: Error, Err: ErrTruncated}
	case "content_filter":
		return Event{Kind: Error, Err: o.errorf("the model's provider blocked this answer (content filter)")}
	case "error":
		return Event{Kind: Error, Err: o.errorf("the model stopped with an error")}
	case "":
		if !sawDone {
			return Event{Kind: Error, Err: o.errorf("the connection closed before the answer finished")}
		}
	}
	return Event{Kind: Done}
}

// readWhole handles a non-streaming JSON reply.
func (o *OpenRouter) readWhole(body io.Reader, send func(Event) bool, fail func(string, ...any), stopped func() bool) {
	raw, err := io.ReadAll(io.LimitReader(body, 16<<20))
	if err != nil {
		if !stopped() {
			fail("the connection to OpenRouter broke: %v", err)
		}
		return
	}
	var c chunk
	if err := json.Unmarshal(raw, &c); err != nil {
		fail("OpenRouter sent data Codie couldn't read.")
		return
	}
	if c.Error != nil {
		fail("OpenRouter reported an error: %s", c.Error.describe())
		return
	}
	var finish string
	for _, ch := range c.Choices {
		if txt := Sanitize(ch.Message.Content); txt != "" && !send(Event{Kind: Text, Text: txt}) {
			return
		}
		if ch.FinishReason != nil {
			finish = *ch.FinishReason
		}
	}
	send(o.terminal(finish, true))
}

func (e *apiError) describe() string {
	if e.Message == "" {
		return fmt.Sprintf("code %v", e.Code)
	}
	return e.Message
}

// httpError turns a non-200 response into a message that says what to do.
func (o *OpenRouter) httpError(status int, body []byte) error {
	var what string
	switch {
	case status == http.StatusUnauthorized:
		what = fmt.Sprintf("OpenRouter rejected your API key (401). Check %s.", EnvAPIKey)
	case status == http.StatusPaymentRequired:
		what = "OpenRouter says your account is out of credits (402). Add credits at openrouter.ai."
	case status == http.StatusForbidden:
		what = "OpenRouter blocked the request (403)."
	case status == http.StatusNotFound && blockedByPolicy(errorDetail(body)):
		// The model exists; your own account settings exclude every endpoint for
		// it. Telling the user to "check the model ID" would send them the wrong way.
		what = fmt.Sprintf("OpenRouter has the model %q, but your account's guardrails or data-policy settings exclude every endpoint for it (404). Choose a model your settings allow, or change the setting the details below point to.", o.cfg.Model)
	case status == http.StatusNotFound:
		what = fmt.Sprintf("OpenRouter couldn't find the model or an endpoint for it (404). Check %s (currently %q).", EnvModel, o.cfg.Model)
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		what = fmt.Sprintf("OpenRouter timed out (%d). Try again.", status)
	case status == http.StatusTooManyRequests:
		what = "OpenRouter is rate limiting you (429). Wait a moment and try again."
	case status >= 500:
		what = fmt.Sprintf("OpenRouter or the model's provider had an error (%d). Try again.", status)
	case status >= 300 && status < 400:
		what = fmt.Sprintf("OpenRouter tried to redirect the request (%d); Codie won't follow it with your API key.", status)
	default:
		what = fmt.Sprintf("OpenRouter returned HTTP %d.", status)
	}

	if detail := errorDetail(body); detail != "" {
		what += " Details: " + detail
	}
	return errors.New(o.redact(what))
}

// blockedByPolicy reports whether an error detail says the model was excluded by
// the user's own OpenRouter settings (a workspace guardrail, or a data-policy
// setting such as zero data retention) rather than not existing.
func blockedByPolicy(detail string) bool {
	d := strings.ToLower(detail)
	return strings.Contains(d, "guardrail") || strings.Contains(d, "data policy")
}

// errorDetail extracts a human message from an error body, or "" if the
// body isn't something worth showing (HTML error pages, empty bodies).
func errorDetail(body []byte) string {
	var wrapper struct {
		Error *apiError `json:"error"`
	}
	if json.Unmarshal(body, &wrapper) == nil && wrapper.Error != nil && wrapper.Error.Message != "" {
		return truncate(wrapper.Error.Message, 1000)
	}
	s := strings.TrimSpace(string(body))
	if s == "" || strings.HasPrefix(s, "<") {
		return ""
	}
	return truncate(strings.Join(strings.Fields(s), " "), 500)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// errorf builds an error whose text can never contain the API key, even if a
// server or the network stack echoed it back, nor terminal control characters.
func (o *OpenRouter) errorf(format string, args ...any) error {
	return errors.New(o.redact(fmt.Sprintf(format, args...)))
}

// redact removes the API key and terminal control characters from text that
// is about to be shown to the user.
func (o *OpenRouter) redact(s string) string {
	if o.cfg.APIKey != "" {
		s = strings.ReplaceAll(s, o.cfg.APIKey, "[redacted]")
	}
	return Sanitize(s)
}
