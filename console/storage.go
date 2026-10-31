package console

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/dumbmachine/fabricate/environment"
	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

// The registry publishes a sandbox only after its database files are durable.
// Transitional rows make interrupted creation/deletion recoverable. Tokens are
// secret-bearing metadata: protect this entire volume, not only the databases.
type storage struct {
	root   string
	db     *sql.DB
	unlock func() error
}
type storedSession struct {
	Project       string                       `json:"project,omitempty"`
	Expires       *time.Time                   `json:"expiresAt,omitempty"`
	Request       *StartRequest                `json:"request,omitempty"`
	RequestKey    string                       `json:"requestKey,omitempty"`
	RequestDigest string                       `json:"requestDigest,omitempty"`
	ID            string                       `json:"id"`
	Kind          string                       `json:"kind"`
	Spec          environment.Spec             `json:"spec"`
	Started       time.Time                    `json:"started"`
	Tokens        map[string]string            `json:"tokens,omitempty"`
	Metadata      map[string]scenario.Metadata `json:"metadata,omitempty"`
	Contracts     map[string]string            `json:"contracts,omitempty"`
	LogRunID      string                       `json:"logRunId,omitempty"`
}

var sandboxID = regexp.MustCompile(`^[a-f0-9]{8}$`)

func openStorage(root, origin string) (_ *storage, err error) {
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if err = os.Chmod(root, 0o700); err != nil {
		return nil, err
	}
	unlock, err := lockStorage(filepath.Join(root, "owner.lock"))
	if err != nil {
		return nil, err
	}
	s := &storage{root: root, unlock: unlock}
	defer func() {
		if err != nil {
			_ = s.close()
		}
	}()
	path := filepath.Join(root, "control.db")
	info, statErr := os.Lstat(path)
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if statErr == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("storage: invalid registry file")
	}
	if os.IsNotExist(statErr) {
		entries, e := os.ReadDir(filepath.Join(root, "sandboxes"))
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		if len(entries) > 0 {
			return nil, fmt.Errorf("storage: registry missing alongside sandbox data; refusing initialization")
		}
		file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if e != nil {
			return nil, e
		}
		if e = file.Close(); e != nil {
			return nil, e
		}
	}
	if err = os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	s.db, err = sql.Open("sqlite", u.String()+"?mode=rw&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	s.db.SetMaxOpenConns(1)
	var version int
	if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return nil, fmt.Errorf("storage: read registry: %w", err)
	}
	if version < 0 || version > 2 {
		return nil, fmt.Errorf("storage: unsupported registry version %d", version)
	}
	if version == 0 {
		entries, e := os.ReadDir(filepath.Join(root, "sandboxes"))
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		if len(entries) > 0 {
			return nil, fmt.Errorf("storage: uninitialized registry alongside sandbox data")
		}
		_, err = s.db.Exec(`BEGIN; CREATE TABLE config (key TEXT PRIMARY KEY, value TEXT NOT NULL); CREATE TABLE sessions (id TEXT PRIMARY KEY, status TEXT NOT NULL CHECK(status IN ('creating','ready','deleting')), record BLOB NOT NULL); PRAGMA user_version=1; COMMIT;`)
		if err != nil {
			return nil, err
		}
	}
	if _, err = s.db.Exec("INSERT INTO config(key,value) VALUES('public_url',?) ON CONFLICT DO NOTHING", origin); err != nil {
		return nil, err
	}
	var saved string
	if err = s.db.QueryRow("SELECT value FROM config WHERE key='public_url'").Scan(&saved); err != nil {
		return nil, err
	}
	if saved != origin {
		return nil, fmt.Errorf("storage: public URL differs from stored deployment; restore the original public URL")
	}
	if version < 2 {
		_, err = s.db.Exec(`BEGIN; CREATE TABLE projects (id TEXT PRIMARY KEY,record BLOB NOT NULL); CREATE TABLE project_tokens (id TEXT PRIMARY KEY,project TEXT NOT NULL,name TEXT NOT NULL,digest TEXT NOT NULL UNIQUE,expires INTEGER NOT NULL,revoked INTEGER NOT NULL); CREATE TABLE requests (project TEXT NOT NULL,key TEXT NOT NULL,digest TEXT NOT NULL,id TEXT NOT NULL,status TEXT NOT NULL,PRIMARY KEY(project,key)); PRAGMA user_version=2; COMMIT;`)
		if err != nil {
			return nil, err
		}
	}
	if err = os.MkdirAll(filepath.Join(root, "sandboxes"), 0o700); err != nil {
		return nil, err
	}
	if err = syncDirectory(filepath.Join(root, "sandboxes")); err != nil {
		return nil, err
	}
	if err = syncDirectory(root); err != nil {
		return nil, err
	}
	if err = syncDirectory(filepath.Dir(root)); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *storage) close() error {
	var err error
	if s.db != nil {
		err = s.db.Close()
	}
	if s.unlock != nil {
		e := s.unlock()
		s.unlock = nil
		if err == nil {
			err = e
		}
	}
	return err
}
func (s *storage) directory(id string) string { return filepath.Join(s.root, "sandboxes", id) }
func (s *storage) create(record storedSession) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO sessions(id,status,record) VALUES(?,'creating',?)", record.ID, raw); err != nil {
		return err
	}
	if record.RequestKey != "" {
		if _, err = tx.Exec("INSERT INTO requests(project,key,digest,id,status) VALUES(?,?,?,?,'creating')", record.Project, record.RequestKey, record.RequestDigest, record.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *storage) ready(record storedSession) error {
	// SQLite syncs file contents; directory syncs persist their names before the
	// registry can acknowledge creation to clients.
	if err := syncTreeDirectories(s.directory(record.ID)); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Join(s.root, "sandboxes")); err != nil {
		return err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE sessions SET status='ready',record=? WHERE id=?", raw, record.ID); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE requests SET status='ready' WHERE id=?", record.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *storage) markDeleting(id string) error {
	_, err := s.db.Exec("UPDATE sessions SET status='deleting' WHERE id=?", id)
	return err
}
func (s *storage) discard(id string) error {
	if !sandboxID.MatchString(id) {
		return fmt.Errorf("storage: invalid sandbox identity")
	}
	if err := os.RemoveAll(s.directory(id)); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Join(s.root, "sandboxes")); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE requests SET status='closed' WHERE id=?", id); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM sessions WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
func resourceContract(resource httpresource.Resource) string {
	d := resource.Descriptor()
	return fmt.Sprintf("%s/%d/%s/%s", d.Version, d.ScenarioVersion, d.OpenAPIDigest, scenario.Digest(resource.Contract().ScenarioJSON))
}
func (m *Manager) recoverStorage(ctx context.Context) error {
	rows, err := m.storage.db.Query("SELECT id,status,record FROM sessions ORDER BY id")
	if err != nil {
		return err
	}
	type entry struct {
		id, status string
		record     storedSession
	}
	var entries []entry
	for rows.Next() {
		var e entry
		var raw []byte
		if err = rows.Scan(&e.id, &e.status, &raw); err != nil {
			break
		}
		if !sandboxID.MatchString(e.id) {
			err = fmt.Errorf("storage: invalid stored sandbox identity")
			break
		}
		if err = json.Unmarshal(raw, &e.record); err != nil {
			err = fmt.Errorf("storage: invalid sandbox metadata")
			break
		}
		if e.record.ID != e.id {
			err = fmt.Errorf("storage: sandbox identity mismatch")
			break
		}
		entries = append(entries, e)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	active := 0
	for _, e := range entries {
		if (e.status == "ready" || e.status == "creating") && !expired(e.record.Expires, time.Now()) {
			active++
		}
	}
	if active > m.capacity {
		return fmt.Errorf("storage: active sandboxes exceed configured capacity")
	}
	for _, e := range entries {
		if e.status == "creating" && e.record.Request != nil && !expired(e.record.Expires, time.Now()) {
			runtime, record, createErr := m.provision(ctx, e.record)
			if createErr != nil {
				return fmt.Errorf("storage: resume sandbox %s: %w", e.id, createErr)
			}
			m.sessions[e.id] = &runningSession{id: e.id, kind: record.Kind, spec: record.Spec, runtime: runtime, started: record.Started, project: record.Project, expires: record.Expires}
			continue
		}
		if e.status == "creating" || e.status == "deleting" || expired(e.record.Expires, time.Now()) {
			if err = m.storage.discard(e.id); err != nil {
				return err
			}
			continue
		}
		if e.status != "ready" {
			return fmt.Errorf("storage: unknown sandbox status")
		}
		r := e.record
		if r.Project == "" {
			r.Project = "default"
		}
		if err = r.Spec.Validate(); err != nil {
			return fmt.Errorf("storage: invalid spec for %s", e.id)
		}
		if len(r.Tokens) != len(r.Spec.Services) || len(r.Metadata) != len(r.Spec.Services) || len(r.Contracts) != len(r.Spec.Services) || r.LogRunID == "" {
			return fmt.Errorf("storage: incomplete metadata for %s", e.id)
		}
		for name, service := range r.Spec.Services {
			resource, ok := m.registry.Get(service.Resource)
			if !ok || r.Contracts[name] != resourceContract(resource) {
				return fmt.Errorf("storage: resource contract changed for %s/%s; migration required", e.id, name)
			}
			meta := r.Metadata[name]
			if meta.ID != service.Scenario || meta.Resource != service.Resource || meta.ResourceVersion != resource.Descriptor().Version {
				return fmt.Errorf("storage: baseline identity mismatch for %s/%s", e.id, name)
			}
		}
		runtime, e2 := environment.StartWithStorage(ctx, r.Spec, m.registry, nil, environment.StorageOptions{Directory: m.storage.directory(e.id), Reopen: true, Tokens: r.Tokens, Metadata: r.Metadata, LogRunID: r.LogRunID})
		if e2 != nil {
			return fmt.Errorf("storage: recover sandbox %s: %w", e.id, e2)
		}
		m.sessions[e.id] = &runningSession{id: e.id, kind: r.Kind, spec: r.Spec, runtime: runtime, started: r.Started, project: r.Project, expires: r.Expires}
	}
	return nil
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func syncTreeDirectories(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, dir := range dirs {
		if err = syncDirectory(dir); err != nil {
			return err
		}
	}
	return nil
}

func (s *storage) lookupRequest(project, key, digest string) (storedSession, string, error) {
	var id, saved, status string
	err := s.db.QueryRow("SELECT id,digest,status FROM requests WHERE project=? AND key=?", project, key).Scan(&id, &saved, &status)
	if err == sql.ErrNoRows {
		return storedSession{}, "", nil
	}
	if err != nil {
		return storedSession{}, "", err
	}
	if digest != "" && saved != digest {
		return storedSession{}, "", problem(409, "idempotency key already used for different arguments")
	}
	record := storedSession{ID: id, Project: project}
	var raw []byte
	err = s.db.QueryRow("SELECT record FROM sessions WHERE id=?", id).Scan(&raw)
	if err == sql.ErrNoRows {
		return record, "closed", nil
	}
	if err != nil {
		return record, "", err
	}
	if err = json.Unmarshal(raw, &record); err != nil {
		return record, "", err
	}
	return record, status, nil
}
func (s *storage) renew(id string, expiry *time.Time) error {
	var raw []byte
	if err := s.db.QueryRow("SELECT record FROM sessions WHERE id=?", id).Scan(&raw); err != nil {
		return err
	}
	var record storedSession
	if err := json.Unmarshal(raw, &record); err != nil {
		return err
	}
	record.Expires = expiry
	raw, _ = json.Marshal(record)
	_, err := s.db.Exec("UPDATE sessions SET record=? WHERE id=?", raw, id)
	return err
}
