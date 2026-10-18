// ==== ACME Inc. portfolio ===============================================
// Project: notification_and_message_hub | File: dispatch/dispatch.go
// About: Package dispatch turns pending hub events into messages: who gets one, what it says, and...
// Copyright (c) 2026 kjFriday | SPDX-License-Identifier: AGPL-3.0-only
// You may change this file under the AGPL-3.0. The stamp at the end marks
// the version kjFriday wrote. Verify with the portfolio's stamp.py --check.
// Reused code, if any, is mapped in PROVENANCE.md.
// ========================================================================

// Package dispatch turns pending hub events into messages: who gets one, what it says, and whether
// the throttle lets it go now. It does no I/O; cmd/dispatch reads the events and writes the results.
package dispatch

import (
	"bufio"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"strings"
	"time"

	"acme/hub/textsafe"
	"acme/hub/throttle"
)

// Stamp is the hub's timestamp layout (yyyy-mm-ddThh:mm).
const Stamp = "2006-01-02T15:04"

// Event is one row of the hub's events table.
type Event struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Payload   string `json:"payload"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// Person is a customer or employee row.
type Person struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Template is one seasonal line; @ marks the recipient's first name.
type Template struct {
	Season     string
	Start, End string // mm-dd, both inside the window
	Text       string
}

// LoadTemplates reads templates/seasonal.tsv: # comments, a header row, then season, starts, ends, template.
func LoadTemplates(r io.Reader) ([]Template, error) {
	var out []Template
	sc := bufio.NewScanner(r)
	header := true
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if header {
			header = false
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 4 || strings.Count(f[3], "@") != 1 {
			return nil, fmt.Errorf("templates line %d: want 4 fields and one @", n)
		}
		out = append(out, Template{Season: f[0], Start: f[1], End: f[2], Text: f[3]})
	}
	return out, sc.Err()
}

// inWindow reports whether mm-dd day falls in [start, end], allowing windows that wrap the new year.
func inWindow(day, start, end string) bool {
	if start <= end {
		return start <= day && day <= end
	}
	return day >= start || day <= end
}

// Pick returns the template for date whose window contains it, chosen by key so a recipient always
// gets the same line; ok is false when no window matches.
func Pick(ts []Template, date time.Time, key string) (Template, bool) {
	day := date.Format("01-02")
	var hits []Template
	for _, t := range ts {
		if inWindow(day, t.Start, t.End) {
			hits = append(hits, t)
		}
	}
	if len(hits) == 0 {
		return Template{}, false
	}
	h := fnv.New32a()
	h.Write([]byte(key))
	return hits[int(h.Sum32()%uint32(len(hits)))], true
}

// Message is one notification for one recipient.
type Message struct {
	EventID  int64             `json:"event_id"`
	Kind     string            `json:"kind"`
	To       string            `json:"to"`
	Email    string            `json:"email"`
	Priority throttle.Priority `json:"-"`
	Class    string            `json:"class"`
	Subject  string            `json:"subject"`
	Body     string            `json:"body"`
}

func firstName(name string) string {
	if f := strings.Fields(name); len(f) > 0 {
		return f[0]
	}
	return "there"
}

// Directory resolves people by id.
type Directory struct {
	Customers map[int64]Person
	Audience  []Person // who receives a broadcast (seasonal) message
}

// NewDirectory indexes customers; the broadcast audience is the customers the events mention.
func NewDirectory(customers []Person, events []Event) Directory {
	d := Directory{Customers: map[int64]Person{}}
	for _, c := range customers {
		d.Customers[c.ID] = c
	}
	seen := map[int64]bool{}
	for _, e := range events {
		if id := payloadInt(e.Payload, "customer_id"); id > 0 && !seen[id] {
			if c, ok := d.Customers[id]; ok {
				seen[id] = true
				d.Audience = append(d.Audience, c)
			}
		}
	}
	sort.Slice(d.Audience, func(i, j int) bool { return d.Audience[i].ID < d.Audience[j].ID })
	return d
}

func payload(raw string) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &m)
	return m
}

func payloadInt(raw, key string) int64 {
	if v, ok := payload(raw)[key].(float64); ok {
		return int64(v)
	}
	return 0
}

// Plan lists the messages an event should produce. Kinds with nobody to tell (solstices, venue
// shows) give none.
func Plan(e Event, d Directory, ts []Template) []Message {
	p := payload(e.Payload)
	one := func(who Person, prio throttle.Priority, class, subject, body string) Message {
		return Message{EventID: e.ID, Kind: e.Kind, To: who.Name, Email: who.Email, Priority: prio, Class: class,
			Subject: textsafe.Clean(subject), Body: textsafe.CleanN(body, 480)}
	}
	switch e.Kind {
	case "order.created":
		c, ok := d.Customers[payloadInt(e.Payload, "customer_id")]
		if !ok {
			return nil
		}
		oid := payloadInt(e.Payload, "order_id")
		note, _ := p["note"].(string)
		return []Message{one(c, throttle.Transactional, "transactional", fmt.Sprintf("Order #%d confirmed", oid),
			fmt.Sprintf("Hi %s, your ACME Inc. order #%d is confirmed. %s", firstName(c.Name), oid, note))}
	case "ticket.created":
		c, ok := d.Customers[payloadInt(e.Payload, "customer_id")]
		if !ok {
			return nil
		}
		tid := payloadInt(e.Payload, "ticket_id")
		return []Message{one(c, throttle.High, "support", fmt.Sprintf("We have your ticket #%d", tid),
			fmt.Sprintf("Hi %s, ticket #%d is with our support team. We will reply on the ticket.", firstName(c.Name), tid))}
	case "calendar.window", "calendar.day":
		name, _ := p["name"].(string)
		date, _ := p["start"].(string)
		if date == "" {
			date, _ = p["date"].(string)
		}
		when, err := time.Parse("2006-01-02", date)
		if err != nil {
			return nil
		}
		var out []Message
		for _, c := range d.Audience {
			t, ok := Pick(ts, when, fmt.Sprintf("%d", c.ID))
			if !ok {
				continue
			}
			out = append(out, one(c, throttle.Marketing, "marketing", name, strings.Replace(t.Text, "@", firstName(c.Name), 1)))
		}
		return out
	}
	return nil
}

// Delivery is what happened to one message.
type Delivery struct {
	Message
	At         string `json:"at"`
	Action     string `json:"action"` // send, batch, digest, defer
	Band       string `json:"band"`
	RetryAfter string `json:"retry_after,omitempty"`
}

// Result is the outcome per event: "sent" when every message went out, "skipped" when there was no
// one to tell, "pending" when the throttle held something back.
type Result struct {
	Deliveries []Delivery
	Status     map[int64]string
}

var actionNames = map[throttle.Action]string{throttle.Send: "send", throttle.Batch: "batch", throttle.Digest: "digest", throttle.Defer: "defer"}

// Run plans and throttles every pending event in time order. The throttle's clock follows the event
// times, so a burst of events for one customer is spread out exactly as it would be live.
func Run(events []Event, d Directory, ts []Template, cfg throttle.Config) (Result, error) {
	var now time.Time
	th, err := throttle.New(cfg, func() time.Time { return now })
	if err != nil {
		return Result{}, err
	}
	pending := make([]Event, 0, len(events))
	for _, e := range events {
		if e.Status == "pending" {
			pending = append(pending, e)
		}
	}
	sort.SliceStable(pending, func(i, j int) bool {
		if pending[i].CreatedAt != pending[j].CreatedAt {
			return pending[i].CreatedAt < pending[j].CreatedAt
		}
		return pending[i].ID < pending[j].ID
	})
	res := Result{Status: map[int64]string{}}
	for _, e := range pending {
		if t, err := time.Parse(Stamp, e.CreatedAt); err == nil {
			now = t
		}
		msgs := Plan(e, d, ts)
		if len(msgs) == 0 {
			res.Status[e.ID] = "skipped"
			continue
		}
		status := "sent"
		for _, m := range msgs {
			key := m.Email
			dec := th.Allow(key, m.Priority)
			dl := Delivery{Message: m, At: now.Format(Stamp), Action: actionNames[dec.Action], Band: dec.Band}
			if dec.Action == throttle.Send {
				th.Record(key, throttle.Sent)
			} else {
				status = "pending"
				dl.RetryAfter = dec.RetryAfter.String()
			}
			res.Deliveries = append(res.Deliveries, dl)
		}
		res.Status[e.ID] = status
	}
	return res, nil
}

// ==== stamp v1 | author=kjFriday | hmac-sha256=9dc8598cd5ea5088a9189535b035dad50200d81d60e363cc19d03b94ca53e7d8 ====
