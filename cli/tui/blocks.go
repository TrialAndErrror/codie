package tui

import "strings"

// segment is one piece of an answer: either prose or a fenced code block.
type segment struct {
	Code bool
	Text string // prose as written, or the code with the fences removed
	Info string // for code: the opening fence's info string, e.g. "go" or "go main.go"
}

// Lang is the language named on the opening fence ("go", "python", ...), or "".
func (s segment) Lang() string {
	f := strings.Fields(s.Info)
	if len(f) == 0 {
		return ""
	}
	return strings.ToLower(f[0])
}

// parseAnswer splits an answer into prose and fenced code blocks.
//
// It follows the Markdown fence rules closely enough for real model output:
// ``` or ~~~ fences, an info string after the opening fence, a closing fence of
// the same character at least as long as the opening one (so a longer outer
// fence can contain a shorter inner one), and up to three spaces of indent.
// A fence that is never closed runs to the end of the answer: models do forget
// to close, and a stream being parsed mid-way is in exactly that state.
//
// The result is safe to compute on every chunk of a stream: any prefix of an
// answer parses without error.
func parseAnswer(raw string) []segment {
	var (
		segs    []segment
		prose   []string
		code    []string
		inCode  bool
		fenceCh byte
		fenceN  int
		info    string
	)
	flushProse := func() {
		if text := trimBlankLines(strings.Join(prose, "\n")); text != "" {
			segs = append(segs, segment{Text: text})
		}
		prose = nil
	}
	flushCode := func() {
		segs = append(segs, segment{Code: true, Text: strings.TrimRight(strings.Join(code, "\n"), "\n"), Info: info})
		code = nil
	}

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !inCode {
			if ch, n, rest, ok := openingFence(line); ok {
				flushProse()
				inCode, fenceCh, fenceN, info = true, ch, n, strings.TrimSpace(rest)
				continue
			}
			prose = append(prose, line)
			continue
		}
		if isClosingFence(line, fenceCh, fenceN) {
			flushCode()
			inCode = false
			continue
		}
		code = append(code, line)
	}
	if inCode {
		flushCode()
	} else {
		flushProse()
	}
	return segs
}

// selectable reports whether a segment can be stepped to and copied: a code
// block with something in it.
func (s segment) selectable() bool {
	return s.Code && strings.TrimSpace(s.Text) != ""
}

// openingFence recognises a line that starts a fenced block.
func openingFence(line string) (ch byte, n int, rest string, ok bool) {
	body, indentOK := stripIndent(line)
	if !indentOK || len(body) < 3 || (body[0] != '`' && body[0] != '~') {
		return 0, 0, "", false
	}
	ch = body[0]
	for n < len(body) && body[n] == ch {
		n++
	}
	if n < 3 {
		return 0, 0, "", false
	}
	rest = body[n:]
	// "```foo```" on one line is inline code, not a fence.
	if ch == '`' && strings.Contains(rest, "`") {
		return 0, 0, "", false
	}
	return ch, n, rest, true
}

// isClosingFence recognises the line that ends a block opened with n of ch.
func isClosingFence(line string, ch byte, n int) bool {
	body, indentOK := stripIndent(line)
	if !indentOK || len(body) < n {
		return false
	}
	run := 0
	for run < len(body) && body[run] == ch {
		run++
	}
	return run >= n && strings.TrimSpace(body[run:]) == ""
}

// stripIndent removes up to three leading spaces. Four or more means the line
// is an indented code block or deeper indentation, never a fence.
func stripIndent(line string) (string, bool) {
	i := 0
	for i < len(line) && line[i] == ' ' {
		i++
	}
	if i > 3 {
		return "", false
	}
	return line[i:], true
}

// trimBlankLines drops blank (or whitespace-only) lines from both ends of s,
// leaving the indentation of the first real line alone.
func trimBlankLines(s string) string {
	ls := strings.Split(s, "\n")
	for len(ls) > 0 && strings.TrimSpace(ls[0]) == "" {
		ls = ls[1:]
	}
	for len(ls) > 0 && strings.TrimSpace(ls[len(ls)-1]) == "" {
		ls = ls[:len(ls)-1]
	}
	return strings.Join(ls, "\n")
}
