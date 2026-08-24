// Package httpengine owns provider-independent state lifecycle for compiled
// HTTP resources. Provider tables and scenario semantics remain in each
// resource's ScenarioCodec.
package httpengine

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const (
	baselineFilename = "baseline.db"
	liveFilename     = "live.db"
)

type StateManager struct{}

type ServiceState struct {
	mu       sync.Mutex
	dir      string
	baseline string
	live     string
	db       *sql.DB
}

// Prepare validates and materializes a scenario before publishing either
// database name. A failed prepare removes only the new service directory.
func (StateManager) Prepare(ctx context.Context, serviceDir string, codec httpresource.ScenarioCodec, doc scenario.Document) (_ *ServiceState, err error) {
	if serviceDir == "" || codec == nil {
		return nil, fmt.Errorf("http state: service directory and scenario codec are required")
	}
	if _, err := os.Stat(serviceDir); err == nil {
		return nil, fmt.Errorf("http state: service directory already exists: %s", serviceDir)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("http state: inspect service directory: %w", err)
	}
	if err := codec.Validate(ctx, doc); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(serviceDir, 0o700); err != nil {
		return nil, fmt.Errorf("http state: create service directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(serviceDir)
		}
	}()

	baseline := filepath.Join(serviceDir, baselineFilename)
	live := filepath.Join(serviceDir, liveFilename)
	temporaryBaseline := baseline + ".tmp"
	db, err := openSQLite(temporaryBaseline)
	if err != nil {
		return nil, err
	}
	if err := codec.Initialize(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := codec.Load(ctx, db, doc); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Close(); err != nil {
		return nil, fmt.Errorf("http state: close baseline: %w", err)
	}
	if err := os.Rename(temporaryBaseline, baseline); err != nil {
		return nil, fmt.Errorf("http state: publish baseline: %w", err)
	}
	if err := copyFileAtomic(baseline, live); err != nil {
		return nil, err
	}
	liveDB, err := openSQLite(live)
	if err != nil {
		return nil, err
	}
	state := &ServiceState{dir: serviceDir, baseline: baseline, live: live, db: liveDB}
	committed = true
	return state, nil
}

// DB is valid until Reset or Close begins. The engine must quiesce requests
// and reconstruct the resource server around the DB returned by Reset.
func (s *ServiceState) DB() *sql.DB {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db
}

func (s *ServiceState) Reset(_ context.Context) (*sql.DB, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, fmt.Errorf("http state: service is closed")
	}
	if err := s.db.Close(); err != nil {
		return nil, fmt.Errorf("http state: close live database for reset: %w", err)
	}
	s.db = nil
	if err := copyFileAtomic(s.baseline, s.live); err != nil {
		return nil, err
	}
	db, err := openSQLite(s.live)
	if err != nil {
		return nil, err
	}
	s.db = db
	return db, nil
}

func (s *ServiceState) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func openSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("http state: open SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("http state: ping SQLite: %w", err)
	}
	return db, nil
}

func copyFileAtomic(source, destination string) error {
	src, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("http state: open copy source: %w", err)
	}
	defer src.Close()
	temporary := destination + ".tmp"
	dst, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("http state: create copy destination: %w", err)
	}
	cleanup := true
	defer func() {
		_ = dst.Close()
		if cleanup {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("http state: copy database: %w", err)
	}
	if err := dst.Sync(); err != nil {
		return fmt.Errorf("http state: sync database copy: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("http state: close database copy: %w", err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("http state: publish database copy: %w", err)
	}
	cleanup = false
	return nil
}
