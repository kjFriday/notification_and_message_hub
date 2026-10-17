// Package store opens the shared ACME database by path: the dispatcher's only link to the other
// programs. It reads events and customers and writes back each processed event's status. The CRUD hub
// owns the schema; this package never creates or changes tables, and it only ever moves an event out
// of "pending".
package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"acme/hub/dispatch"
	"acme/hub/textsafe"

	_ "modernc.org/sqlite" // pure-Go SQLite: no cgo
)

// Needs is every table and column the dispatcher reads or writes.
var Needs = map[string][]string{
	"events":    {"id", "kind", "payload", "status", "created_at"},
	"customers": {"id", "name", "email"},
}

// Open opens path for reading and writing. A missing file is an error; it is never created.
func Open(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	abs, _ := filepath.Abs(path)
	dsn := "file:" + (&url.URL{Path: filepath.ToSlash(abs)}).EscapedPath() +
		"?mode=rw&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// CheckSchema fails unless every table and column in Needs exists.
func CheckSchema(conn *sql.DB) error {
	tables := make([]string, 0, len(Needs))
	for t := range Needs {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, table := range tables {
		rows, err := conn.Query("SELECT name FROM pragma_table_info(?)", table)
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for rows.Next() {
			var n string
			rows.Scan(&n)
			have[n] = true
		}
		rows.Close()
		if len(have) == 0 {
			return fmt.Errorf("no %s table", table)
		}
		var missing []string
		for _, c := range Needs[table] {
			if !have[c] {
				missing = append(missing, c)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("%s lacks %s", table, strings.Join(missing, ", "))
		}
	}
	return nil
}

// Events reads every event, oldest id first.
func Events(conn *sql.DB) ([]dispatch.Event, error) {
	rows, err := conn.Query("SELECT id, kind, COALESCE(payload, ''), COALESCE(status, ''), COALESCE(created_at, '') FROM events ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dispatch.Event
	for rows.Next() {
		var e dispatch.Event
		if err := rows.Scan(&e.ID, &e.Kind, &e.Payload, &e.Status, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Customers reads every customer.
func Customers(conn *sql.DB) ([]dispatch.Person, error) {
	rows, err := conn.Query("SELECT id, name, COALESCE(email, '') FROM customers ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dispatch.Person
	for rows.Next() {
		var p dispatch.Person
		if err := rows.Scan(&p.ID, &p.Name, &p.Email); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// WriteStatuses stores every status other than "pending", in one transaction. Only rows still pending
// change, so a second dispatcher (or the hub's own edits) is never overwritten. -> rows changed.
func WriteStatuses(conn *sql.DB, status map[int64]string) (int, error) {
	ids := make([]int64, 0, len(status))
	for id, s := range status {
		if s != "pending" {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	tx, err := conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, id := range ids {
		res, err := tx.Exec("UPDATE events SET status = ? WHERE id = ? AND status = 'pending'", textsafe.Clean(status[id]), id)
		if err != nil {
			return 0, err
		}
		changed, _ := res.RowsAffected()
		n += int(changed)
	}
	return n, tx.Commit()
}
