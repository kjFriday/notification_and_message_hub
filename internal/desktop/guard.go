// Package desktop shows an ACME program's web pages in its own native window (Edge WebView2), served
// by an in-process server on 127.0.0.1 that only that window can use.
package desktop

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
)

// Cookie carries the per-run token after the window's first request.
const Cookie = "acme_session"

// NewToken is 32 random hex characters, made fresh for every run.
func NewToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Guard lets a request through only from the program's own window: the first navigation to
// /__open?t=TOKEN sets an HttpOnly, SameSite=Strict cookie; after that every request needs the cookie
// and the exact Host. A web page in a browser can neither read the token nor send the cookie, so it
// cannot use (or change anything through) the local server.
func Guard(next http.Handler, token, host string) http.Handler {
	ok := func(s string) bool { return subtle.ConstantTimeCompare([]byte(s), []byte(token)) == 1 }
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/__open" {
			if !ok(r.URL.Query().Get("t")) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: Cookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		c, err := r.Cookie(Cookie)
		if err != nil || !ok(c.Value) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
