package tui

import "strings"

// Copied code is wrapped in a banner above and below it, so that once it has been
// pasted somewhere (a chat, a file, a doc) it is obvious where Codie's snippet
// starts and ends:
//
//	#############################
//	# Codie Code Snippet: Begin #
//	#############################
//
//	def main():
//	    pass
//
//	#############################
//	# Codie Code Snippet:  End  #
//	#############################
//
// The banner is written as a comment in the snippet's own language (taken from
// the language on its code fence), so pasting it into a source file leaves the
// file valid. A language with no comment syntax (JSON) gets no banner.

const (
	// bannerBegin and bannerEnd are the same length so the two boxes match.
	bannerBegin = "Codie Code Snippet: Begin"
	bannerEnd   = "Codie Code Snippet:  End "
)

// commentStyle says how to draw a banner in one family of comment syntax.
type commentStyle struct {
	open, close string                 // wrapped around the text line: open + text + close
	border      func(width int) string // a full-width border line, itself a valid comment
}

func repeat(s string, n int) string { return strings.Repeat(s, max(n, 0)) }

var (
	hashStyle  = commentStyle{"# ", " #", func(w int) string { return repeat("#", w) }}
	slashStyle = commentStyle{"// ", " //", func(w int) string { return repeat("/", w) }}
	dashStyle  = commentStyle{"-- ", " --", func(w int) string { return repeat("-", w) }}
	cssStyle   = commentStyle{"/* ", " */", func(w int) string { return "/" + repeat("*", w-2) + "/" }}
	htmlStyle  = commentStyle{"<!-- ", " -->", func(w int) string { return "<!-- " + repeat("=", w-9) + " -->" }}
)

// languageStyles maps a fence language to its comment style. Anything not listed
// (including no language at all) falls back to the # style.
var languageStyles = map[string]commentStyle{
	// # comments
	"python": hashStyle, "py": hashStyle, "sh": hashStyle, "bash": hashStyle, "shell": hashStyle,
	"zsh": hashStyle, "ruby": hashStyle, "rb": hashStyle, "yaml": hashStyle, "yml": hashStyle,
	"toml": hashStyle, "dockerfile": hashStyle, "make": hashStyle, "makefile": hashStyle,
	"perl": hashStyle, "r": hashStyle, "powershell": hashStyle,
	// // comments
	"go": slashStyle, "golang": slashStyle, "js": slashStyle, "javascript": slashStyle,
	"ts": slashStyle, "typescript": slashStyle, "jsx": slashStyle, "tsx": slashStyle,
	"java": slashStyle, "c": slashStyle, "cpp": slashStyle, "c++": slashStyle,
	"csharp": slashStyle, "cs": slashStyle, "rust": slashStyle, "rs": slashStyle,
	"swift": slashStyle, "kotlin": slashStyle, "kt": slashStyle, "php": slashStyle,
	"scala": slashStyle, "dart": slashStyle, "zig": slashStyle, "proto": slashStyle,
	// -- comments
	"sql": dashStyle, "haskell": dashStyle, "hs": dashStyle, "lua": dashStyle,
	// /* */ comments
	"css": cssStyle, "scss": cssStyle, "less": cssStyle,
	// <!-- --> comments
	"html": htmlStyle, "xml": htmlStyle, "svg": htmlStyle, "markdown": htmlStyle, "md": htmlStyle,
}

// noComments lists languages where a comment would make the snippet invalid.
var noComments = map[string]bool{"json": true}

// withBanner wraps code in begin/end banners drawn as comments in lang. The code
// itself is untouched. ok is false for a language with no comment syntax, in
// which case code is returned unchanged.
func withBanner(lang, code string) (wrapped string, ok bool) {
	if noComments[lang] {
		return code, false
	}
	style, found := languageStyles[lang]
	if !found {
		style = hashStyle
	}
	return banner(style, bannerBegin) + "\n\n" + code + "\n\n" + banner(style, bannerEnd), true
}

// banner draws one three-line box around text.
func banner(s commentStyle, text string) string {
	width := len(s.open) + len(text) + len(s.close)
	border := s.border(width)
	return border + "\n" + s.open + text + s.close + "\n" + border
}
