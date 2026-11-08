package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codie/backend"
	"codie/tui"
)

const key = "sk-or-test-SECRET-KEY-1234"

var (
	now       = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	storePath = func() (string, error) { return "/tmp/codie-test/history.json", nil }
)

func cfg(key, model string) backend.Config {
	return backend.Config{APIKey: key, Model: model, BaseURL: backend.DefaultBaseURL}
}

// ---- which backend do we get? -----------------------------------------------------

func TestNoKeyMeansDemo(t *testing.T) {
	rt, err := resolveRuntime(cfg("", ""), false, false, storePath, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.Researcher.(*tui.FakeResearcher); !ok {
		t.Fatalf("want the fake backend, got %T", rt.Researcher)
	}
	if !strings.Contains(rt.Label, "demo") || !strings.Contains(rt.Label, backend.EnvAPIKey) {
		t.Fatalf("label should say demo and how to get real answers: %q", rt.Label)
	}
}

func TestDemoModeNeverTouchesDisk(t *testing.T) {
	called := false
	rt, err := resolveRuntime(cfg("", ""), true, true, func() (string, error) { called = true; return "", nil }, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, onDisk := rt.Store.(tui.JSONStore); onDisk || called {
		t.Fatalf("demo history must be in memory: store=%T pathLookup=%v", rt.Store, called)
	}
	entries, _ := rt.Store.Load()
	if len(entries) < 10 {
		t.Fatalf("demo history should be pre-seeded, got %d entries", len(entries))
	}
}

func TestKeyAndModelMeansReal(t *testing.T) {
	rt, err := resolveRuntime(cfg(key, "some/model"), false, false, storePath, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.Researcher.(*backend.OpenRouter); !ok {
		t.Fatalf("want OpenRouter, got %T", rt.Researcher)
	}
	js, ok := rt.Store.(tui.JSONStore)
	if !ok || js.Path != "/tmp/codie-test/history.json" {
		t.Fatalf("real mode should persist history to disk: %#v", rt.Store)
	}
	if !strings.Contains(rt.Label, "OpenRouter") || !strings.Contains(rt.Label, "some/model") {
		t.Fatalf("label should name the backend and model: %q", rt.Label)
	}
	if strings.Contains(rt.Label, key) {
		t.Fatal("label leaked the key")
	}
}

func TestKeyWithoutModelIsAnErrorNotADemo(t *testing.T) {
	_, err := resolveRuntime(cfg(key, ""), false, false, storePath, now)
	if err == nil || !strings.Contains(err.Error(), backend.EnvModel) {
		t.Fatalf("want an error naming %s, got %v", backend.EnvModel, err)
	}
	if strings.Contains(err.Error(), key) {
		t.Fatal("error leaked the key")
	}
}

func TestExplicitRealModeWithoutKeyIsAnError(t *testing.T) {
	_, err := resolveRuntime(cfg("", "m"), false, true, storePath, now) // --demo=false
	if err == nil || !strings.Contains(err.Error(), backend.EnvAPIKey) {
		t.Fatalf("want an error naming %s, got %v", backend.EnvAPIKey, err)
	}
}

func TestExplicitDemoBeatsAKey(t *testing.T) {
	rt, err := resolveRuntime(cfg(key, "m"), true, true, storePath, now) // --demo
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.Researcher.(*tui.FakeResearcher); !ok {
		t.Fatalf("--demo must force the fake backend even with a key set, got %T", rt.Researcher)
	}
}

func TestInsecureBaseURLIsRefusedBeforeAnythingIsSent(t *testing.T) {
	c := cfg(key, "m")
	c.BaseURL = "http://example.com/v1"
	_, err := resolveRuntime(c, false, false, storePath, now)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("got %v", err)
	}
}

func TestStorePathErrorIsReported(t *testing.T) {
	boom := errors.New("no config dir")
	_, err := resolveRuntime(cfg(key, "m"), false, false, func() (string, error) { return "", boom }, now)
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}

// ---- codie check ---------------------------------------------------------------------

func checkServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.Copy(io.Discard, r.Body)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func localCfg(srv *httptest.Server) backend.Config {
	return backend.Config{APIKey: key, Model: "test/model", BaseURL: srv.URL}
}

func TestCheckWithoutLiveMakesNoRequests(t *testing.T) {
	srv, hits := checkServer(t, func(w http.ResponseWriter, r *http.Request) {})
	var out bytes.Buffer
	if err := runCheck(context.Background(), &out, localCfg(srv), false); err != nil {
		t.Fatalf("err: %v\n%s", err, out.String())
	}
	if hits.Load() != 0 {
		t.Fatalf("`codie check` must not hit the network without --live; got %d request(s)", hits.Load())
	}
	s := out.String()
	for _, want := range []string{"Mode: real", "test/model", "No request was made", "…1234"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
}

func TestCheckNeverPrintsTheKey(t *testing.T) {
	srv, _ := checkServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprintf(w, `{"error":{"message":"bad key %s"}}`, key)
	})
	var out bytes.Buffer
	err := runCheck(context.Background(), &out, localCfg(srv), true)
	if err == nil {
		t.Fatal("expected a failure")
	}
	if strings.Contains(out.String(), key) || strings.Contains(err.Error(), key) {
		t.Fatalf("the key leaked:\n%s\n%v", out.String(), err)
	}
}

func TestCheckDemoMode(t *testing.T) {
	var out bytes.Buffer
	if err := runCheck(context.Background(), &out, cfg("", ""), false); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "Mode: demo") || !strings.Contains(s, "nothing is sent anywhere") {
		t.Fatalf("output:\n%s", s)
	}
}

func TestCheckLiveWithoutKeyFails(t *testing.T) {
	var out bytes.Buffer
	err := runCheck(context.Background(), &out, cfg("", ""), true)
	if err == nil {
		t.Fatal("--live with no key must fail")
	}
	if strings.Count(out.String(), "Problem:") != 1 || !strings.Contains(out.String(), backend.EnvAPIKey) {
		t.Fatalf("the failure must be explained in the output (cobra's error line is silenced):\n%s", out.String())
	}
}

func TestCheckReportsIncompleteConfig(t *testing.T) {
	var out bytes.Buffer
	err := runCheck(context.Background(), &out, cfg(key, ""), false)
	if err == nil || !strings.Contains(out.String(), "Problem:") || !strings.Contains(out.String(), backend.EnvModel) {
		t.Fatalf("err=%v\n%s", err, out.String())
	}
}

func TestCheckLiveSuccessSendsExactlyOneSmallRequest(t *testing.T) {
	srv, hits := checkServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	var out bytes.Buffer
	if err := runCheck(context.Background(), &out, localCfg(srv), true); err != nil {
		t.Fatalf("err: %v\n%s", err, out.String())
	}
	if hits.Load() != 1 {
		t.Fatalf("want exactly 1 request, got %d", hits.Load())
	}
	if s := out.String(); !strings.Contains(s, "OK.") || !strings.Contains(s, `"ok"`) {
		t.Fatalf("output:\n%s", s)
	}
}

func TestCheckLiveRequestIsTiny(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	var out bytes.Buffer
	if err := runCheck(context.Background(), &out, localCfg(srv), true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"max_tokens":16`) || len(got) > 400 {
		t.Fatalf("the test request should be capped and small (%d bytes): %s", len(got), got)
	}
}

func TestCheckLiveFailureIsReportedAndReturned(t *testing.T) {
	srv, _ := checkServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(402) })
	var out bytes.Buffer
	err := runCheck(context.Background(), &out, localCfg(srv), true)
	if err == nil || !strings.Contains(out.String(), "out of credits") {
		t.Fatalf("err=%v\n%s", err, out.String())
	}
}
