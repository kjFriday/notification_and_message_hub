<img src="icon/app-256.png" width="64" height="64" alt="" style="image-rendering: pixelated">

# ACME Notify Hub (notification hub)

Turns pending ACME Inc. events into messages and throttles them per recipient, in its own desktop
window. ACME Inc. and everyone in it is fictional; no message leaves the machine (the outbox file is the
delivery channel).

The window shows the pending-event queue, a **Dispatch** button (with a dry run), and the last pass's
deliveries with their throttle band and retry time. Events and customers come from the shared ACME
database, opened **by path**: `-db PATH`, the `ACME_DB` environment variable, the path saved on the
**About** page, or `%LOCALAPPDATA%\ACME Inc\acme.db`. Each processed event's status is written back (only
rows still `pending` change), so a second pass finds nothing to do. With no database, or `-mock`, it
runs on the embedded snapshot and writes nothing back.

## Build and run

```bash
go build -trimpath -ldflags "-H windowsgui" -o acme-notify.exe ./cmd/notify
```

```bash
acme-notify.exe
```

`-trimpath` keeps the build machine's paths out of the exe. The window is Microsoft Edge WebView2 over pages the exe serves on `127.0.0.1` to that window only (a
per-run token cookie; other requests are refused). `acme-notify.exe -serve` serves them without a window
on `http://127.0.0.1:8730/`. The console dispatcher is still there:

```bash
go run ./cmd/dispatch -db PATH
```

Deliveries go to `deliveries.jsonl` in the outbox folder (the window's is
`%LOCALAPPDATA%\ACME Inc\ACME.Notify\outbox`), one JSON object per message.

## Install

```bash
acme-notify.exe -install
```

Installs for the current user: copies the exe to `%LOCALAPPDATA%\Programs\ACME Inc\ACME Notify Hub\`,
adds **ACME Inc → ACME Notify Hub** to the Start menu (`-desktop-shortcut` for the desktop too) and lists
it in **Apps & features**. `-uninstall` (or Apps & features) removes it; the database is never deleted
(`-purge` also removes the program's own settings, outbox and WebView2 profile). `-root DIR` sandboxes
it all in DIR. Other flags: `-version`, `-quiet`, `-smoke FILE`.

`wizard-maker.exe build setup.json` (ACME Wizard Maker) makes the setup, `dist\ACME Notify Hub-0.2.0-setup.exe`.

## Routing

| Event kind | Recipient | Class (throttle priority) |
|---|---|---|
| `order.created` | the ordering customer | transactional |
| `ticket.created` | the customer on the ticket | support (high) |
| `calendar.window`, `calendar.day` | every customer the events mention | marketing, text from `templates/seasonal.tsv` |
| anything else | nobody; the event is marked `skipped` | - |

Events are processed in time order and the throttle's clock follows the event times, so a burst to one
recipient is held back (`batch`, `digest`, `defer`, with `retry_after`) exactly as it would be live. An
event whose messages were all sent becomes `sent`; one with a held message stays `pending`.

| Package | What |
|---|---|
| `dispatch` | Templates, routing (`Plan`) and the throttled run (`Run`); no I/O |
| `internal/run` | One pass: load, dispatch, write the outbox and the statuses (used by both commands) |
| `internal/store` | The shared database: events, customers, status write-back |
| `internal/app`, `internal/install`, `internal/desktop` | Version and DB path, self-installer, WebView2 window (copied from the CMS) |
| `throttle` | Per-key load-score throttle (see `reference/throttle`) |
| `textsafe` | The CRUD hub's input sanitizer, ported |
| `cmd/notify` | The desktop window (`acme-notify.exe`) |
| `cmd/dispatch` | The same pass from the console |

## Tests

```bash
go vet ./...
```

```bash
go test ./...
```

## Dependencies

- `github.com/jchv/go-webview2`: the native window, driven from pure Go (no cgo, so no C compiler).
- `modernc.org/sqlite` (v1.46.1, the last release for Go 1.24): the shared SQLite file, also without cgo.

## Icon and license

Icon: Silk "bell" by Mark James, CC BY 2.5, enlarged by nearest-neighbour (`icon/README.md`).
Code: AGPL-3.0, as the rest of the portfolio.
