// ==== ACME Inc. portfolio ===============================================
// Project: notification_and_message_hub | File: cmd/notify/main.go
// About: Command acme-notify is the ACME Notify Hub window: the pending-event queue, a Dispatch...
// Copyright (c) 2026 kjFriday | SPDX-License-Identifier: AGPL-3.0-only
// You may change this file under the AGPL-3.0. The stamp at the end marks
// the version kjFriday wrote. Verify with the portfolio's stamp.py --check.
// Reused code, if any, is mapped in PROVENANCE.md.
// ========================================================================

// Command acme-notify is the ACME Notify Hub window: the pending-event queue, a Dispatch button, and
// the last pass's deliveries with their throttle band and retry time.
//
// Events and customers come from the shared ACME database, opened by path; with no database it shows
// the embedded snapshot and dispatches without writing anything back. The pages are served in-process
// to this window only (see internal/desktop). Flags: see options.
package main

import (
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"sync"

	"acme/hub/dispatch"
	"acme/hub/icon"
	"acme/hub/internal/app"
	"acme/hub/internal/run"
)

// hub is the window's state: the source, where the outbox goes, and the last pass.
type hub struct {
	mu       sync.Mutex
	dbPath   string
	forced   bool
	outDir   string
	last     *run.Summary
	lastErr  string
	settings func(path string) error
}

func (h *hub) source() run.Source {
	h.mu.Lock()
	defer h.mu.Unlock()
	return run.Choose(h.dbPath, h.forced)
}

type kindCount struct {
	Kind  string
	Count int
}

func (h *hub) page(extra map[string]any) map[string]any {
	src := h.source()
	detail := src.DB
	if src.Mock {
		detail = src.Why
	}
	data := map[string]any{"Name": app.Name, "Version": app.Version, "Source": src.Text(), "Detail": detail, "OnDB": !src.Mock}
	for k, v := range extra {
		data[k] = v
	}
	return data
}

func routes(h *hub) *http.ServeMux {
	mux := http.NewServeMux()
	serve := func(b []byte, kind string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", kind)
			w.Write(b)
		}
	}
	mux.HandleFunc("GET /favicon.ico", serve(icon.ICO, "image/x-icon"))
	mux.HandleFunc("GET /icon-32.png", serve(icon.PNG32, "image/png"))
	mux.HandleFunc("GET /icon-256.png", serve(icon.PNG256, "image/png"))

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		events, _, err := run.Load(h.source())
		var queue []kindCount
		total := 0
		for k, n := range run.Queue(events) {
			queue = append(queue, kindCount{k, n})
			total += n
		}
		sortCounts(queue)
		h.mu.Lock()
		last, lastErr := h.last, h.lastErr
		h.mu.Unlock()
		var counts []kindCount
		if last != nil {
			for _, k := range run.Keys(last.Counts) {
				counts = append(counts, kindCount{k, last.Counts[k]})
			}
		}
		data := h.page(map[string]any{"Queue": queue, "Total": total, "Events": len(events), "Last": last, "Counts": counts,
			"LastErr": lastErr})
		if err != nil {
			data["LoadErr"] = err.Error()
		}
		render(w, queuePage, data)
	})
	mux.HandleFunc("POST /dispatch", func(w http.ResponseWriter, r *http.Request) {
		sum, err := run.Dispatch(h.source(), h.outDir, r.FormValue("dry") != "")
		h.mu.Lock()
		h.last, h.lastErr = &sum, ""
		if err != nil {
			h.lastErr = err.Error()
		}
		h.mu.Unlock()
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /deliveries", func(w http.ResponseWriter, r *http.Request) {
		ds, err := run.ReadOutbox(h.outDir)
		more := 0
		if len(ds) > 300 {
			more, ds = len(ds)-300, ds[:300]
		}
		data := h.page(map[string]any{"Deliveries": ds, "More": more, "Outbox": filepath.Join(h.outDir, "deliveries.jsonl")})
		if err != nil {
			data["None"] = true
		}
		render(w, deliveriesPage, data)
	})
	about := func(w http.ResponseWriter, code int, extra map[string]any) {
		h.mu.Lock()
		path := h.dbPath
		h.mu.Unlock()
		extra["DB"], extra["Credit"], extra["Publisher"] = path, app.Credit, app.Publisher
		w.WriteHeader(code)
		render(w, aboutPage, h.page(extra))
	}
	mux.HandleFunc("GET /about", func(w http.ResponseWriter, r *http.Request) {
		about(w, http.StatusOK, map[string]any{"Saved": r.URL.Query().Get("saved") != ""})
	})
	mux.HandleFunc("POST /about", func(w http.ResponseWriter, r *http.Request) {
		path, err := filepath.Abs(r.FormValue("db"))
		if err == nil {
			if src := run.Choose(path, false); src.Mock {
				about(w, http.StatusBadRequest, map[string]any{"Error": path + ": " + src.Why})
				return
			}
			h.mu.Lock()
			h.dbPath, h.forced = path, false
			h.mu.Unlock()
			if h.settings != nil {
				err = h.settings(path)
			}
		}
		if err != nil {
			about(w, http.StatusBadRequest, map[string]any{"Error": err.Error()})
			return
		}
		http.Redirect(w, r, "/about?saved=1", http.StatusSeeOther)
	})
	return mux
}

func sortCounts(c []kindCount) {
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && (c[j].Count > c[j-1].Count || c[j].Count == c[j-1].Count && c[j].Kind < c[j-1].Kind); j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
}

func render(w http.ResponseWriter, t *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, data); err != nil {
		log.Print(err)
	}
}

var funcs = template.FuncMap{"short": func(d dispatch.Delivery) string { return d.At[5:] }}

const layout = `{{define "top"}}<!doctype html><html lang="en"><head><meta charset="utf-8"><title>ACME Notify Hub</title>
<link rel="icon" href="/favicon.ico"><style>
body{margin:0;font:15px/1.5 system-ui,sans-serif;color:#1d2330;background:#f6f7f9}
header,main,footer{max-width:960px;margin:0 auto;padding:14px 16px}
header{display:flex;gap:14px;align-items:center}header h1{font-size:20px;margin:0;flex:1;display:flex;gap:10px;align-items:center}
.px{image-rendering:pixelated;image-rendering:crisp-edges}
a{color:#2f5bd3;text-decoration:none}a:hover{text-decoration:underline}
.card{background:#fff;border:1px solid #dde1e7;border-radius:8px;padding:14px 16px;margin-bottom:12px}
.meta{color:#667085;font-size:13px}.badge{display:inline-block;padding:1px 8px;border-radius:10px;background:#e7ecf8;font-size:12px}
.badge.db{background:#12805c;color:#fff}.error{color:#b42318}
table{border-collapse:collapse;width:100%;font-size:13px}td,th{text-align:left;padding:4px 6px;border-bottom:1px solid #eceef2}
th{color:#667085;font-weight:600}.num{text-align:right}button{font:inherit;padding:6px 14px}
input.wide{width:100%;box-sizing:border-box;font:inherit;padding:4px 8px}
.send{color:#12805c}.batch,.digest{color:#b26b00}.defer{color:#b42318}
footer{color:#667085;font-size:12px}
@media (prefers-color-scheme:dark){body{color:#e6e8ec;background:#14171c}a{color:#8fb0ff}.card{background:#1d2128;border-color:#2e343e}td,th{border-color:#2e343e}.badge{background:#2e343e}}
</style></head><body><header><h1><img class="px" src="/icon-32.png" width="32" height="32" alt=""><a href="/">ACME Inc. Notify Hub</a></h1>
<a href="/">Queue</a><a href="/deliveries">Deliveries</a><a href="/about">About</a></header><main>
<p class="meta"><span class="badge{{if .OnDB}} db{{end}}">{{if .OnDB}}shared database{{else}}mock snapshot{{end}}</span> {{.Detail}}</p>{{end}}
{{define "bottom"}}</main><footer>{{.Name}} {{.Version}} · Fictional company; no message leaves this computer (the outbox file is the delivery channel).</footer></body></html>{{end}}`

var (
	queuePage = template.Must(template.Must(template.New("queue").Funcs(funcs).Parse(layout)).Parse(`{{template "top" .}}
{{if .LoadErr}}<p class="error">{{.LoadErr}}</p>{{end}}
<div class="card"><h2 style="margin-top:0">Queue: {{.Total}} pending of {{.Events}} events</h2>
<table>{{range .Queue}}<tr><td>{{.Kind}}</td><td class="num">{{.Count}}</td></tr>{{else}}<tr><td class="meta">Nothing pending.</td></tr>{{end}}</table>
<form method="post" action="/dispatch" style="margin-top:12px"><button type="submit">Dispatch</button>
<label class="meta"><input type="checkbox" name="dry" {{if not .OnDB}}checked disabled{{end}}> dry run (write no statuses)</label></form></div>
{{if .LastErr}}<p class="error">{{.LastErr}}</p>{{end}}
{{with .Last}}<div class="card"><h2 style="margin-top:0">Last pass</h2><p class="meta">{{.Pending}} events processed · {{len .Deliveries}} deliveries ·
{{if .DryRun}}nothing written back{{else}}{{.Written}} statuses written{{end}} · <a href="/deliveries">see them</a></p>
<table>{{range $.Counts}}<tr><td>{{.Kind}}</td><td class="num">{{.Count}}</td></tr>{{end}}</table></div>{{end}}
{{template "bottom" .}}`))
	deliveriesPage = template.Must(template.Must(template.New("deliveries").Funcs(funcs).Parse(layout)).Parse(`{{template "top" .}}
<div class="card">{{if .None}}<p class="meta">No pass yet: press Dispatch on the Queue page.</p>{{else}}
<p class="meta">From {{.Outbox}}{{if .More}} (first 300; {{.More}} more in the file){{end}}</p>
<table><tr><th>At</th><th>Action</th><th>Band</th><th>Retry</th><th>Class</th><th>To</th><th>Subject</th></tr>
{{range .Deliveries}}<tr><td>{{short .}}</td><td class="{{.Action}}">{{.Action}}</td><td>{{.Band}}</td><td>{{.RetryAfter}}</td><td>{{.Class}}</td><td>{{.Email}}</td><td>{{.Subject}}</td></tr>{{end}}
</table>{{end}}</div>
{{template "bottom" .}}`))
	aboutPage = template.Must(template.Must(template.New("about").Funcs(funcs).Parse(layout)).Parse(`{{template "top" .}}
<div class="card" style="text-align:center"><img class="px" src="/icon-256.png" width="128" height="128" alt="">
<h2>{{.Name}} {{.Version}}</h2><p class="meta">{{.Publisher}}</p><p class="meta">{{.Credit}}<br>Code: AGPL-3.0. All data is fictional.</p></div>
<div class="card"><h2 style="margin-top:0">Shared database</h2><p class="meta">Pending events and customers are read from it, and each processed event's status is written back.</p>
<form method="post" action="/about"><input class="wide" name="db" value="{{.DB}}" spellcheck="false">
<p><button type="submit">Use this database</button>{{if .Saved}} <span class="meta">Saved.</span>{{end}}</p></form>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}</div>
{{template "bottom" .}}`))
)

// ==== stamp v1 | author=kjFriday | hmac-sha256=615e2209a272ec2cc9e67ce83123e805baebee870b29a4b1fee1ed3bbe350519 ====
