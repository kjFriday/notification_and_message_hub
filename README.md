# Notification hub

Turns pending ACME Inc. events into messages and throttles them per recipient. Go standard library only.
ACME Inc. and everyone in it is fictional; no message leaves the machine (the outbox file is the
delivery channel).

- **Standalone:** reads `mock/events.json` and `mock/customers.json`.
- **Connected:** `-hub http://127.0.0.1:8700` (or `ACME_HUB`) reads the CRUD hub's `events` and
  `customers` tables and writes each processed event's status back with `PATCH /api/events/{id}`
  (`-dry-run` skips that). Run it twice: the second run finds nothing pending.

## Run

```bash
go run ./cmd/dispatch
```

```bash
go run ./cmd/dispatch -hub http://127.0.0.1:8700
```

Deliveries go to `outbox/deliveries.jsonl`, one JSON object per message; a summary is printed.

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
| `throttle` | Per-key load-score throttle (see `reference/throttle`) |
| `textsafe` | The CRUD hub's input sanitizer, ported |
| `cmd/dispatch` | Reads the snapshot or the hub, writes the outbox and statuses |

## Tests

```bash
go vet ./...
```

```bash
go test ./...
```

## License

AGPL-3.0, as the rest of the portfolio.
