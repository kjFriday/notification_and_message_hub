package textsafe

import (
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	esc = "\x1b"
	csi = "\u009b" // the single-character form of ESC [
)

func TestBothSpellingsOfCSIAreRemoved(t *testing.T) {
	if got := Clean(esc + "[2Jinvoice"); got != "[2Jinvoice" {
		t.Errorf("ESC: %q", got)
	}
	if got := Clean(csi + "2Jinvoice"); got != "2Jinvoice" {
		t.Errorf("CSI: %q", got)
	}
}

func TestDelAndWholeC1BlockAreRemoved(t *testing.T) {
	for c := rune(0x7F); c < 0xA0; c++ {
		if got := Clean("a" + string(c) + "b"); got != "ab" {
			t.Errorf("%#x: %q", c, got)
		}
	}
}

func TestC0ControlsAreRemovedExceptTabAndNewline(t *testing.T) {
	for c := rune(0); c < 0x20; c++ {
		want := "ab"
		if c == '\t' || c == '\n' {
			want = "a" + string(c) + "b"
		}
		if got := Clean("a" + string(c) + "b"); got != want {
			t.Errorf("%#x: %q, want %q", c, got, want)
		}
	}
}

func TestSeparatorsAndBidiControlsAreRemoved(t *testing.T) {
	cs := []rune{0x2028, 0x2029}
	for c := rune(0x202A); c <= 0x202E; c++ {
		cs = append(cs, c)
	}
	for c := rune(0x2066); c <= 0x2069; c++ {
		cs = append(cs, c)
	}
	for _, c := range cs {
		if got := Clean("order" + string(c) + "42"); got != "order42" {
			t.Errorf("%#x: %q", c, got)
		}
	}
}

func TestOrdinaryTextIsUntouched(t *testing.T) {
	plain := "Order 1042 for Zoë Müller: 3 × Widget, 東京 office \U0001F4E6\tnote\nline two"
	if got := Clean(plain); got != plain {
		t.Errorf("%q", got)
	}
}

func TestLengthIsCappedAfterRemoval(t *testing.T) {
	if got := utf8.RuneCountInString(Clean(strings.Repeat("x", 500))); got != Limit {
		t.Errorf("default cap: %d", got)
	}
	if got := CleanN("abcdef", 3); got != "abc" {
		t.Errorf("cap 3: %q", got)
	}
	if got := Clean(strings.Repeat(esc, 300) + "ok"); got != "ok" {
		t.Errorf("removal before cap: %q", got)
	}
	if got := len(Strip(strings.Repeat("y", 500))); got != 500 {
		t.Errorf("no cap: %d", got)
	}
}

// The cap counts characters, not bytes.
func TestCapCountsCharacters(t *testing.T) {
	if got := CleanN("東京\U0001F4E6", 2); got != "東京" {
		t.Errorf("%q", got)
	}
}

func TestZeroOrNegativeLimitGivesEmpty(t *testing.T) {
	for _, n := range []int{0, -1, -2} {
		if got := CleanN("hello", n); got != "" {
			t.Errorf("limit %d: %q", n, got)
		}
	}
}

func TestEmptyStaysEmpty(t *testing.T) {
	if got := Clean(""); got != "" {
		t.Errorf("%q", got)
	}
}

func TestInvalidUTF8BecomesReplacement(t *testing.T) {
	if got := Clean("a\xffb"); got != "a�b" {
		t.Errorf("%q", got)
	}
}
