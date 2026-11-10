package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testKey = "sk-or-test-SECRET-KEY-1234"

// ---- helpers ---------------------------------------------------------------

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the whole request body first, as a real server would. net/http
		// only notices a client disconnect (and cancels r.Context()) once the
		// body has been consumed, so handlers that block on r.Context().Done()
		// would otherwise hang forever.
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func client(t *testing.T, srv *httptest.Server) *OpenRouter {
	t.Helper()
	o, err := NewOpenRouter(Config{APIKey: testKey, Model: "test/model", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// sse writes data lines (each already a JSON payload or [DONE]) and flushes.
func sse(w http.ResponseWriter, payloads ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, p := range payloads {
		fmt.Fprintf(w, "data: %s\n\n", p)
	}
	w.(http.Flusher).Flush()
}

func delta(content string) string {
	b, _ := json.Marshal(content)
	return fmt.Sprintf(`{"choices":[{"delta":{"content":%s}}]}`, b)
}

func finishChunk(reason string) string {
	return fmt.Sprintf(`{"choices":[{"delta":{"content":""},"finish_reason":%q}]}`, reason)
}

// drain reads every event, failing the test if the stream never closes.
func drain(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var evs []Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return evs
			}
			evs = append(evs, ev)
		case <-timeout:
			t.Fatalf("stream did not close; got %d events so far", len(evs))
		}
	}
}

func text(evs []Event) string {
	var b strings.Builder
	for _, e := range evs {
		if e.Kind == Text {
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

// last returns the final event, which must be terminal (Done or Error), and
// checks that no other event in the stream is terminal.
func last(t *testing.T, evs []Event) Event {
	t.Helper()
	if len(evs) == 0 {
		t.Fatal("no events")
	}
	for _, e := range evs[:len(evs)-1] {
		if e.Kind == Done || e.Kind == Error {
			t.Fatalf("terminal event before the end: %+v", evs)
		}
	}
	l := evs[len(evs)-1]
	if l.Kind != Done && l.Kind != Error {
		t.Fatalf("stream ended without a terminal event: %+v", evs)
	}
	return l
}

func noKey(t *testing.T, evs []Event) {
	t.Helper()
	for _, e := range evs {
		if e.Err != nil && strings.Contains(e.Err.Error(), testKey) {
			t.Fatalf("API key leaked into an error: %v", e.Err)
		}
	}
}

// ---- config ----------------------------------------------------------------

func TestConfigFromEnv(t *testing.T) {
	env := map[string]string{EnvAPIKey: "  k  ", EnvModel: " m/x "}
	c := ConfigFromEnv(func(k string) string { return env[k] })
	if c.APIKey != "k" || c.Model != "m/x" || c.BaseURL != DefaultBaseURL {
		t.Fatalf("got %+v", c)
	}
	env[EnvBaseURL] = "http://localhost:9"
	if c := ConfigFromEnv(func(k string) string { return env[k] }); c.BaseURL != "http://localhost:9" {
		t.Fatalf("base url override ignored: %+v", c)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string // substring of the error; "" means valid
	}{
		{"ok", Config{APIKey: "k", Model: "m", BaseURL: DefaultBaseURL}, ""},
		{"missing key names the env var", Config{Model: "m", BaseURL: DefaultBaseURL}, EnvAPIKey},
		{"missing model names the env var", Config{APIKey: "k", BaseURL: DefaultBaseURL}, EnvModel},
		{"plain http to a remote host is refused", Config{APIKey: "k", Model: "m", BaseURL: "http://example.com/v1"}, "refusing"},
		{"plain http to localhost is allowed", Config{APIKey: "k", Model: "m", BaseURL: "http://localhost:8080"}, ""},
		{"plain http to 127.0.0.1 is allowed", Config{APIKey: "k", Model: "m", BaseURL: "http://127.0.0.1:8080"}, ""},
		{"plain http to ::1 is allowed", Config{APIKey: "k", Model: "m", BaseURL: "http://[::1]:8080"}, ""},
		{"other schemes are refused", Config{APIKey: "k", Model: "m", BaseURL: "ftp://example.com"}, "https://"},
		{"garbage url", Config{APIKey: "k", Model: "m", BaseURL: "not a url"}, "valid URL"},
		{"localhost lookalike is not loopback", Config{APIKey: "k", Model: "m", BaseURL: "http://localhost.evil.com"}, "refusing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
			if err != nil && tt.cfg.APIKey != "" && strings.Contains(err.Error(), tt.cfg.APIKey) && len(tt.cfg.APIKey) > 3 {
				t.Fatal("validation error leaked the key")
			}
		})
	}
}

func TestMaskedKeyNeverRevealsTheKey(t *testing.T) {
	for _, key := range []string{"", "short", testKey} {
		m := Config{APIKey: key}.MaskedKey()
		if key != "" && strings.Contains(m, key) {
			t.Fatalf("masked key contains the whole key: %q", m)
		}
	}
	if m := (Config{APIKey: testKey}).MaskedKey(); !strings.HasSuffix(m, "1234)") {
		t.Fatalf("expected last four characters to be shown: %q", m)
	}
	if got := (Config{}).MaskedKey(); got != "not set" {
		t.Fatalf("got %q", got)
	}
}

func TestNewOpenRouterRejectsBadConfig(t *testing.T) {
	if _, err := NewOpenRouter(Config{}); err == nil {
		t.Fatal("expected a validation error")
	}
}

// ---- the request we send -----------------------------------------------------

func TestRequestShape(t *testing.T) {
	var (
		method, path, auth, ctype, title string
		body                             chatRequest
	)
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		auth, ctype, title = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Header.Get("X-Title")
		_ = json.NewDecoder(r.Body).Decode(&body)
		sse(w, delta("x"), finishChunk("stop"), "[DONE]")
	})
	o := client(t, srv)
	o.MaxTokens = 123
	o.System = "be brief"
	drain(t, o.Research(context.Background(), "how do I loop?"))

	if method != http.MethodPost || path != "/chat/completions" {
		t.Fatalf("request line: %s %s", method, path)
	}
	if auth != "Bearer "+testKey {
		t.Fatalf("Authorization header wrong: %q", auth)
	}
	if ctype != "application/json" || title != "codie" {
		t.Fatalf("headers: content-type=%q x-title=%q", ctype, title)
	}
	if body.Model != "test/model" || !body.Stream || body.MaxTokens != 123 {
		t.Fatalf("body: %+v", body)
	}
	if len(body.Messages) != 2 ||
		body.Messages[0].Role != "system" || body.Messages[0].Content != "be brief" ||
		body.Messages[1].Role != "user" || body.Messages[1].Content != "how do I loop?" {
		t.Fatalf("messages: %+v", body.Messages)
	}
}

func TestMaxTokensOmittedWhenZero(t *testing.T) {
	var raw map[string]any
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		sse(w, delta("x"), "[DONE]")
	})
	drain(t, client(t, srv).Research(context.Background(), "q"))
	if _, present := raw["max_tokens"]; present {
		t.Fatal("max_tokens should be omitted when unset")
	}
}

func TestBaseURLTrailingSlash(t *testing.T) {
	var path string
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		sse(w, delta("x"), "[DONE]")
	})
	o, _ := NewOpenRouter(Config{APIKey: testKey, Model: "m", BaseURL: srv.URL + "/api/v1/"})
	drain(t, o.Research(context.Background(), "q"))
	if path != "/api/v1/chat/completions" {
		t.Fatalf("path = %q", path)
	}
}

// ---- reading the stream ------------------------------------------------------

func TestStreamHappyPath(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": OPENROUTER PROCESSING\n\n") // keep-alive comment
		sse(w,
			`{"choices":[{"delta":{"role":"assistant","content":""}}]}`, // role-only opener
			delta("func main() {"),
			`{"choices":[{"delta":{"content":null}}]}`, // null content
			delta("\n\tprintln(1)\n}"),
			finishChunk("stop"),
			"[DONE]",
		)
	})
	evs := drain(t, client(t, srv).Research(context.Background(), "q"))

	if l := last(t, evs); l.Kind != Done {
		t.Fatalf("want Done, got %+v", l)
	}
	if got := text(evs); got != "func main() {\n\tprintln(1)\n}" {
		t.Fatalf("assembled text = %q", got)
	}
	for _, e := range evs {
		if e.Kind == Text && e.Text == "" {
			t.Fatal("empty Text events should be suppressed")
		}
	}
}

func TestStreamEventsArriveIncrementally(t *testing.T) {
	release := make(chan struct{})
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, delta("first"))
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		sse(w, delta("second"), finishChunk("stop"), "[DONE]")
	})
	ch := client(t, srv).Research(context.Background(), "q")

	select {
	case ev := <-ch:
		if ev.Kind != Text || ev.Text != "first" {
			t.Fatalf("first event = %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first chunk was not delivered before the stream finished: not streaming")
	}
	close(release)
	if text(drain(t, ch)) != "second" {
		t.Fatal("second chunk missing")
	}
}

func TestStreamLineSplitAcrossWrites(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, part := range []string{`data: {"choices":[{"del`, `ta":{"content":"he`, `llo"}}]}` + "\n\n", "data: " + finishChunk("stop") + "\n\n", "data: [DONE]\n\n"} {
			io.WriteString(w, part)
			f.Flush()
			time.Sleep(15 * time.Millisecond)
		}
	})
	evs := drain(t, client(t, srv).Research(context.Background(), "q"))
	if last(t, evs).Kind != Done || text(evs) != "hello" {
		t.Fatalf("events: %+v", evs)
	}
}

func TestStreamCRLFLineEndings(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\r\n\r\ndata: %s\r\n\r\ndata: [DONE]\r\n\r\n", delta("ok"), finishChunk("stop"))
	})
	evs := drain(t, client(t, srv).Research(context.Background(), "q"))
	if last(t, evs).Kind != Done || text(evs) != "ok" {
		t.Fatalf("events: %+v", evs)
	}
}

func TestStreamIgnoresOtherSSEFields(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\nid: 7\nretry: 100\ndata:%s\n\ndata:%s\n\ndata:[DONE]\n\n", delta("x"), finishChunk("stop"))
	})
	evs := drain(t, client(t, srv).Research(context.Background(), "q"))
	if last(t, evs).Kind != Done || text(evs) != "x" {
		t.Fatalf("events: %+v", evs)
	}
}

func TestStreamVeryLongLine(t *testing.T) {
	big := strings.Repeat("a", 300<<10) // far beyond bufio.Scanner's 64KB default
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, delta(big), finishChunk("stop"), "[DONE]")
	})
	evs := drain(t, client(t, srv).Research(context.Background(), "q"))
	if last(t, evs).Kind != Done || text(evs) != big {
		t.Fatalf("long line mangled: kind=%v len=%d", last(t, evs).Kind, len(text(evs)))
	}
}

func TestStreamUnicodeSurvives(t *testing.T) {
	want := "// 日本語 ✓ 🚀 naïve"
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, delta(want), finishChunk("stop"), "[DONE]")
	})
	evs := drain(t, client(t, srv).Research(context.Background(), "q"))
	if text(evs) != want {
		t.Fatalf("got %q", text(evs))
	}
}

func TestStreamDoneWithoutFinishReasonIsDone(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { sse(w, delta("x"), "[DONE]") })
	if l := last(t, drain(t, client(t, srv).Research(context.Background(), "q"))); l.Kind != Done {
		t.Fatalf("got %+v", l)
	}
}

func TestStreamEOFWithFinishButNoDoneIsDone(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { sse(w, delta("x"), finishChunk("stop")) })
	if l := last(t, drain(t, client(t, srv).Research(context.Background(), "q"))); l.Kind != Done {
		t.Fatalf("got %+v", l)
	}
}

// ---- when things go wrong ----------------------------------------------------

func TestStreamEOFMidAnswerIsAnError(t *testing.T) {
	// The connection drops after some text: that's a truncated answer, not a finished one.
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { sse(w, delta("partial")) })
	evs := drain(t, client(t, srv).Research(context.Background(), "q"))
	l := last(t, evs)
	if l.Kind != Error || !strings.Contains(l.Err.Error(), "closed before the answer finished") {
		t.Fatalf("got %+v", l)
	}
	if text(evs) != "partial" {
		t.Fatalf("partial text should still be delivered, got %q", text(evs))
	}
}

func TestStreamFinishReasons(t *testing.T) {
	tests := []struct {
		reason  string
		want    Kind
		message string
	}{
		{"stop", Done, ""},
		{"tool_calls", Done, ""},
		{"length", Error, "length limit"},
		{"content_filter", Error, "content filter"},
		{"error", Error, "stopped with an error"},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
				sse(w, delta("x"), finishChunk(tt.reason), "[DONE]")
			})
			l := last(t, drain(t, client(t, srv).Research(context.Background(), "q")))
			if l.Kind != tt.want {
				t.Fatalf("got %+v", l)
			}
			if tt.message != "" && !strings.Contains(l.Err.Error(), tt.message) {
				t.Fatalf("message %q lacks %q", l.Err, tt.message)
			}
		})
	}
}

func TestStreamErrorObjectMidAnswer(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, delta("part one"),
			`{"error":{"code":502,"message":"upstream provider fell over"},"choices":[{"delta":{"content":""},"finish_reason":"error"}]}`)
	})
	evs := drain(t, client(t, srv).Research(context.Background(), "q"))
	l := last(t, evs)
	if l.Kind != Error || !strings.Contains(l.Err.Error(), "upstream provider fell over") {
		t.Fatalf("got %+v", l)
	}
	if text(evs) != "part one" {
		t.Fatalf("text before the error should be kept, got %q", text(evs))
	}
}

func TestStreamErrorObjectWithoutMessage(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { sse(w, `{"error":{"code":429}}`) })
	l := last(t, drain(t, client(t, srv).Research(context.Background(), "q")))
	if l.Kind != Error || !strings.Contains(l.Err.Error(), "429") {
		t.Fatalf("got %+v", l)
	}
}

func TestStreamUnreadableChunk(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { sse(w, delta("ok"), `{not json`) })
	evs := drain(t, client(t, srv).Research(context.Background(), "q"))
	if l := last(t, evs); l.Kind != Error || !strings.Contains(l.Err.Error(), "couldn't read") {
		t.Fatalf("got %+v", l)
	}
}

func TestHTTPErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   []string // all must appear
	}{
		{"bad key", 401, `{"error":{"message":"No auth credentials found"}}`, []string{"rejected your API key", EnvAPIKey, "No auth credentials found"}},
		{"no credits", 402, `{"error":{"message":"Insufficient credits"}}`, []string{"out of credits", "Insufficient credits"}},
		{"blocked", 403, `{"error":{"message":"flagged by moderation"}}`, []string{"blocked", "flagged by moderation"}},
		{"unknown model", 404, `{"error":{"message":"No endpoints found for x/y"}}`, []string{"couldn't find the model", EnvModel, "test/model", "No endpoints found"}},
		{"rate limited", 429, `{"error":{"message":"slow down"}}`, []string{"rate limiting", "slow down"}},
		{"provider down", 503, `{"error":{"message":"overloaded"}}`, []string{"503", "overloaded"}},
		{"gateway timeout", 504, ``, []string{"timed out", "504"}},
		{"bad request", 400, `{"error":{"message":"x is not a valid model ID"}}`, []string{"HTTP 400", "not a valid model ID"}},
		{"html error page is not dumped on the user", 502, `<html><body>Bad gateway</body></html>`, []string{"502"}},
		{"plain text body is shown", 500, "kaboom\n  happened", []string{"kaboom happened"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			})
			evs := drain(t, client(t, srv).Research(context.Background(), "q"))
			if len(evs) != 1 || evs[0].Kind != Error {
				t.Fatalf("want exactly one Error, got %+v", evs)
			}
			msg := evs[0].Err.Error()
			for _, w := range tt.want {
				if !strings.Contains(msg, w) {
					t.Errorf("message %q lacks %q", msg, w)
				}
			}
			if strings.Contains(msg, "<html") {
				t.Errorf("HTML leaked into the message: %q", msg)
			}
		})
	}
}

func TestHTTPErrorBodyEchoingTheKeyIsRedacted(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprintf(w, `{"error":{"message":"invalid key %s"}}`, testKey)
	})
	evs := drain(t, client(t, srv).Research(context.Background(), "q"))
	noKey(t, evs)
	if !strings.Contains(evs[0].Err.Error(), "[redacted]") {
		t.Fatalf("expected a redaction marker: %v", evs[0].Err)
	}
}

func TestMidStreamErrorEchoingTheKeyIsRedacted(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, fmt.Sprintf(`{"error":{"message":"key %s revoked"}}`, testKey))
	})
	noKey(t, drain(t, client(t, srv).Research(context.Background(), "q")))
}

func TestConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing is listening any more
	o, _ := NewOpenRouter(Config{APIKey: testKey, Model: "m", BaseURL: url})
	evs := drain(t, o.Research(context.Background(), "q"))
	l := last(t, evs)
	if l.Kind != Error || !strings.Contains(l.Err.Error(), "could not reach OpenRouter") {
		t.Fatalf("got %+v", l)
	}
	noKey(t, evs)
}

func TestNonStreamingJSONFallback(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"whole answer"},"finish_reason":"stop"}]}`)
	})
	evs := drain(t, client(t, srv).Research(context.Background(), "q"))
	if last(t, evs).Kind != Done || text(evs) != "whole answer" {
		t.Fatalf("events: %+v", evs)
	}
}

func TestNonStreamingJSONError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"error":{"code":500,"message":"provider returned nothing"}}`)
	})
	l := last(t, drain(t, client(t, srv).Research(context.Background(), "q")))
	if l.Kind != Error || !strings.Contains(l.Err.Error(), "provider returned nothing") {
		t.Fatalf("got %+v", l)
	}
}

func TestNonStreamingJSONTruncated(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"cut"},"finish_reason":"length"}]}`)
	})
	if l := last(t, drain(t, client(t, srv).Research(context.Background(), "q"))); l.Kind != Error {
		t.Fatalf("a length-limited reply must be an Error, got %+v", l)
	}
}

// ---- cancellation and timeouts -----------------------------------------------

func TestCancelMidStreamClosesChannelAndDropsConnection(t *testing.T) {
	disconnected := make(chan struct{})
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, delta("hello"))
		<-r.Context().Done() // hold the stream open until the client goes away
		close(disconnected)
	})
	ctx, cancel := context.WithCancel(context.Background())
	ch := client(t, srv).Research(ctx, "q")

	if ev := <-ch; ev.Text != "hello" {
		t.Fatalf("first event = %+v", ev)
	}
	cancel()
	evs := drain(t, ch) // must close promptly
	for _, e := range evs {
		if e.Kind == Error {
			t.Fatalf("a user cancel must not surface as an Error: %+v", e)
		}
	}
	select {
	case <-disconnected:
	case <-time.After(3 * time.Second):
		t.Fatal("the HTTP connection was not closed after cancel (would keep billing)")
	}
}

func TestCancelWhenNobodyIsReading(t *testing.T) {
	// The consumer stops draining the channel and cancels. The producer must
	// not stay blocked on its send forever, and must drop the connection.
	disconnected := make(chan struct{})
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		for i := 0; ; i++ {
			sse(w, delta("x"))
			select {
			case <-r.Context().Done():
				close(disconnected)
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	_ = client(t, srv).Research(ctx, "q") // never read from it
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-disconnected:
	case <-time.After(3 * time.Second):
		t.Fatal("the connection was not dropped")
	}
	// Dropping the connection isn't enough: the goroutine itself must exit too,
	// even though nobody ever received from its channel.
	waitForNoResearchGoroutines(t)
}

// waitForNoResearchGoroutines fails if a Research goroutine is still alive
// shortly after its context was cancelled (a leak).
func waitForNoResearchGoroutines(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		buf := make([]byte, 1<<20)
		stacks := string(buf[:runtime.Stack(buf, true)])
		if !strings.Contains(stacks, "(*OpenRouter).run") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a Research goroutine leaked after cancel:\n%s", stacks)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCancelBeforeResponseHeaders(t *testing.T) {
	started := make(chan struct{})
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	ch := client(t, srv).Research(ctx, "q")
	<-started
	cancel()
	for _, e := range drain(t, ch) {
		if e.Kind == Error {
			t.Fatalf("cancel while waiting for the first byte must be silent: %+v", e)
		}
	}
}

func TestIdleTimeoutReportsAStalledStream(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, delta("started"))
		<-r.Context().Done() // then say nothing, forever
	})
	o := client(t, srv)
	o.IdleTimeout = 100 * time.Millisecond

	begin := time.Now()
	evs := drain(t, o.Research(context.Background(), "q"))
	l := last(t, evs)
	if l.Kind != Error || !strings.Contains(l.Err.Error(), "stopped responding") {
		t.Fatalf("got %+v", l)
	}
	if text(evs) != "started" {
		t.Fatalf("text before the stall should be kept: %q", text(evs))
	}
	if time.Since(begin) > 3*time.Second {
		t.Fatal("idle timeout took far too long")
	}
}

func TestIdleTimeoutAlsoCoversWaitingForTheFirstByte(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	o := client(t, srv)
	o.IdleTimeout = 100 * time.Millisecond
	l := last(t, drain(t, o.Research(context.Background(), "q")))
	if l.Kind != Error || !strings.Contains(l.Err.Error(), "stopped responding") {
		t.Fatalf("got %+v", l)
	}
}

func TestKeepAliveCommentsPreventIdleTimeout(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for i := 0; i < 10; i++ { // 300ms of silence-with-heartbeats, well past the 120ms limit
			io.WriteString(w, ": OPENROUTER PROCESSING\n\n")
			f.Flush()
			time.Sleep(30 * time.Millisecond)
		}
		sse(w, delta("late but fine"), finishChunk("stop"), "[DONE]")
	})
	o := client(t, srv)
	o.IdleTimeout = 120 * time.Millisecond
	evs := drain(t, o.Research(context.Background(), "q"))
	if last(t, evs).Kind != Done || text(evs) != "late but fine" {
		t.Fatalf("heartbeats should keep the stream alive: %+v", evs)
	}
}

// ---- API key safety ------------------------------------------------------------

func TestRedirectsAreNotFollowedAndKeyIsNotForwarded(t *testing.T) {
	var hits atomic.Int32
	var gotAuth atomic.Value
	other := serve(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotAuth.Store(r.Header.Get("Authorization"))
		sse(w, delta("from the other server"), "[DONE]")
	})
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/chat/completions", http.StatusTemporaryRedirect)
	})

	evs := drain(t, client(t, srv).Research(context.Background(), "q"))
	l := last(t, evs)
	if l.Kind != Error || !strings.Contains(l.Err.Error(), "redirect") {
		t.Fatalf("a redirect should be an error, got %+v", l)
	}
	if hits.Load() != 0 {
		t.Fatalf("redirect target was contacted %d time(s); auth=%v", hits.Load(), gotAuth.Load())
	}
}

func TestKeyIsOnlySentInTheAuthorizationHeader(t *testing.T) {
	var url, body string
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		url, body = r.URL.String(), string(b)
		sse(w, delta("x"), "[DONE]")
	})
	drain(t, client(t, srv).Research(context.Background(), "q"))
	if strings.Contains(url, testKey) || strings.Contains(body, testKey) {
		t.Fatal("the API key must never appear in the URL or request body")
	}
}

func TestSecretsInTheQuestionAreSentAsIs(t *testing.T) {
	// Documenting behaviour, not a feature: Codie sends the question verbatim.
	// (Redaction applies to *our* key in *error text*, not to user input.)
	var body chatRequest
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		sse(w, delta("x"), "[DONE]")
	})
	drain(t, client(t, srv).Research(context.Background(), "what does `"+testKey+"` mean?"))
	if !strings.Contains(body.Messages[1].Content, testKey) {
		t.Fatal("question should be sent verbatim")
	}
}

// ---- the stream contract -------------------------------------------------------

func TestExactlyOneTerminalEventAndChannelCloses(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"success":     func(w http.ResponseWriter, r *http.Request) { sse(w, delta("x"), finishChunk("stop"), "[DONE]") },
		"http error":  func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) },
		"midstream":   func(w http.ResponseWriter, r *http.Request) { sse(w, delta("x"), `{"error":{"message":"bad"}}`) },
		"early close": func(w http.ResponseWriter, r *http.Request) { sse(w, delta("x")) },
	} {
		t.Run(name, func(t *testing.T) {
			evs := drain(t, client(t, serve(t, h)).Research(context.Background(), "q"))
			terminals := 0
			for _, e := range evs {
				if e.Kind == Done || e.Kind == Error {
					terminals++
				}
			}
			if terminals != 1 {
				t.Fatalf("want exactly 1 terminal event, got %d: %+v", terminals, evs)
			}
			last(t, evs)
		})
	}
}

func TestCollect(t *testing.T) {
	mk := func(evs ...Event) <-chan Event {
		ch := make(chan Event, len(evs))
		for _, e := range evs {
			ch <- e
		}
		close(ch)
		return ch
	}
	if s, err := Collect(mk(Event{Kind: Text, Text: "a"}, Event{Kind: Activity, Text: "reading"}, Event{Kind: Text, Text: "b"}, Event{Kind: Done})); s != "ab" || err != nil {
		t.Fatalf("got %q, %v", s, err)
	}
	boom := fmt.Errorf("boom")
	if s, err := Collect(mk(Event{Kind: Text, Text: "a"}, Event{Kind: Error, Err: boom})); s != "a" || err != boom {
		t.Fatalf("got %q, %v", s, err)
	}
	if _, err := Collect(mk(Event{Kind: Text, Text: "a"})); err == nil {
		t.Fatal("a stream that closes without a result must be an error")
	}
}

// ---- terminal safety -------------------------------------------------------------

func TestStreamedTextHasControlCharactersStripped(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			delta("safe \x1b[2J\x1b[Hcleared? "),    // clear screen + home
			delta("\x1b]52;c;SGVsbG8=\x07copied? "), // OSC 52 clipboard write
			delta("over\rwritten "),                 // carriage return
			delta("c1:\u009b31m "),                  // 8-bit CSI
			delta("tab\tand\nnewline kept"),
			finishChunk("stop"), "[DONE]")
	})
	evs := drain(t, client(t, srv).Research(context.Background(), "q"))
	got := text(evs)
	for _, bad := range []string{"\x1b", "\x07", "\r", "\u009b"} {
		if strings.Contains(got, bad) {
			t.Fatalf("control character %q reached the UI: %q", bad, got)
		}
	}
	if !strings.Contains(got, "tab\tand\nnewline kept") || !strings.Contains(got, "safe ") {
		t.Fatalf("ordinary text must survive: %q", got)
	}
	if last(t, evs).Kind != Done {
		t.Fatal("stream should still complete")
	}
}

func TestNonStreamingTextHasControlCharactersStripped(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"a\u001b[31mb"},"finish_reason":"stop"}]}`)
	})
	if got := text(drain(t, client(t, srv).Research(context.Background(), "q"))); strings.Contains(got, "\x1b") {
		t.Fatalf("escape reached the UI: %q", got)
	}
}

func TestErrorMessagesFromTheServerHaveControlCharactersStripped(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"http error body": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			io.WriteString(w, `{"error":{"message":"bad \u001b]0;pwned\u0007 thing"}}`)
		},
		"mid-stream error": func(w http.ResponseWriter, r *http.Request) {
			sse(w, `{"error":{"message":"bad \u001b[2J thing"}}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			evs := drain(t, client(t, serve(t, h)).Research(context.Background(), "q"))
			msg := last(t, evs).Err.Error()
			if strings.ContainsAny(msg, "\x1b\x07") {
				t.Fatalf("escape reached an error message: %q", msg)
			}
		})
	}
}

// ---- account policy blocks ---------------------------------------------------------

// This is the shape of the 404 OpenRouter returns when a workspace guardrail
// excludes every endpoint for a model that does exist.
const guardrailBody = `{"error":{"message":"0 endpoints out of 14 requested are available matching your guardrail restrictions and data policy. We removed them for the following reasons (an endpoint may have matched multiple reasons):\nModel blocked by guardrail: 14 endpoints excluded; configurable at https://openrouter.ai/workspaces/default/guardrails","code":404}}`

func TestGuardrailBlockIsNotReportedAsAMissingModel(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, guardrailBody)
	})
	evs := drain(t, client(t, srv).Research(context.Background(), "q"))
	msg := last(t, evs).Err.Error()

	for _, want := range []string{
		"guardrails or data-policy settings",                  // says what is actually wrong
		"test/model",                                          // names the model
		"Model blocked by guardrail",                          // OpenRouter's own reason is kept
		"https://openrouter.ai/workspaces/default/guardrails", // the link is not cut off
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	for _, bad := range []string{"couldn't find the model", "Check " + EnvModel} {
		if strings.Contains(msg, bad) {
			t.Errorf("message misleads the user with %q:\n%s", bad, msg)
		}
	}
}

func TestDataPolicyBlockIsNotReportedAsAMissingModel(t *testing.T) {
	// e.g. zero data retention is enforced and the model has no ZDR endpoint.
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, `{"error":{"message":"No endpoints found matching your data policy (Zero data retention). Configure: https://openrouter.ai/settings/privacy"}}`)
	})
	msg := last(t, drain(t, client(t, srv).Research(context.Background(), "q"))).Err.Error()
	if !strings.Contains(msg, "data-policy settings") || !strings.Contains(msg, "https://openrouter.ai/settings/privacy") {
		t.Fatalf("got: %s", msg)
	}
	if strings.Contains(msg, "couldn't find the model") {
		t.Fatalf("misleading: %s", msg)
	}
}

func TestGenuinelyMissingModelStillSaysSo(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, `{"error":{"message":"No endpoints found for nope/not-a-model"}}`)
	})
	msg := last(t, drain(t, client(t, srv).Research(context.Background(), "q"))).Err.Error()
	if !strings.Contains(msg, "couldn't find the model") || !strings.Contains(msg, EnvModel) {
		t.Fatalf("a real missing model should keep the 'check CODIE_MODEL' advice: %s", msg)
	}
}

func TestLongErrorDetailsAreKeptUpToALimit(t *testing.T) {
	long := strings.Repeat("reason; ", 100) + "THE-LINK-AT-THE-END" // ~800 chars
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprintf(w, `{"error":{"message":%q}}`, long)
	})
	if msg := last(t, drain(t, client(t, srv).Research(context.Background(), "q"))).Err.Error(); !strings.Contains(msg, "THE-LINK-AT-THE-END") {
		t.Fatalf("detail was cut off too early (%d chars)", len(msg))
	}

	huge := strings.Repeat("x", 5000)
	srv2 := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprintf(w, `{"error":{"message":%q}}`, huge)
	})
	if msg := last(t, drain(t, client(t, srv2).Research(context.Background(), "q"))).Err.Error(); len(msg) > 1500 {
		t.Fatalf("a huge server message must still be bounded, got %d chars", len(msg))
	}
}

func TestGuardrailWordingAloneIsRecognised(t *testing.T) {
	// A message that says "guardrail" but never "data policy", so detection can't
	// lean on the other keyword.
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, `{"error":{"message":"Model blocked by guardrail: 3 endpoints excluded"}}`)
	})
	msg := last(t, drain(t, client(t, srv).Research(context.Background(), "q"))).Err.Error()
	if !strings.Contains(msg, "guardrails or data-policy settings") || strings.Contains(msg, "couldn't find the model") {
		t.Fatalf("got: %s", msg)
	}
}

// ---- the length limit is a recognisable condition ------------------------------------

func TestLengthLimitIsRecognisableWithErrorsIs(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"streaming": func(w http.ResponseWriter, r *http.Request) {
			sse(w, delta("part"), finishChunk("length"), "[DONE]")
		},
		"reasoning only, no answer text": func(w http.ResponseWriter, r *http.Request) {
			sse(w, `{"choices":[{"delta":{"reasoning":"thinking…"}}]}`, finishChunk("length"), "[DONE]")
		},
		"non-streaming reply": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"choices":[{"message":{"content":"cut"},"finish_reason":"length"}]}`)
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			evs := drain(t, client(t, serve(t, h)).Research(context.Background(), "q"))
			l := last(t, evs)
			if l.Kind != Error || !errors.Is(l.Err, ErrTruncated) {
				t.Fatalf("want an error that is ErrTruncated, got %+v", l)
			}
			if !strings.Contains(l.Err.Error(), "length limit") {
				t.Fatalf("the message people read should be unchanged: %q", l.Err)
			}
		})
	}
}

func TestOtherFailuresAreNotMistakenForTheLengthLimit(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"content filter": func(w http.ResponseWriter, r *http.Request) {
			sse(w, delta("x"), finishChunk("content_filter"), "[DONE]")
		},
		"model error":    func(w http.ResponseWriter, r *http.Request) { sse(w, delta("x"), finishChunk("error"), "[DONE]") },
		"dropped stream": func(w http.ResponseWriter, r *http.Request) { sse(w, delta("x")) },
		"http 500":       func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) },
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			l := last(t, drain(t, client(t, serve(t, h)).Research(context.Background(), "q")))
			if l.Kind != Error || errors.Is(l.Err, ErrTruncated) {
				t.Fatalf("%s must be an error but not ErrTruncated: %+v", name, l)
			}
		})
	}
}

// ---- conversations ---------------------------------------------------------

func TestConverseSendsEarlierTurnsInOrder(t *testing.T) {
	var got chatRequest
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		sse(w, delta("ok"), finishChunk("stop"), "[DONE]")
	})
	o := client(t, srv)
	hist := []Turn{{"q1", "a1"}, {"q2", "a2"}}
	if _, err := Collect(o.Converse(context.Background(), hist, "q3")); err != nil {
		t.Fatal(err)
	}
	var seq []string
	for _, m := range got.Messages {
		seq = append(seq, m.Role+":"+m.Content)
	}
	want := []string{"system:" + DefaultSystemPrompt, "user:q1", "assistant:a1", "user:q2", "assistant:a2", "user:q3"}
	if strings.Join(seq, "|") != strings.Join(want, "|") {
		t.Fatalf("messages:\n%q\nwant\n%q", seq, want)
	}
}

func TestResearchHasNoHistory(t *testing.T) {
	var got chatRequest
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		sse(w, delta("ok"), finishChunk("stop"), "[DONE]")
	})
	Collect(client(t, srv).Research(context.Background(), "q"))
	if len(got.Messages) != 2 {
		t.Fatalf("messages: %+v", got.Messages)
	}
}

func TestTrimHistoryKeepsTheNewestWithinLimits(t *testing.T) {
	var many []Turn
	for i := 0; i < 20; i++ {
		many = append(many, Turn{fmt.Sprint("q", i), "a"})
	}
	got := trimHistory(many)
	if len(got) != maxHistoryTurns || got[len(got)-1].Question != "q19" {
		t.Errorf("turn cap: %d turns, last %q", len(got), got[len(got)-1].Question)
	}

	big := strings.Repeat("x", maxHistoryChars/2)
	got = trimHistory([]Turn{{"old", big}, {"mid", big}, {"new", big}})
	if len(got) == 0 || got[len(got)-1].Question != "new" || len(got) >= 3 {
		t.Errorf("size cap: %+v", got)
	}
	// A single oversized turn is still kept: the latest context beats none.
	if got = trimHistory([]Turn{{"only", strings.Repeat("x", maxHistoryChars*2)}}); len(got) != 1 {
		t.Errorf("lone big turn dropped")
	}
	if got = trimHistory(nil); len(got) != 0 {
		t.Errorf("nil history: %v", got)
	}
}
