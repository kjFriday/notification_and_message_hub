// Command dispatch sends the pending ACME Inc. notifications.
//
// Standalone (default) it reads mock/events.json and mock/customers.json. With -hub it reads the CRUD
// hub's events and customers tables and writes each event's new status back with PATCH. Either way it
// writes every delivery to <out>/deliveries.jsonl and prints a summary. No message leaves the machine:
// the outbox file is the delivery channel.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"acme/hub/dispatch"
	"acme/hub/throttle"
)

const page = 500 // the hub's largest ?limit

func main() {
	hub := flag.String("hub", os.Getenv("ACME_HUB"), "CRUD hub base URL, e.g. http://127.0.0.1:8700 (empty: use -mock)")
	mock := flag.String("mock", "mock", "folder holding events.json and customers.json")
	tpl := flag.String("templates", filepath.Join("templates", "seasonal.tsv"), "seasonal templates")
	out := flag.String("out", "outbox", "folder for deliveries.jsonl")
	dry := flag.Bool("dry-run", false, "with -hub: do not write statuses back")
	show := flag.Int("show", 8, "deliveries to print")
	flag.Parse()
	if err := run(strings.TrimRight(*hub, "/"), *mock, *tpl, *out, *dry, *show); err != nil {
		fmt.Fprintln(os.Stderr, "dispatch:", err)
		os.Exit(1)
	}
}

func run(hub, mock, tplPath, out string, dry bool, show int) error {
	f, err := os.Open(tplPath)
	if err != nil {
		return err
	}
	templates, err := dispatch.LoadTemplates(f)
	f.Close()
	if err != nil {
		return err
	}

	var events []dispatch.Event
	var customers []dispatch.Person
	source := "mock snapshot " + mock
	if hub == "" {
		if err := readJSON(filepath.Join(mock, "events.json"), &events); err != nil {
			return err
		}
		if err := readJSON(filepath.Join(mock, "customers.json"), &customers); err != nil {
			return err
		}
	} else {
		source = "live hub " + hub
		if err := fetchAll(hub, "events", &events); err != nil {
			return fmt.Errorf("hub unreachable (%w); run without -hub to use the mock snapshot", err)
		}
		if err := fetchAll(hub, "customers", &customers); err != nil {
			return err
		}
	}

	dir := dispatch.NewDirectory(customers, events)
	res, err := dispatch.Run(events, dir, templates, throttle.DefaultConfig())
	if err != nil {
		return err
	}
	if err := writeOutbox(out, res.Deliveries); err != nil {
		return err
	}

	fmt.Printf("source: %s\n", source)
	fmt.Printf("events: %d read, %d pending\n", len(events), len(res.Status))
	counts := map[string]int{}
	for _, d := range res.Deliveries {
		counts[d.Class+" "+d.Action]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %-22s %d\n", k, counts[k])
	}
	for i, d := range res.Deliveries {
		if i >= show {
			fmt.Printf("  ... %d more in %s\n", len(res.Deliveries)-show, filepath.Join(out, "deliveries.jsonl"))
			break
		}
		fmt.Printf("  %s %-6s %-34s %s\n", d.At, d.Action, d.Email, d.Subject)
	}

	if hub != "" && !dry {
		n, err := writeStatuses(hub, res.Status)
		fmt.Printf("hub: %d event statuses written with PATCH /api/events/{id}\n", n)
		return err
	}
	return nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

var client = &http.Client{Timeout: 10 * time.Second}

// fetchAll reads every row of a hub table, paging with ?limit&offset.
func fetchAll[T any](hub, table string, into *[]T) error {
	for {
		resp, err := client.Get(fmt.Sprintf("%s/api/%s?limit=%d&offset=%d", hub, table, page, len(*into)))
		if err != nil {
			return err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET /api/%s: %s", table, resp.Status)
		}
		var rows []T
		if err := json.Unmarshal(body, &rows); err != nil {
			return err
		}
		*into = append(*into, rows...)
		if len(rows) < page {
			return nil
		}
	}
}

// writeStatuses PATCHes each event whose status changed; events still held stay "pending".
func writeStatuses(hub string, status map[int64]string) (int, error) {
	ids := make([]int64, 0, len(status))
	for id, s := range status {
		if s != "pending" {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for n, id := range ids {
		body, _ := json.Marshal(map[string]string{"status": status[id]})
		req, err := http.NewRequest(http.MethodPatch, fmt.Sprintf("%s/api/events/%d", hub, id), bytes.NewReader(body))
		if err != nil {
			return n, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return n, err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return n, fmt.Errorf("PATCH /api/events/%d: %s", id, resp.Status)
		}
	}
	return len(ids), nil
}

func writeOutbox(dir string, ds []dispatch.Delivery) error {
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
