package dispatch

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"acme/hub/throttle"
)

const tsv = "# comment\nseason\tstarts\tends\ttemplate\nAUTUMN\t10-01\t11-02\t@ lights a lantern.\nAUTUMN\t10-01\t11-02\t@ walks home.\nWINTER\t12-20\t01-05\tHello @.\n"

func templates(t *testing.T) []Template {
	t.Helper()
	ts, err := LoadTemplates(strings.NewReader(tsv))
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestLoadTemplatesChecksShape(t *testing.T) {
	if got := len(templates(t)); got != 3 {
		t.Fatalf("templates = %d, want 3", got)
	}
	if _, err := LoadTemplates(strings.NewReader("h\th\th\th\nA\t01-01\t01-02\tno marker\n")); err == nil {
		t.Fatal("a template without @ was accepted")
	}
}

func TestPickIsStableAndWrapsTheYear(t *testing.T) {
	ts := templates(t)
	day := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	a, ok := Pick(ts, day, "42")
	b, _ := Pick(ts, day, "42")
	if !ok || a != b || a.Season != "AUTUMN" {
		t.Fatalf("pick = %+v, %v", a, ok)
	}
	if w, ok := Pick(ts, time.Date(2027, 1, 3, 0, 0, 0, 0, time.UTC), "1"); !ok || w.Season != "WINTER" {
		t.Fatalf("new-year window: %+v %v", w, ok)
	}
	if _, ok := Pick(ts, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), "1"); ok {
		t.Fatal("June matched a window")
	}
}

func dir() Directory {
	cs := []Person{{ID: 1, Name: "Maria Garcia", Email: "maria.garcia@mail.example"}, {ID: 2, Name: "Kenji Sato", Email: "kenji.sato@mail.example"}}
	ev := []Event{{Payload: `{"customer_id": 2}`}, {Payload: `{"customer_id": 1}`}, {Payload: `{"customer_id": 9}`}}
	return NewDirectory(cs, ev)
}

func TestPlanRoutesEachKind(t *testing.T) {
	d, ts := dir(), templates(t)
	if len(d.Audience) != 2 || d.Audience[0].ID != 1 {
		t.Fatalf("audience = %+v", d.Audience)
	}
	order := Plan(Event{ID: 5, Kind: "order.created", Payload: `{"order_id": 77, "customer_id": 1, "note": "Lorem\u0007 ipsum."}`}, d, ts)
	if len(order) != 1 || order[0].Subject != "Order #77 confirmed" || !strings.HasPrefix(order[0].Body, "Hi Maria,") || strings.ContainsRune(order[0].Body, 7) {
		t.Fatalf("order message = %+v", order)
	}
	ticket := Plan(Event{Kind: "ticket.created", Payload: `{"ticket_id": 3, "customer_id": 2}`}, d, ts)
	if len(ticket) != 1 || ticket[0].Priority != throttle.High || ticket[0].Email != "kenji.sato@mail.example" {
		t.Fatalf("ticket message = %+v", ticket)
	}
	sale := Plan(Event{Kind: "calendar.window", Payload: `{"name": "Autumn Sale", "start": "2026-10-01", "end": "2026-11-02"}`}, d, ts)
	if len(sale) != 2 || sale[0].Priority != throttle.Marketing || strings.Contains(sale[0].Body, "@") {
		t.Fatalf("broadcast = %+v", sale)
	}
	for _, kind := range []string{"calendar.instant", "venue.show", "unknown"} {
		if m := Plan(Event{Kind: kind, Payload: `{}`}, d, ts); m != nil {
			t.Fatalf("%s planned %d messages", kind, len(m))
		}
	}
	if m := Plan(Event{Kind: "order.created", Payload: `{"customer_id": 9}`}, d, ts); m != nil {
		t.Fatal("unknown customer got a message")
	}
}

func TestRunThrottlesABurstAndReportsStatus(t *testing.T) {
	d, ts := dir(), templates(t)
	var events []Event
	for i := 0; i < 12; i++ { // twelve orders for one customer inside a minute
		p := `{"order_id": ` + string(rune('0'+i%10)) + `, "customer_id": 1}`
		events = append(events, Event{ID: int64(100 + i), Kind: "order.created", Payload: p, Status: "pending", CreatedAt: "2026-09-27T10:00"})
	}
	events = append(events,
		Event{ID: 1, Kind: "calendar.instant", Payload: `{}`, Status: "pending", CreatedAt: "2026-09-22T00:00"},
		Event{ID: 2, Kind: "order.created", Payload: `{"order_id": 1, "customer_id": 2}`, Status: "sent", CreatedAt: "2026-09-27T10:00"},
	)
	res, err := Run(events, d, ts, throttle.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Status) != 13 || res.Status[1] != "skipped" {
		t.Fatalf("status = %v", res.Status)
	}
	if _, done := res.Status[2]; done {
		t.Fatal("an already-sent event was dispatched again")
	}
	sent, held := 0, 0
	for _, dl := range res.Deliveries {
		if dl.Action == "send" {
			sent++
		} else {
			held++
			if dl.RetryAfter == "" {
				t.Fatal("held delivery without retry_after")
			}
		}
	}
	if sent == 0 || held == 0 || res.Status[100] != "sent" || res.Status[111] != "pending" {
		t.Fatalf("sent %d held %d first %q last %q", sent, held, res.Status[100], res.Status[111])
	}
}

func TestMockSnapshotDispatches(t *testing.T) {
	read := func(name string, v any) {
		b, err := os.ReadFile("../mock/" + name)
		if err != nil {
			t.Skip("no mock snapshot:", err)
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatal(err)
		}
	}
	var events []Event
	var customers []Person
	read("events.json", &events)
	read("customers.json", &customers)
	f, err := os.Open("../templates/seasonal.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ts, err := LoadTemplates(f)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(events, NewDirectory(customers, events), ts, throttle.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	classes := map[string]bool{}
	for _, dl := range res.Deliveries {
		classes[dl.Class] = true
		if dl.Email == "" || strings.Contains(dl.Body, "@") {
			t.Fatalf("bad delivery %+v", dl)
		}
	}
	for _, c := range []string{"transactional", "support", "marketing"} {
		if !classes[c] {
			t.Fatalf("no %s deliveries from the snapshot", c)
		}
	}
}
