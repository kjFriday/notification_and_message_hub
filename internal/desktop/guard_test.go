// ==== ACME Inc. portfolio ===============================================
// Project: notification_and_message_hub | File: internal/desktop/guard_test.go
// About: Tests for package desktop.
// Copyright (c) 2026 kjFriday | SPDX-License-Identifier: AGPL-3.0-only
// You may change this file under the AGPL-3.0. The stamp at the end marks
// the version kjFriday wrote. Verify with the portfolio's stamp.py --check.
// Reused code, if any, is mapped in PROVENANCE.md.
// ========================================================================

package desktop

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGuard(t *testing.T) {
	token := NewToken()
	if len(token) != 32 || token == NewToken() {
		t.Fatalf("token %q", token)
	}
	h := Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("page")) }), token, "127.0.0.1:5000")
	do := func(path, host string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = host
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := do("/", "127.0.0.1:5000", nil); w.Code != 403 {
		t.Fatalf("no cookie: %d", w.Code)
	}
	if w := do("/__open?t=wrong", "127.0.0.1:5000", nil); w.Code != 403 {
		t.Fatalf("wrong token: %d", w.Code)
	}
	if w := do("/", "evil.example:5000", &http.Cookie{Name: Cookie, Value: token}); w.Code != 403 {
		t.Fatalf("wrong host: %d", w.Code)
	}
	w := do("/__open?t="+token, "127.0.0.1:5000", nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("open: %d %q", w.Code, w.Header().Get("Location"))
	}
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Value != token {
		t.Fatalf("cookie %+v", c)
	}
	if w := do("/", "127.0.0.1:5000", c); w.Code != 200 || w.Body.String() != "page" {
		t.Fatalf("with cookie: %d", w.Code)
	}
	if w := do("/", "127.0.0.1:5000", &http.Cookie{Name: Cookie, Value: "x" + token[1:]}); w.Code != 403 {
		t.Fatalf("forged cookie: %d", w.Code)
	}
}

// ==== stamp v1 | author=kjFriday | hmac-sha256=3ce5f3183bdc9ccde9a8b3a9d3cdfcae690dcbe45c1be4649815e27bbadd8d05 ====
