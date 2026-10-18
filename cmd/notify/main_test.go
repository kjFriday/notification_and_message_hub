// ==== ACME Inc. portfolio ===============================================
// Project: notification_and_message_hub | File: cmd/notify/main_test.go
// About: Tests for package main.
// Copyright (c) 2026 kjFriday | SPDX-License-Identifier: AGPL-3.0-only
// You may change this file under the AGPL-3.0. The stamp at the end marks
// the version kjFriday wrote. Verify with the portfolio's stamp.py --check.
// Reused code, if any, is mapped in PROVENANCE.md.
// ========================================================================

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"acme/hub/internal/app"
)

func get(t *testing.T, c *http.Client, u string) (int, string) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestPagesOnTheSnapshot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.db")
	h := &hub{dbPath: missing, outDir: t.TempDir()}
	srv := httptest.NewServer(routes(h))
	defer srv.Close()
	c := srv.Client()

	code, body := get(t, c, srv.URL+"/")
	if code != 200 || !strings.Contains(body, "mock snapshot") || !strings.Contains(body, "pending of 600 events") {
		t.Fatalf("queue: %d %.300s", code, body)
	}
	if code, body := get(t, c, srv.URL+"/deliveries"); code != 200 || !strings.Contains(body, "No pass yet") {
		t.Fatalf("deliveries before: %d", code)
	}
	resp, err := c.PostForm(srv.URL+"/dispatch", url.Values{})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("dispatch: %v %v", err, resp)
	}
	resp.Body.Close()
	if code, body := get(t, c, srv.URL+"/"); code != 200 || !strings.Contains(body, "nothing written back") {
		t.Fatalf("after dispatch: %d", code)
	}
	if code, body := get(t, c, srv.URL+"/deliveries"); code != 200 || !strings.Contains(body, "<td class=\"send\">send</td>") {
		t.Fatalf("deliveries after: %d", code)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("a missing database was created")
	}
	for _, p := range []string{"/favicon.ico", "/icon-32.png", "/icon-256.png"} {
		if code, _ := get(t, c, srv.URL+p); code != 200 {
			t.Fatalf("%s: %d", p, code)
		}
	}
	code, body = get(t, c, srv.URL+"/about")
	if code != 200 || !strings.Contains(body, app.Version) || !strings.Contains(body, "CC BY 2.5") {
		t.Fatalf("about: %d", code)
	}
	resp, _ = c.PostForm(srv.URL+"/about", url.Values{"db": {missing}})
	if resp.StatusCode != 400 {
		t.Fatalf("switching to a missing database: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestOptions(t *testing.T) {
	o, err := parse([]string{"--db", "x.db", "-install", "--root", "sb", "-quiet"}, io.Discard)
	if err != nil || o.db != "x.db" || !o.inst || o.root != "sb" || !o.quiet {
		t.Fatalf("%+v %v", o, err)
	}
}

// ==== stamp v1 | author=kjFriday | hmac-sha256=2606fe2f7638c65d7172fc81c0f922310d100aa400cfb7a37c9b7efc4e3a82d7 ====
