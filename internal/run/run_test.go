package run

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"acme/hub/dispatch"
	"acme/hub/internal/store"
	"acme/hub/mock"
)

// mockDB builds acme.db from the hub's schema (mock/schema.sql) filled with the embedded snapshot.
func mockDB(t *testing.T) string {
	t.Helper()
	schema, err := os.ReadFile("../../mock/schema.sql")
	if err != nil {
		t.Skip("no mock/schema.sql:", err)
	}
	path := filepath.Join(t.TempDir(), "acme.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	var events []dispatch.Event
	var customers []dispatch.Person
	b, _ := mock.FS.ReadFile("events.json")
	json.Unmarshal(b, &events)
	b, _ = mock.FS.ReadFile("customers.json")
	json.Unmarshal(b, &customers)
	tx, _ := conn.Begin()
	for _, c := range customers {
		tx.Exec("INSERT INTO customers (id, name, email) VALUES (?, ?, ?)", c.ID, c.Name, c.Email)
	}
	for _, e := range events {
		tx.Exec("INSERT INTO events (id, kind, payload, status, created_at) VALUES (?, ?, ?, ?, ?)", e.ID, e.Kind, e.Payload, e.Status, e.CreatedAt)
	}
	tx.Commit()
	return path
}

func TestDatabaseMatchesSnapshot(t *testing.T) {
	db := mockDB(t)
	src := Choose(db, false)
	if src.Mock {
		t.Fatalf("fell back to the snapshot: %s", src.Why)
	}
	fromDB, _, err := Load(src)
	if err != nil {
		t.Fatal(err)
	}
	fromMock, _, _ := Load(Source{Mock: true})
	if len(fromDB) != len(fromMock) || len(Queue(fromDB)) == 0 {
		t.Fatalf("db %d events, mock %d", len(fromDB), len(fromMock))
	}
	for k, n := range Queue(fromMock) {
		if Queue(fromDB)[k] != n {
			t.Fatalf("pending %s: db %d mock %d", k, Queue(fromDB)[k], n)
		}
	}
}

func TestSecondPassFindsNothingPending(t *testing.T) {
	db := mockDB(t)
	out := t.TempDir()
	first, err := Dispatch(Choose(db, false), out, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Written == 0 || len(first.Deliveries) == 0 {
		t.Fatalf("first pass: %+v", first.Counts)
	}
	back, err := ReadOutbox(out)
	if err != nil || len(back) != len(first.Deliveries) {
		t.Fatalf("outbox %d vs %d (%v)", len(back), len(first.Deliveries), err)
	}
	second, err := Dispatch(Choose(db, false), out, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Written != 0 {
		t.Fatalf("second pass wrote %d statuses", second.Written)
	}
	if second.Pending >= first.Pending {
		t.Fatalf("pending %d then %d", first.Pending, second.Pending)
	}
}

func TestDryRunAndSnapshotWriteNothing(t *testing.T) {
	db := mockDB(t)
	sum, err := Dispatch(Choose(db, false), t.TempDir(), true)
	if err != nil || sum.Written != 0 || !sum.DryRun {
		t.Fatalf("dry run: %+v %v", sum.Written, err)
	}
	if again, _ := Dispatch(Choose(db, false), t.TempDir(), true); again.Pending != sum.Pending {
		t.Fatal("a dry run changed the queue")
	}
	mockRun, err := Dispatch(Source{Mock: true}, t.TempDir(), false)
	if err != nil || !mockRun.DryRun || mockRun.Written != 0 {
		t.Fatalf("snapshot run: %+v %v", mockRun, err)
	}
}

func TestOnlyPendingRowsChange(t *testing.T) {
	db := mockDB(t)
	conn, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var sentID int64
	conn.QueryRow("SELECT id FROM events WHERE status = 'sent' LIMIT 1").Scan(&sentID)
	n, err := store.WriteStatuses(conn, map[int64]string{sentID: "skipped"})
	if err != nil || n != 0 {
		t.Fatalf("changed a sent event: %d %v", n, err)
	}
}

func TestMissingDatabaseFallsBack(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.db")
	if src := Choose(missing, false); !src.Mock {
		t.Fatal("used a missing database")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("a missing database was created")
	}
}
