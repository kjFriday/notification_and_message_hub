// ==== ACME Inc. portfolio ===============================================
// Project: notification_and_message_hub | File: cmd/dispatch/main.go
// About: Command dispatch sends the pending ACME Inc. notifications from the console (the window...
// Copyright (c) 2026 kjFriday | SPDX-License-Identifier: AGPL-3.0-only
// You may change this file under the AGPL-3.0. The stamp at the end marks
// the version kjFriday wrote. Verify with the portfolio's stamp.py --check.
// Reused code, if any, is mapped in PROVENANCE.md.
// ========================================================================

// Command dispatch sends the pending ACME Inc. notifications from the console (the window is cmd/notify).
//
// It reads the shared ACME database by path (-db, ACME_DB, the Notify hub's saved setting, or
// %LOCALAPPDATA%\ACME Inc\acme.db) and writes each processed event's status back; with no database,
// or with -mock, it runs on the embedded snapshot and writes nothing back. Either way every delivery
// goes to <out>/deliveries.jsonl and a summary is printed. No message leaves the machine: the outbox
// file is the delivery channel.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"acme/hub/internal/app"
	"acme/hub/internal/run"
)

func main() {
	db := flag.String("db", "", "shared database (default: ACME_DB, the saved setting, or the shared default)")
	mock := flag.Bool("mock", false, "use the embedded snapshot, not the database")
	out := flag.String("out", "outbox", "folder for deliveries.jsonl")
	dry := flag.Bool("dry-run", false, "do not write statuses back")
	show := flag.Int("show", 8, "deliveries to print")
	flag.Parse()
	src := run.Choose(app.ResolveDB(*db, os.Getenv), *mock)
	sum, err := run.Dispatch(src, *out, *dry)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dispatch:", err)
		os.Exit(1)
	}
	fmt.Printf("source: %s\n", sum.Source)
	fmt.Printf("events: %d read, %d pending\n", sum.Events, sum.Pending)
	for _, k := range run.Keys(sum.Counts) {
		fmt.Printf("  %-22s %d\n", k, sum.Counts[k])
	}
	for i, d := range sum.Deliveries {
		if i >= *show {
			fmt.Printf("  ... %d more in %s\n", len(sum.Deliveries)-*show, filepath.Join(*out, "deliveries.jsonl"))
			break
		}
		fmt.Printf("  %s %-6s %-34s %s\n", d.At, d.Action, d.Email, d.Subject)
	}
	if !sum.DryRun {
		fmt.Printf("database: %d event statuses written\n", sum.Written)
	}
}

// ==== stamp v1 | author=kjFriday | hmac-sha256=eba6f05ed93a8648b28d31a0a8835b17ffd6f4a6da36a3d844df034896e2f7e3 ====
