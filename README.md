# codie

A coding assistant, not a coding agent.

Codie lives in your terminal. You ask it a question about how to build
something, and it answers with a block of code you can scroll through, copy, or
save. It never touches your files unless you tell it to save an answer.

> **Version 0.1: preview.** The interface is complete and ready to try, but
> Codie isn't connected to an AI yet. For now it replies with a rotating set of
> sample code snippets (Avogadro's number, recursive factorials, the
> Mandelbrot set, and more) so you can get a feel for how everything works.
> Real answers are coming next.

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

Once the answer arrives, you can scroll through it and act on it:

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

## Sample mode and your history

In this preview, Codie runs in **sample mode** by default. It starts with a set
of pretend past questions so the history screen has something to show, and
anything you ask during a session is kept only until you quit. Nothing is written
to your computer.

To try real history saving, start Codie with:

```sh
go run . --demo=false
```

Your questions and answers will then be stored in a file called `history.json`
in Codie's folder inside your user config directory (on Linux, that's
`~/.config/codie/`), and they'll be there next time you open the app.

## Troubleshooting

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

- Connecting Codie to a real AI so answers are actually about your question
- Technical documentation for contributors

## License

[MIT](LICENSE). Tinker away.
