// Package run is one dispatcher pass, shared by the window (cmd/notify) and the console (cmd/dispatch):
// read events and customers from the shared database (or the embedded snapshot), route and throttle
// them, write the outbox, and store each processed event's status.
package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"acme/hub/dispatch"
	"acme/hub/internal/store"
	"acme/hub/mock"
	"acme/hub/templates"
	"acme/hub/throttle"
)

// Source is where the rows come from.
type Source struct {
	DB   string // the shared database; "" means the embedded snapshot
	Why  string // when on the snapshot: why the database was not used
	Mock bool
}

// Choose uses the database at path when it opens and has the needed tables, else the snapshot.
func Choose(path string, forceMock bool) Source {
	if forceMock {
		return Source{Mock: true, Why: "-mock"}
	}
	conn, err := store.Open(path)
	if err == nil {
		err = store.CheckSchema(conn)
		conn.Close()
	}
	if err != nil {
		return Source{Mock: true, Why: err.Error()}
	}
	return Source{DB: path}
}

// Text describes the source for people.
func (s Source) Text() string {
	if s.Mock {
		return "mock snapshot (" + s.Why + ")"
	}
	return "shared database " + s.DB
}

// Load reads events and customers.
func Load(s Source) ([]dispatch.Event, []dispatch.Person, error) {
	var events []dispatch.Event
	var customers []dispatch.Person
	if s.Mock {
		for name, into := range map[string]any{"events.json": &events, "customers.json": &customers} {
			b, err := mock.FS.ReadFile(name)
			if err != nil {
				return nil, nil, err
			}
			if err := json.Unmarshal(b, into); err != nil {
				return nil, nil, err
			}
		}
		return events, customers, nil
	}
	conn, err := store.Open(s.DB)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	if events, err = store.Events(conn); err != nil {
		return nil, nil, err
	}
	customers, err = store.Customers(conn)
	return events, customers, err
}

// Queue counts pending events by kind.
func Queue(events []dispatch.Event) map[string]int {
	out := map[string]int{}
	for _, e := range events {
		if e.Status == "pending" {
			out[e.Kind]++
		}
	}
	return out
}

// Summary is what one pass did.
type Summary struct {
	Source     string              `json:"source"`
	Events     int                 `json:"events"`
	Pending    int                 `json:"pending"`
	Counts     map[string]int      `json:"counts"` // "<class> <action>" -> deliveries
	Deliveries []dispatch.Delivery `json:"deliveries"`
	Written    int                 `json:"written"` // event statuses stored in the database
	Outbox     string              `json:"outbox"`
	DryRun     bool                `json:"dry_run"`
}

// Dispatch runs one pass. With dry, or on the snapshot, no status is written back.
func Dispatch(s Source, outDir string, dry bool) (Summary, error) {
	ts, err := dispatch.LoadTemplates(bytes.NewReader(templates.Seasonal))
	if err != nil {
		return Summary{}, err
	}
	events, customers, err := Load(s)
	if err != nil {
		return Summary{}, err
	}
	res, err := dispatch.Run(events, dispatch.NewDirectory(customers, events), ts, throttle.DefaultConfig())
	if err != nil {
		return Summary{}, err
	}
	sum := Summary{Source: s.Text(), Events: len(events), Pending: len(res.Status), Counts: map[string]int{},
		Deliveries: res.Deliveries, Outbox: filepath.Join(outDir, "deliveries.jsonl"), DryRun: dry || s.Mock}
	for _, d := range res.Deliveries {
		sum.Counts[d.Class+" "+d.Action]++
	}
	if err := WriteOutbox(outDir, res.Deliveries); err != nil {
		return sum, err
	}
	if !sum.DryRun {
		conn, err := store.Open(s.DB)
		if err != nil {
			return sum, err
		}
		defer conn.Close()
		if sum.Written, err = store.WriteStatuses(conn, res.Status); err != nil {
			return sum, fmt.Errorf("writing statuses: %w", err)
		}
	}
	return sum, nil
}

// WriteOutbox writes one JSON delivery per line to dir/deliveries.jsonl (the delivery channel).
func WriteOutbox(dir string, ds []dispatch.Delivery) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	for _, d := range ds {
		if err := enc.Encode(d); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, "deliveries.jsonl"), b.Bytes(), 0o644)
}

// ReadOutbox reads the last pass's deliveries back.
func ReadOutbox(dir string) ([]dispatch.Delivery, error) {
	b, err := os.ReadFile(filepath.Join(dir, "deliveries.jsonl"))
	if err != nil {
		return nil, err
	}
	var out []dispatch.Delivery
	dec := json.NewDecoder(bytes.NewReader(b))
	for dec.More() {
		var d dispatch.Delivery
		if err := dec.Decode(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// Keys returns m's keys, sorted.
func Keys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
