# LLMs.md: shared context for AI sessions working on Codie

Read this first. It says what Codie is, how it's built, where things live, which
rules the tests enforce, and what's decided versus still open. It is written to
be skimmed. **If you change the architecture, a contract below, a keybinding, or
a decision, update this file in the same change.** `README.md` is the
user-facing doc (no internals); this file is the internals doc for sessions.

References use function and type names, not line numbers, because line numbers
rot. Use `grep -n 'func name'` to jump.

---

## 1. What Codie is

A terminal app (Go, Cobra + Bubble Tea). You type a coding question; it asks an
LLM and shows the answer as simple, documented code examples you can step through,
copy one block at a time, or save.

**"A coding assistant, not a coding agent."** It is read-only with respect to the
user's machine: it never runs code or edits files. The only thing it writes is a
file the user explicitly saves (Ctrl+S). Keep it that way unless the user says
otherwise. (Planned file *reading* is allowed and described in section 9.)

- Module name `codie`, Go module root is **`cli/`** (not the repo root). Run all
  `go` commands from `cli/`. Go version per `cli/go.mod`.
- Backend: **OpenRouter** (OpenAI-compatible chat completions, streamed over SSE).
- Two modes: **demo** (fake, offline, free; the default with no key) and **real**.

## 2. Repo map

```
README.md            user-facing docs (install, keys, modes, model tips, troubleshooting)
LLMs.md              this file
LICENSE              MIT
example.env          template for the 3 env vars; Codie does NOT load .env itself (user sources it)
cli/
  main.go            calls cmd.Execute()
  cmd/               CLI wiring (Cobra): flags, mode selection, `codie check`
    root.go            rootCmd, --demo / --model flags, loadConfig, starts the TUI
    runtime.go         resolveRuntime: demo-vs-real decision + what each mode wires up
    check.go           `codie check [--live]`: config report, optional 1 tiny real request
  backend/           talking to the AI. Knows NOTHING about the UI.
    backend.go         Event, Kind, Researcher interface (the stream contract), Collect
    config.go          Config, env var names, Validate, MaskedKey
    openrouter.go      the OpenRouter client (SSE parsing, errors, cancel, idle timeout)
    prompt.go          DefaultSystemPrompt (the step-by-step answer format the UI depends on)
    sanitize.go        Sanitize: strips terminal control characters from untrusted text
  tui/               the Bubble Tea UI + local data
    model.go           Model, Update/View, all three screens, streaming, selection, render
    blocks.go          parseAnswer: splits an answer into prose + fenced code blocks
    snippet.go         withBanner: Begin/End markers (as comments in the block's language) for `c`
    history.go         history screen (bubbles/list with search)
    store.go           Entry, Store interface, JSONStore (disk), memStore (memory)
    output.go          copyToClipboard, saveFile
    researcher.go      FakeResearcher (demo backend; implements backend.Researcher)
    demo.go            16 canned answers, DemoEntries (fake history), NewMemStore
    testdata/fuzz/     fuzz regression inputs (commit these)
    *_test.go          see section 8
```

Dependency direction: `cmd` → `tui` → `backend`. `backend` imports neither. `tui`
depends on `backend` only through the `Researcher` interface and `Event` type
(plus `backend.Sanitize`).

## 3. Runtime flow

1. `main` → `cmd.Execute` → `rootCmd.RunE`.
2. `loadConfig()` reads `OPENROUTER_API_KEY`, `CODIE_MODEL`, `CODIE_BASE_URL`
   (`--model` overrides the model).
3. `resolveRuntime(cfg, demo, demoSet, storePath, now)` picks the backend, in order:
   1. `--demo` → demo, even if a key is set
   2. `--demo=false` → real (error if key/model missing)
   3. no API key → demo
   4. key set → real (error if model missing). **A set key never silently falls
      back to demo**, so fake answers can't be mistaken for real ones.
4. It returns a `runtime{Researcher, Store, Label}`:
   - **demo:** `FakeResearcher{Delay: 700ms, ChunkDelay: 20ms}` + in-memory store
     seeded by `DemoEntries`. **Never touches disk.** Label `demo · sample answers…`.
   - **real:** `backend.NewOpenRouter(cfg)` + `JSONStore` at `DefaultStorePath()`
     (`os.UserConfigDir()/codie/history.json`). Label `OpenRouter · <model>`.
5. `tui.New(researcher, store).WithLabel(label)` run in `tea.NewProgram(m, tea.WithAltScreen())`.

### Asking a question (the streaming path)

`updateAsk` on Enter → `reqID++`, create a cancellable context, `showPane(...)`,
`m.events = researcher.Research(ctx, q)`, return `tea.Batch(spin.Tick, waitEvent(reqID, events))`.
`waitEvent` blocks on the channel and returns an `eventMsg{id, ev, ok}`.
`handleEvent` applies it and, for non-terminal events, re-issues `waitEvent`:

- `Text` → append to `m.raw`, re-render, keep following the bottom *only if the
  user was already at the bottom*.
- `Activity` → status line (nothing emits these yet; UI is ready for file tools).
- `Done` → `finish()`: reject empty answers, else append to history + store.
- `Error` or channel closed without a terminal event → `fail()`: keep the partial
  text on screen, show the reason, **never store/copy/save it**.
- Events whose `id != reqID` (or when not loading) are **stale and ignored**; that's
  how cancel works without races.

Esc while loading → `cancelRequest()` (cancels ctx → HTTP connection drops,
`reqID++`). Ctrl+C → `quit()` which also cancels first.

## 4. UI: screens and keys

Screens (`screen` enum): `screenAsk`, `screenAnswer`, `screenHistory`.

| Screen | Keys |
| --- | --- |
| **Ask** | Enter ask · Ctrl+R last answer · Ctrl+H history · Esc quit |
| **Answer** | `j`/`↓` first/next code block · `k`/`↑` last/previous block (wraps) · `c` copy selected block **with Begin/End markers** · `C` copy it **plain** · Ctrl+S save (selected block, else whole answer) · Ctrl+P "print entire response": question + whole answer as one `.md` (`startSaveMarkdown`, `markdownDoc`). Saves go to `CODIE_SAVE_DIR` (`tui.EnvSaveDir`, `WithSaveDir`, `savePath`) for relative names; unset = launch folder · Esc or `q` deselect, then back (`q` is not a letter on these screens; it is still typeable in the ask box and in search) · PgUp/PgDn/`b`/`f`/`u`/`d`/space scroll · `g`/`G` top/bottom |
| **Save prompt** | Enter save · Esc cancel |
| **History** | ↑/↓ move · `/` search · Enter open · Esc or `q` clear search, then back |
| **Everywhere** | **Ctrl+C quits** (handled first in `Update`; never copies) |

Notes:
- `j/k/↑/↓` select blocks only when `canSelect()` (answer finished, no error, ≥1
  selectable block). Otherwise they scroll the viewport (the bubbles viewport
  keymap already binds j/k).
- Selection never applies to streaming or failed answers: their blocks may be
  half-written.
- Ctrl+H is sometimes sent as Backspace by terminals/tmux. Known; a second
  binding may be needed.

### Answer rendering and selection

`renderPane()` runs `parseAnswer(m.raw)` and builds the viewport content: prose
wrapped to the width, code drawn by `renderCode` with a left gutter (`│ `
normally, **`▌ ` + background when selected**; the glyph difference means it
works without colour). It records each selectable block's start/end line in
`m.blocks` (`blockRef`: `start`/`end` of the block, plus `intro`, the first line of
the paragraph directly above it). Answers are a walkthrough (words, code, words,
code), so `revealSelection()` scrolls the minimum needed to show the **step**: the
introducing sentences *and* the block. If both fit, both come into view and a step
already fully on screen doesn't move the view; if only the block fits it is shown
whole with as much explanation above it as there is room for; a block taller than the
pane shows its top and PgDn reads the rest. (Prose between fences is always one
segment, so two prose segments are never adjacent; a block directly after another
block has `intro == start`.)

**Copy and save use the unwrapped `blockRef.text` / `m.raw`, never the rendered
text.** Wrapping must not leak into what the user copies.

**Copy markers (`snippet.go`).** `c` wraps the block in a 3-line box above and below,
"Codie Code Snippet: Begin" / "…:  End " (the two texts are the same length so the boxes
match), with a blank line between each box and the code. The boxes are comments in the
block's language (`//`, `#`, `--`, `/* */`, `<!-- -->`; unknown or no language
falls back to `#`, the style the user asked for) so pasting into a source file stays
valid; a test parses banner-wrapped Go, Python and shell. **JSON gets no markers** (a
comment would make it invalid; the status line says why). `C` copies the code byte for
byte with nothing around it. **Saving never adds markers.** The code between the markers is
never altered (tests check it byte for byte).

`startSave` captures `saveText`/`saveWhat` at open time; the filename suggestion
is `slug(question)` + `extFor(lang)` for a block (table `langExt`, anything else
`.txt` so a model can't steer the filename) or `.txt` for the whole answer.
`saveFile` uses `O_EXCL`: **never overwrites**.

### Parsing (`blocks.go`)

`parseAnswer` implements the Markdown fence rules closely enough for real model
output: ``` or `~~~`, info string (`Lang()` = first word, lowercased), closing
fence must be the same char and ≥ as long (so a longer outer fence can contain a
shorter inner one), ≤3 spaces indent, a line like "```foo```" is inline code not
a fence, and **an unclosed fence runs to the end** (models forget to close, and a
stream mid-way is in that state). Every prefix of a stream parses. Blank code
blocks aren't selectable. Trailing blank lines in a block are trimmed.

## 5. Backend details

### The Researcher contract (`backend.go`)

`Research(ctx, question) <-chan Event`: zero or more `Text`/`Activity`, then
**exactly one** `Done` or `Error`, then the channel closes. On ctx cancel the
implementation stops promptly and closes the channel (possibly with no terminal
event). It must **never block forever on a consumer that left** (select on
`ctx.Done()` when sending). `Collect(ch)` drains a stream to a string.
`FakeResearcher` and `OpenRouter` both honour this.

### OpenRouter client (`openrouter.go`)

- `POST {BaseURL}/chat/completions`, body `{model, messages:[system,user], stream:true, max_tokens?}`.
  Headers: `Authorization: Bearer <key>`, `Content-Type`, `Accept: text/event-stream`, `X-Title: codie`.
- **Redirects are refused** (`CheckRedirect` → `ErrUseLastResponse`); a 3xx is an error. The key never goes to another host.
- SSE parsed with `bufio.Reader.ReadString('\n')` (no 64 KB line limit). `:` comment
  lines (OpenRouter keep-alives) and `event:/id:/retry:` lines are ignored.
- Termination: `[DONE]` or EOF. `finish_reason` `length` / `content_filter` / `error`
  → `Error`; `stop`/`tool_calls`/other → `Done`; EOF with no finish and no `[DONE]` →
  `Error` ("connection closed before the answer finished"). An `error` object inside
  the stream → `Error`.
- If the reply is plain JSON instead of SSE (some providers ignore `stream`), it's
  handled by `readWhole`.
- **Idle watchdog:** `IdleTimeout` (default 60 s) cancels the request if no bytes
  arrive; reset on every line (so keep-alives count). It covers waiting for the
  first byte too. A user cancel is silent (no `Error` event).
- HTTP errors → `httpError`: 401/402/403/404/408,504/429/5xx/3xx each get an
  actionable message plus the server's `error.message` (HTML bodies are not shown).
- `redact()` (used by `errorf`/`httpError`): replaces the API key with `[redacted]`
  and runs `Sanitize`. **Every user-visible error string goes through it.**
- Text events are `Sanitize`d before being sent.

### Config (`config.go`)

Env: `OPENROUTER_API_KEY`, `CODIE_MODEL`, `CODIE_BASE_URL` (default
`https://openrouter.ai/api/v1`). **No default model, on purpose** (IDs and prices
change). `Validate()` errors are written to be acted on. Base URL must be `https`,
except loopback hosts (`localhost`, `127.0.0.1`, `::1`) may use `http` (used by
tests/fake servers). `MaskedKey()` shows only whether it's set and the last 4 chars.

### Prompt (`prompt.go`)

`DefaultSystemPrompt` asks for a **step-by-step walkthrough: words, code block, words,
code block**: open with 2–3 plain sentences saying what is being built and the
approach; then alternate one fenced block (one step, usually 5–25 lines) with 2–3
sentences that say what it did and lead into the next step; **end on a code block**
(no closing summary); say in the sentences which file a step belongs in; language on
every fence, never nest fences; plain prose only (no headings, bold, lists or tables, it's
a terminal); short comments in the code because the sentences carry the explanation;
assumptions stated in the sentences rather than asking questions. (An earlier version
asked for code only, with a single block when "simple"; tests now forbid that wording from
creeping back.) **The UI depends on this shape**: it finds the blocks for j/k selection and
scrolls each step into view with its introducing sentences, so changing the structure
means checking `renderPane`/`revealSelection`. The prompt tests only guard the wording; they
cannot prove a model obeys it. Only trying real models can.

### Sanitize (`sanitize.go`)

Strips C0 controls (incl. ESC, BEL, CR), DEL, and C1 controls; keeps `\n` and `\t`.
Why: terminals obey escape sequences, so model text could clear the screen,
overwrite output, or write the clipboard (OSC 52). Applied to: streamed text,
non-streaming text, all error text, and history read from disk (`openEntry`).
Found by fuzzing.

### `codie check` (`check.go`)

Prints the config (key masked), the mode, and problems. **Without `--live` it makes
no network request** (a test enforces this). `--live` sends one request:
system "Reply with exactly one word.", `max_tokens` 256 (`liveCheckMaxTokens`), question "Say: ok", 60 s
timeout. A reply cut off by that cap (`errors.Is(err, backend.ErrTruncated)`) still counts as
a **pass**: models that think before answering (GLM, DeepSeek, Haiku 5.5 all report
`reasoning`) can burn the cap on thinking and finish with `length`, but the request
clearly worked. Errors are printed once (`SilenceErrors` on the subcommand). Exit code is
non-zero on any problem.

## 6. Data

- `Entry{Question, Answer, At}`: one saved Q&A. Only **complete, successful** answers
  are stored.
- `Store` interface: `Load()`, `Append(Entry)`. `JSONStore` writes a temp file then
  renames (atomic); `memStore`/`NewMemStore` for demo and tests.
- History screen is newest-first; the list item title is the question,
  description the timestamp; `/` searches the question text.

## 7. Demo mode content (`demo.go`, `researcher.go`)

16 canned answers: 8 single-block, 7 multi-block (2–3 blocks, each with a sentence saying
which file it belongs in), and 1 prose-only. **Known gap: these are in the *older*
format (code with little or no prose), not the walkthrough the prompt now asks for
(2–3 sentences between every block), so demo mode does not yet preview what real answers
look like.** Converting them is a content rewrite (and each split Go answer must still compile). One
block has a very long line (wrapping); the **last** snippet is a generated 84-line
table (scrolling). **Order matters:** the fake backend serves them in order, so the
multi-block answers are at the *front* (the first answer has three blocks) so a few
questions in you can already try selecting between blocks. Tests guard this
(`TestMultiBlockAnswersComeEarlyInTheCycle`, `TestTheLastDemoAnswerIsTheTallScrollTest`,
`TestDemoAnswersAreInTheFormatThePromptAsksFor`). When you add or split a Go demo
answer, compile and run its blocks together before committing; split answers have
to be real code.
`FakeResearcher` streams the next snippet line by line, cycling and wrapping. In
demo mode, a question containing **`midfail`** streams ~half an answer then errors;
containing **`fail`** errors immediately. Failed requests don't consume a snippet.

## 8. Tests and how to run them

```sh
cd cli
gofmt -l . ; go vet ./...
go test -count=1 -race ./...                 # ~230 cases, a few seconds
go test -run xxx -fuzz=FuzzParseAnswer -fuzztime=20s ./tui/
go test -run xxx -fuzz=FuzzSanitize   -fuzztime=20s ./backend/
CGO_ENABLED=0 go build -o codie .            # portable static binary
```

- **No test touches the network or a real key.** Backend tests use `httptest`
  servers speaking OpenRouter's protocol. The key in tests is a fake string.
- `backend/openrouter_test.go`: protocol, errors, cancel, idle timeout, key safety, redirects.
- `tui/model_test.go`: harness + streaming, errors, cancel, history, save, fake backend,
  and **end-to-end tests that wire the real `OpenRouter` client into the real `Model`**
  against a local server.
- `tui/select_test.go`: block selection, copy, save, Ctrl+C, scrolling, sanitized history, and
  the walkthrough scrolling tests (`walkthrough(steps, codeLines)` builds a realistic answer).
  `tui/snippet_test.go`: the copy markers (design matched character for character; banner-wrapped
  Go/Python/shell still parse; JSON untouched).
- `tui/blocks_test.go`: parser table + prefix test + fuzz. `cmd/cmd_test.go`: mode
  selection and `check`.
- **Test harness** (`model_test.go`): `stubResearcher` returns a channel the test never
  writes to; tests inject events with `h.emit/text/done/fail` (`eventMsg` for the
  current `reqID`). `h.pump()`/`h.step()` drive a *real* stream. `h.typ("j")` /
  `h.key(tea.KeyDown)` send keys. `copyFn` is injected so the clipboard is never used.
- **Mutation habit:** passing tests only mean something if they can fail. After adding
  behaviour, break it in a scratch copy of `cli/` (e.g. remove redaction, make copy
  grab the wrong text) and confirm a test fails. The runner script is not in the
  repo; it was ~30 lines of Python that copies `cli/`, applies one string
  replacement, and runs `go test`. This caught real gaps several times.

### Gotchas that have already cost time

- Bubble Tea `Model` uses **value receivers**; helpers that mutate use `*Model` on
  the local copy and `Update` must `return m`. Don't put a `strings.Builder` in `Model`
  (copying it panics).
- `lipgloss` emits **no ANSI colour in tests** (no TTY). Assert on glyphs (`▌` vs `│`),
  not colours.
- `net/http` servers only notice a client disconnect **after the request body is read**.
  Test servers that block on `r.Context().Done()` must read the body first, or hang.
- Cancelling the context drops the connection even if the producing goroutine is
  stuck. To test for a goroutine leak, inspect goroutine stacks (see
  `waitForNoResearchGoroutines`), not just the disconnect.
- `bubbles/list` computes search results asynchronously via a `tea.Cmd`; tests need
  the `deliver` helper to feed `FilterMatchesMsg` back.
- Cobra prints usage and the error twice by default; the root and `check` set
  `SilenceUsage`, and `check` sets `SilenceErrors`.
- A stray `\r` or escape in model text is a terminal-safety bug, not a cosmetic one.
- Default Linux builds link libc dynamically; use `CGO_ENABLED=0` for a portable binary.
- There is no `tmux` in the agent environment, so the TUI can't be driven live.
  Verify rendering by printing `Model.View()` from a throwaway test (delete it after).

## 9. Decisions (made by the user) and roadmap

**Decided**
- Provider: OpenRouter. Key from env var only (never stored, printed, or logged).
- Model: set by `CODIE_MODEL` / `--model`; no default.
- Answers stream. Copy/save wait until finished.
- Each question is independent (no conversation memory yet).
- Answer format: a **walkthrough**, words / code block / words / code block, 2–3 sentences between
  blocks, so j/k step through the work gently. `j`/`k` select blocks, `c` copies one **with Begin/End
  markers** (comments in the block's language; the user's requested look), `C` copies it plain,
  **Ctrl+C is the stop signal** (not copy).
- File reading (not built yet): the model *asks* for files via tool calls;
  root = git root, else the current directory; **approval once per question**
  before the first read; read-only tools only.
- Models: the user must use **Haiku, GLM or DeepSeek, and only ones with a
  zero-data-retention (ZDR) endpoint on OpenRouter**. `example.env` lists the picks.
  **Codie does not enforce ZDR in code** (the user chose not to). Enforcement is the
  user's OpenRouter account privacy settings. Do not claim the app guarantees ZDR.
  Caveats from OpenRouter's docs: safety-retention endpoints exist, and Google Vertex
  endpoints are not strict ZDR (relevant to Haiku, which is served by Bedrock and
  Google). The ZDR endpoint list is public: `https://openrouter.ai/api/v1/endpoints/zdr`.
- License MIT. `README.md` stays non-technical; technical docs come after the backend.

**Done:** TUI (ask/answer/history), demo mode, streaming OpenRouter backend,
`codie check`, mode selection, prompt v1, block selection/copy/save, sanitizing.

**Next (in rough order)**
1. Run `codie check --live` and real questions on a cheap model (`example.env` and
   the README's "Choosing a model" list ZDR-capable Haiku/GLM/DeepSeek picks; the
   README deliberately does not list free models any more, and free models may
   retain prompts, so only throwaway questions on those). Then tune the prompt from
   real output.
2. File tools: read file / list dir / search; guardrails: stay inside the root,
   refuse symlink escapes, respect `.gitignore`, skip secrets (`.env`, keys), cap file
   sizes and tool calls per question; the approval prompt; emit `Activity` events
   (`reading path/to/file`); record which files were read alongside each history entry.
   Tool-call arguments stream in fragments and must be reassembled.
3. Open idea, not decided: `CODIE_MAX_TOKENS` to force truncation and see the
   "cut off" error on any model.
4. Technical docs for contributors; merge `feature/add-backend` to `main`.

**Known gaps / honest limits**
- The OpenRouter client has only been tested against local fakes, not the live service.
- Prose is rendered as plain wrapped text (no markdown styling); long code lines wrap.
- Bidirectional-text control characters (e.g. U+202E) are not stripped; only controls
  that drive the terminal are.
- The `v0.1` tag is local, points at the first commit, and predates the license.

## 10. Working agreements with this user

- **Ask before committing or pushing.** Commit only when asked. The agent
  environment has no GitHub credentials, so the user pushes (`! git push …`).
- The user wants to **see evidence** (tests, mutation checks, real outputs), not just
  claims. State plainly what is verified and what isn't.
- Never use or request their real API key; don't edit their shell config. Anything
  that spends money or sends their data off-machine needs their explicit say-so.
- Walk them through what you built; they asked to understand it, not just receive it.
- Don't hand-edit generated/testdata files; keep `gofmt` and `go vet` clean.
- Keep `README.md` user-facing and `LLMs.md` current.
