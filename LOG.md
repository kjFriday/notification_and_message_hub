# notification_and_message_hub — Log

ACME Inc. is a fictional company. All data in this ecosystem is mock data.

## 2026-10-17

- Chose Go for the hub. The throttle reference was already written in Go, and the hub needs many concurrent channel workers and long-lived connections. Standard library only.
- `throttle`: per-key load-score throttle. Scores decay lazily, four bands decide which priorities send (marketing is batched, then digested, then deferred; critical always sends), `Allow`/`Record` wrap each dispatch, and a closed-form forecast gives `Retry-After`. Tested with a fake clock; the forecast is checked against the excerpt's step-by-step loop.
- `textsafe`: Go port of the CRUD hub's input sanitizer, same rules and 240-character cap, with its tests ported.
