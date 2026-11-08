package backend

import "strings"

// Sanitize removes control characters from text that came from the network
// before it is shown in a terminal or copied anywhere.
//
// Why: a terminal obeys escape sequences, so a model reply (or text a model was
// tricked into repeating) containing them can clear the screen, move the
// cursor over earlier output, retitle the window, or even write to the
// clipboard (OSC 52). Newlines and tabs are kept; everything else below space,
// DEL, and the C1 range (which includes the 8-bit CSI/OSC introducers) is
// dropped, as are carriage returns, so CRLF becomes LF.
//
// A sequence like ESC [ 3 1 m loses its ESC and is left as harmless visible
// text, which is also how an escape split across stream chunks degrades.
func Sanitize(s string) string {
	clean := true
	for _, r := range s {
		if isControl(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, s)
}

func isControl(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20, r == 0x7f: // C0 controls (including ESC, BEL, CR) and DEL
		return true
	case r >= 0x80 && r <= 0x9f: // C1 controls
		return true
	}
	return false
}
