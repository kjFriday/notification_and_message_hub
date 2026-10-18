// ==== ACME Inc. portfolio ===============================================
// Project: notification_and_message_hub | File: textsafe/textsafe.go
// About: Package textsafe guards text that arrives from outside: it drops invisible control...
// Copyright (c) 2026 kjFriday | SPDX-License-Identifier: AGPL-3.0-only
// You may change this file under the AGPL-3.0. The stamp at the end marks
// the version kjFriday wrote. Verify with the portfolio's stamp.py --check.
// Reused code, if any, is mapped in PROVENANCE.md.
// ========================================================================

// Package textsafe guards text that arrives from outside: it drops invisible
// control characters and caps the length.
//
// Removed: C0 controls except tab and newline, DEL, the whole C1 block
// (U+0080-U+009F, which includes U+009B, a one-character escape-sequence
// start), the line/paragraph separators U+2028/U+2029, and the bidirectional
// embedding, override and isolate controls (U+202A-U+202E, U+2066-U+2069).
// Everything else, including ordinary non-ASCII text, passes through
// unchanged. Invalid UTF-8 bytes become U+FFFD.
//
// A Go port of the CRUD hub's textsafe.py; the rules and the cap match it.
package textsafe

import "strings"

// Limit is the shared default cap, in characters (runes).
const Limit = 240

// Refused reports whether r is removed.
func Refused(r rune) bool {
	return (r < 0x20 && r != '\t' && r != '\n') ||
		(0x7F <= r && r <= 0x9F) ||
		r == 0x2028 || r == 0x2029 ||
		(0x202A <= r && r <= 0x202E) ||
		(0x2066 <= r && r <= 0x2069)
}

// Clean returns text without refused characters, cut to at most Limit
// characters.
func Clean(text string) string { return CleanN(text, Limit) }

// CleanN is Clean with a cap of limit characters; a cap of zero or less
// gives "". The cap applies after removal.
func CleanN(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	return clean(text, limit)
}

// Strip removes refused characters with no length cap.
func Strip(text string) string { return clean(text, -1) }

func clean(text string, limit int) string {
	var b strings.Builder
	n := 0
	for _, r := range text {
		if Refused(r) {
			continue
		}
		if n == limit {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// ==== stamp v1 | author=kjFriday | hmac-sha256=da090314b2f17573563dc22715c72fce07f152ae5fb300d64b7fca0b57f3fff3 ====
