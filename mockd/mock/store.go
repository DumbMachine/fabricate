// Package mock is the stateful HTTP-API mock framework fab's httpmock engine
// runs. A Service registers handlers Express-style (svc.GET/POST/…) over a
// per-container SQLite database, so mocks behave like the real API: writes
// (creates, updates, custom actions like reviews:reply) mutate state, and
// subsequent reads reflect it. One binary hosts many services (a registry);
// MOCK_SERVICE picks which one a container serves.
package mock

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// Table is one SQLite table a service declares. DDL is the column/constraint
// body of a CREATE TABLE statement.
type Table struct {
	Name string
	DDL  string
}

// OpenDB opens a fresh SQLite database at path (a file, or ":memory:" in tests)
// and creates the service's tables. MaxOpenConns(1) sidesteps SQLite's
// single-writer locking — a mock's request volume never needs more, and it
// keeps ":memory:" coherent across the database/sql pool.
func OpenDB(path string, tables []Table) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	for _, t := range tables {
		if _, err := db.Exec("CREATE TABLE IF NOT EXISTS " + t.Name + " (" + t.DDL + ")"); err != nil {
			return nil, fmt.Errorf("create table %s: %w", t.Name, err)
		}
	}
	return db, nil
}
