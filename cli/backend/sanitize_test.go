package backend

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitize(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"plain text untouched", "hello, world", "hello, world"},
		{"newlines and tabs kept", "a\n\tb\n", "a\n\tb\n"},
		{"unicode kept", "日本語 ✓ 🚀 naïve", "日本語 ✓ 🚀 naïve"},
		{"ESC removed, the rest is inert text", "x\x1b[31mred\x1b[0m", "x[31mred[0m"},
		{"OSC 52 clipboard write defanged", "\x1b]52;c;SGVsbG8=\x07", "]52;c;SGVsbG8="},
		{"BEL and other C0 controls removed", "a\x07b\x08c\x00d", "abcd"},
		{"carriage returns removed (CRLF becomes LF)", "a\r\nb\rc", "a\nbc"},
		{"DEL removed", "a\x7fb", "ab"},
		{"C1 controls removed (8-bit CSI)", "a\u009b31mb", "a31mb"},
		{"vertical tab and form feed removed", "a\vb\fc", "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sanitize(tt.in); got != tt.want {
				t.Fatalf("Sanitize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeSplitAcrossChunksStillInert(t *testing.T) {
	// An escape sequence split across stream chunks is sanitized per chunk, so
	// the ESC is dropped from whichever chunk holds it.
	got := Sanitize("clear:\x1b") + Sanitize("[2Jdone")
	if strings.Contains(got, "\x1b") {
		t.Fatalf("got %q", got)
	}
}

func FuzzSanitize(f *testing.F) {
	for _, seed := range []string{"", "a\x1b[2Jb", "\r\n", "\u009b", "\x00\x7f", "ok\ttab\nnl"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := Sanitize(in)
		if utf8.ValidString(in) && !utf8.ValidString(out) {
			t.Fatalf("valid UTF-8 became invalid: %q -> %q", in, out)
		}
		for _, r := range out {
			if r != '\n' && r != '\t' && (r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)) {
				t.Fatalf("control character %U survived in %q", r, out)
			}
		}
		if Sanitize(out) != out {
			t.Fatalf("Sanitize is not idempotent for %q", in)
		}
	})
}
