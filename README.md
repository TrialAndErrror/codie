# codie

A coding assistant, not a coding agent.

Codie lives in your terminal. You ask it how to build something, and it walks
you through it step by step: a few sentences explaining the next step, then a short
code block, then a few more sentences, then the next block. You can step through
the code blocks, copy one, or save it. It never changes your files unless you tell
it to save something.

With a real model, Codie can also look at your project to answer questions about it.
Before it reads anything it asks, once per question: press **y** to allow or **n** to
refuse. It can only read the folder you started Codie in and the folders inside it
(never anything above it), it skips secrets such as `.env` files and private keys and anything your
`.gitignore` excludes (this needs `git` installed), and
it only reads: it can't change or run anything. What it reads is sent to the model.

Press **Enter** on a finished answer to ask a follow-up. Codie remembers the
conversation so far (the most recent exchanges), so you can say "now make it handle
errors" without repeating yourself. Each follow-up shows its own answer, and every
answer is saved to your history separately. Asking a new question from the first screen
starts a fresh conversation.

Press **Ctrl+C** at any time, on any screen, to quit.

Codie can answer with a real AI model through [OpenRouter](https://openrouter.ai),
or run in **demo mode** with sample code snippets so you can try the interface
first. It doesn't read your project files: it only answers the question you type.

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
spinner; press **Esc** (or **q**) to cancel and go back if you change your mind.

Answers appear as they're written, and the view follows the newest text. Scroll
up to stop it following while you read. Nothing can be copied or saved until the
answer is finished.

When it's finished you see the whole response, written as a walkthrough: a few
sentences, a code block, a few sentences, the next block, each code block drawn
beside a thin bar. To work through it:

1. Press **j** (or **↓**) to jump to the first code block, or **k** (or **↑**)
   to jump to the last. The selected block gets a heavier bar and a highlighted
   background, and the footer says which one it is (for example
   `block 2/3 (go)`).
2. Keep pressing **j / k** (or **↓ / ↑**) to move between blocks. They wrap
   around at the ends. Each press brings the step into view together with the
   sentences that introduce it, so it reads like turning a page.
3. Press **c** to copy the selected block (with markers around it), **C** to copy
   just the code, or **Ctrl+S** to save it.

| Key | What it does |
| --- | --- |
| **j / ↓** | Select the first code block, then the next one |
| **k / ↑** | Select the last code block, then the previous one |
| **c** | Copy the selected block, wrapped in Begin / End markers |
| **C** | Copy the selected block as plain code, with no markers |
| **Enter** | Ask a follow-up. A prompt opens at the bottom; Enter sends it, Esc closes it |
| **Ctrl+S** | Save the selected block, or the whole answer if none is selected |
| **Ctrl+P** | Print the entire response: your question and the whole answer, saved together as one Markdown (`.md`) file |
| **PgUp / PgDn** (or **b / f**) | Scroll a page, even with a block selected |
| **u / d** | Scroll half a page |
| **g / G** | Jump to the top / bottom |
| **Esc** or **q** | Clear the selection; press again to go back |
| **Ctrl+C** | Quit |

If an answer has no code blocks (or while it's still arriving), **↑ / ↓** and
**j / k** simply scroll.

**Copying.** **c** copies the code in the selected block and nothing else: no
fence lines, no explanation, nothing from the other blocks, and never the
line-wrapping you see on screen. It puts a marker above and below, so once the
code is pasted you can see where the snippet starts and ends:

```
#############################
# Codie Code Snippet: Begin #
#############################

def main():
    pass

#############################
# Codie Code Snippet:  End  #
#############################
```

The markers are comments in the snippet's own language, so pasting them into a
source file doesn't break it: `//` for Go, JavaScript, Rust, Java and similar,
`--` for SQL and Haskell, `/* */` for CSS, `<!-- -->` for HTML, and `#` for
Python, shell and anything else. JSON has no comments, so it is copied without
markers. Press **C** instead when you want exactly the code with nothing around it,
for example when pasting straight into a file. If you press **c** or **C** before
selecting a block, Codie tells you to pick one first. If your terminal or paste
target turns tabs into spaces, that happens after the copy, not in Codie.

**Saving.** **Ctrl+S** asks for a filename and shows what it will save. With a
block selected it saves just that block, and the suggested name ends in the right
extension for its language (`how-do-i-read-a-file.go`, `.py`, `.sh` and so on).
With nothing selected it saves the whole answer as a `.txt` file. Edit the name if
you like and press **Enter**. The file goes in the folder you launched Codie
from. Codie will never overwrite an existing file; if the name is taken you'll
see a message and can try again with a different name. Press **Esc** to cancel.

**Ctrl+P** ("print entire response") works the same way but saves the whole
exchange: your question as a heading, then the full answer, as a single Markdown
file you can open anywhere. It always saves everything, even if a code block is
selected.

**Where saves go.** By default, in the folder you launched Codie from. To pick a
different default, set `CODIE_SAVE_DIR` (for example `CODIE_SAVE_DIR=~/codie-snippets`
in your `.env`). Codie creates the folder if needed, and a leading `~` means your
home folder. This applies to **Ctrl+S** and **Ctrl+P** whenever you type a plain
filename; a full path such as `/tmp/x.go` always goes exactly where you typed it.
Saved files never include the copy markers.

If a request fails partway, you'll see what arrived plus the reason, but its code
blocks can't be selected, copied or saved, because they may be incomplete. Failed
requests are never added to your history.

### 3. History

From the empty text box, press **Ctrl+H** to see every question you've asked,
newest first, each with the date and time you asked it.

| Key | What it does |
| --- | --- |
| **↑ / ↓** | Move through the list |
| **/** | Search your questions by word |
| **Enter** | Open the answer exactly as it was generated at the time |
| **Esc** or **q** | Go back (when a search is active, the first Esc clears it) |

To search, press **/**, type a word or two, then press **Enter** to apply the
filter. Press **Esc** to clear it and see everything again.

Opening an old answer works just like a new one: you can scroll, copy, and
save it. Press **Esc** (or **q**) to return to the list.

## Demo mode or a real model

The label under the title on the first screen always tells you which one you're
using: `demo · sample answers…` or `OpenRouter · <model>`.

**Demo mode** is the default when no API key is set. You get sample code
snippets (some with several files' worth of code blocks, one with no code at
all), a made-up history so the history screen has something to show, and
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

   Prefer a file? Copy `example.env` to `.env`, fill it in, and load it with
   `set -a; source .env; set +a`. Codie doesn't read `.env` files itself, so it has
   to be loaded into your shell first. `.env` is git-ignored.
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

   This sends one tiny request (capped at 256 tokens, a fraction of a cent) and tells
   you whether your key and model work.
6. Run `codie`. The label should now say `OpenRouter · <your model>`.

If a key is set but the model isn't, Codie tells you what's missing. It won't
quietly fall back to demo answers that you might mistake for real ones.

### Choosing a model

Codie has no default model, so you pick one, and any chat model on OpenRouter
works. Whether you need **zero data retention (ZDR)** is up to you. If what you
send is something you'd rather the provider didn't keep, choose a model that has a
ZDR endpoint. If you're just experimenting, you don't have to. (Free models are the
cheapest, but they may keep what you send them, so save those for throwaway
questions.)

Some models with a zero-data-retention endpoint on OpenRouter, checked 2026-11-08
(the list changes):

| Model ID | Family | ZDR providers | Rough cost per question |
| --- | --- | --- | --- |
| `z-ai/glm-5.3-flash` | GLM | 31 | about $0.0005 |
| `deepseek/deepseek-v4-flash` | DeepSeek | 10 | about $0.0003 |
| `anthropic/claude-haiku-5.5` | Haiku | 2 | about $0.0005 |

Also available: `deepseek/deepseek-v4-flash-0731` (19 providers) and
`anthropic/claude-haiku-4.5` (about 10 times the price of 5.5). "Per question"
assumes roughly 500 tokens in and 800 out. `example.env` has the same list.

Check the live list for any model yourself (no key needed; it returns every model
with a ZDR endpoint, so add a pattern to narrow it):

```sh
curl -s https://openrouter.ai/api/v1/endpoints/zdr \
  | grep -o '"model_id":"[^"]*"' | cut -d'"' -f4 | sort -u | grep -i glm
```

If you need ZDR, know what it does and doesn't mean here:

- **Codie doesn't enforce it.** Choosing one of these models doesn't by itself stop
  OpenRouter from routing you to a provider that keeps data. Turn ZDR on in your
  OpenRouter privacy settings: https://openrouter.ai/settings/privacy
- **"Zero retention" has fine print.** OpenRouter's documentation describes
  "safety retention" endpoints that may keep prompts to detect abuse, says Google
  Vertex endpoints are not strict ZDR as of October 2026 (Haiku is served by Amazon
  Bedrock and Google), and says all requests, including ZDR ones, may be screened
  for illegal content. Read it before relying on this for compliance:
  https://openrouter.ai/docs/guides/features/zdr

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
- **What comes back** is cleaned before it reaches your screen: terminal control
  characters (the sequences that can clear your display, rewrite what's already
  on it, or put text on your clipboard) are removed from everything the model
  sends, and from history read back from disk.
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

**"…guardrails or data-policy settings exclude every endpoint for it (404)."**
The model exists, but your OpenRouter account's settings (a workspace guardrail or
a privacy / zero-data-retention setting) don't allow it. Pick a model your
settings allow, or change the setting named in the message details at
openrouter.ai.

**"…rate limiting you (429)."** Wait a moment and ask again.

**"OpenRouter stopped responding."** No data arrived for a minute. Try again;
if it keeps happening, try a different model.

**"…cut off because the model hit its length limit."** The answer was too long
for the model. Ask a narrower question.

**Ctrl+H does nothing.** Some terminals (and tmux setups) send Ctrl+H as
Backspace, so Codie can't tell the difference. Try a different terminal or
terminal settings. You can still ask questions and use **Ctrl+R** for the last
answer without it.

**Copy doesn't seem to work.** (Copying is **c**, on a selected code block;
**Ctrl+C** quits.) Codie uses your system clipboard when it can
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

## License

[MIT](LICENSE). Tinker away.
