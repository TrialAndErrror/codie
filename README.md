# codie

A coding assistant, not a coding agent.

Codie lives in your terminal. You ask it a question about how to build
something, and it answers with a block of code you can scroll through, copy, or
save. It never touches your files unless you tell it to save an answer.

> **Preview.** Codie can answer with a real AI model through
> [OpenRouter](https://openrouter.ai), or run in **demo mode** with sample code
> snippets so you can try the interface first. It doesn't read your project
> files yet; that's coming.

## Getting started

You'll need [Go](https://go.dev/dl/) installed (version 1.27 or newer).

```sh
cd cli
go run .
```

Or build it once and run it from anywhere:

```sh
cd cli
go build -o codie .
./codie
```

Press **Esc** on the first screen to quit.

## How it works

Codie has three screens. You move between them with a few keys, and the hints
at the bottom of every screen remind you what's available.

### 1. Ask

When you start Codie, you get an empty text box. Type your question and press
**Enter**.

| Key | What it does |
| --- | --- |
| **Enter** | Ask your question |
| **Ctrl+R** | Jump back to your most recent answer |
| **Ctrl+H** | Open your history |
| **Esc** | Quit |

### 2. Answer

Your answer appears in a full-screen pane. While Codie is working you'll see a
spinner; press **Esc** to cancel and go back if you change your mind.

Answers appear as they're written. The view follows the newest text; scroll up
to stop it following while you read. Copy and save wait until the answer is
finished.

When it's finished, you can scroll through it and act on it:

| Key | What it does |
| --- | --- |
| **↑ / ↓** (or **k / j**) | Scroll a line at a time |
| **PgUp / PgDn** (or **b / f**) | Scroll a page at a time |
| **u / d** | Scroll half a page |
| **Ctrl+C** | Copy the whole answer to your clipboard |
| **Ctrl+S** | Save the answer to a file |
| **Esc** | Go back to the empty text box |

**Copying.** Ctrl+C copies the entire answer exactly as written, ready to
paste into your editor. If your terminal or paste target turns tabs into
spaces, that happens after the copy, not in Codie.

**Saving.** Ctrl+S asks for a filename, pre-filled from your question (for
example `how-do-i-read-a-file.txt`). Edit it if you like and press **Enter**.
The file is saved in the folder you launched Codie from. Codie will never
overwrite an existing file; if the name is taken, you'll see a message and can
try again with a different name. Press **Esc** to cancel.

If a request fails, you'll see the error in the pane instead of an answer, and
it won't be added to your history.

### 3. History

From the empty text box, press **Ctrl+H** to see every question you've asked,
newest first, each with the date and time you asked it.

| Key | What it does |
| --- | --- |
| **↑ / ↓** | Move through the list |
| **/** | Search your questions by word |
| **Enter** | Open the answer exactly as it was generated at the time |
| **Esc** | Go back (when a search is active, the first Esc clears it) |

To search, press **/**, type a word or two, then press **Enter** to apply the
filter. Press **Esc** to clear it and see everything again.

Opening an old answer works just like a new one: you can scroll, copy, and
save it. Press **Esc** to return to the list.

## Demo mode or a real model

The label under the title on the first screen always tells you which one you're
using: `demo · sample answers…` or `OpenRouter · <model>`.

**Demo mode** is the default when no API key is set. You get sample code
snippets, a made-up history so the history screen has something to show, and
nothing is sent anywhere or saved to your computer. You can also force it with
`codie --demo`, even if a key is set.

### Using a real model

1. Create an API key at [openrouter.ai](https://openrouter.ai).
2. Pick a model from OpenRouter's model list and copy its ID.
3. Set both in your shell:

   ```sh
   export OPENROUTER_API_KEY=your-key-here
   export CODIE_MODEL=the-model-id-you-picked
   ```

   (You can pass `--model some/model-id` instead of setting `CODIE_MODEL`.)
4. **Check your setup before asking anything:**

   ```sh
   codie check
   ```

   This shows what Codie will do and whether anything is missing. It makes **no
   network requests** and costs nothing. Your key is never printed, only whether
   it's set and its last four characters.
5. **Optionally, test the connection:**

   ```sh
   codie check --live
   ```

   This sends one tiny request (a few tokens, a fraction of a cent) and tells
   you whether your key and model work.
6. Run `codie`. The label should now say `OpenRouter · <your model>`.

If a key is set but the model isn't, Codie tells you what's missing. It won't
quietly fall back to demo answers that you might mistake for real ones.

### What gets sent where

In real mode:

- **Your questions** are sent to OpenRouter, and on to the company that runs the
  model you chose. Don't type anything you wouldn't want them to see.
- **Your API key** is read from your environment and sent only to OpenRouter,
  in the request itself. Codie never saves it, never prints it, and strips it
  out of any error message. It refuses to send it over plain `http://` (except
  to your own machine, for testing) and refuses to follow redirects.
- **Your history** is saved to a file called `history.json` in Codie's folder
  inside your user config directory (on Linux, `~/.config/codie/`), and will be
  there next time you open the app. Demo mode never writes it.
- Codie doesn't read or send any of your project files yet.

If an answer fails partway (the connection drops, the model is cut off, or it
returns nothing), you'll see what arrived plus the reason. Codie won't save or
copy an incomplete answer, so it can't end up in your history or your editor by
mistake.

## Troubleshooting

**"OpenRouter rejected your API key (401)."** The key is wrong, expired, or
revoked. Check `OPENROUTER_API_KEY` with `codie check`.

**"…out of credits (402)."** Add credits to your OpenRouter account.

**"…couldn't find the model… (404)."** The model ID in `CODIE_MODEL` (or
`--model`) doesn't exist or isn't available to you. Copy the ID again from
OpenRouter's model list.

**"…rate limiting you (429)."** Wait a moment and ask again.

**"OpenRouter stopped responding."** No data arrived for a minute. Try again;
if it keeps happening, try a different model.

**"…cut off because the model hit its length limit."** The answer was too long
for the model. Ask a narrower question.

**Ctrl+H does nothing.** Some terminals (and tmux setups) send Ctrl+H as
Backspace, so Codie can't tell the difference. Try a different terminal, or
let us know. A second shortcut is an easy addition.

**Copy doesn't seem to work.** Codie uses your system clipboard when it can
(`xclip`, `xsel`, or `wl-copy` on Linux; built in on macOS and Windows). If none
is installed it falls back to a terminal feature called OSC 52, which most
modern terminals support but some disable by default. Installing `xclip` or
`wl-clipboard` is the simplest fix on Linux.

**Long lines look wrapped.** Answers are wrapped to fit your window so nothing
runs off the screen. The copied or saved text is never wrapped.

**The binary won't run on another machine** (for example, an error about a
missing `libc` or `GLIBC` version). By default, Go links against your system's C
library on Linux, so a binary built on one machine can fail on another. Turn that
off to get a fully self-contained binary with nothing to install:

```sh
cd cli
CGO_ENABLED=0 go build -o codie .
```

(Note it's `CGO_ENABLED=0`: *disabling* cgo is what makes the binary portable.)
You can check the result with `ldd codie`, which should say "not a dynamic
executable". Codie doesn't need cgo for anything, so nothing is lost.

You can also build for another system from the one you're on by setting the
target operating system and processor:

```sh
CGO_ENABLED=0 GOOS=linux   GOARCH=arm64  go build -o codie-linux-arm64 .
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64  go build -o codie-mac-apple-silicon .
CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64  go build -o codie-mac-intel .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64  go build -o codie.exe .
```

One thing a portable binary can't carry with it is the clipboard helper: on
Linux, copying still uses `xclip`, `xsel`, or `wl-copy` if they're installed on
the machine you run it on (otherwise it falls back to OSC 52, described above).

## What's next

- A tuned prompt, so answers are consistently a single block of code
- Letting the model read files in your project (with your permission, one
  question at a time)
- Selecting and copying an individual code block from an answer
- Technical documentation for contributors

## License

[MIT](LICENSE). Tinker away.
