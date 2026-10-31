package console

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/dumbmachine/fabricate/environment"
)

var projectPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var requestKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func expired(expiry *time.Time, now time.Time) bool { return expiry != nil && !expiry.After(now) }
func expiryFor(ttl *int64, p Project) (*time.Time, error) {
	seconds := p.DefaultTTLSeconds
	if ttl != nil {
		seconds = *ttl
	}
	if seconds < 0 || (seconds == 0 && p.ID != "default") || seconds > p.MaxTTLSeconds {
		return nil, problem(400, "TTL is outside project policy")
	}
	if seconds == 0 {
		return nil, nil
	}
	expiry := time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	return &expiry, nil
}
func (m *Manager) checkCapacity(project Project, resumeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	total, count := 0, 0
	if m.storage != nil {
		if err := m.storage.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE status IN ('creating','ready') AND id<>?", resumeID).Scan(&total); err != nil {
			return err
		}
		rows, err := m.storage.db.Query("SELECT record FROM sessions WHERE status IN ('creating','ready') AND id<>?", resumeID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var record storedSession
			if err = rows.Scan(&raw); err != nil {
				return err
			}
			if err = json.Unmarshal(raw, &record); err != nil {
				return err
			}
			owner := record.Project
			if owner == "" {
				owner = "default"
			}
			if owner == project.ID {
				count++
			}
		}
		if err = rows.Err(); err != nil {
			return err
		}
	} else {
		for _, session := range m.sessions {
			if session.id == resumeID {
				continue
			}
			total++
			owner := session.project
			if owner == "" {
				owner = "default"
			}
			if owner == project.ID {
				count++
			}
		}
	}
	if total >= m.capacity || count >= project.MaxSandboxes {
		return problem(429, "sandbox capacity reached; close an unused sandbox or ask the operator to adjust capacity")
	}
	return nil
}
func (m *Manager) start(ctx context.Context, request StartRequest) (_ Session, err error) {
	m.startMu.Lock()
	defer m.startMu.Unlock()
	if err = m.reserve(); err != nil {
		return Session{}, err
	}
	defer m.releaseReservation()
	project, err := m.project(ctx, request.Project)
	if err != nil {
		return Session{}, err
	}
	request.Project = project.ID
	if request.IdempotencyKey != "" && !requestKeyPattern.MatchString(request.IdempotencyKey) {
		return Session{}, problem(400, "invalid idempotency key")
	}
	if m.storage == nil && request.IdempotencyKey != "" {
		return Session{}, problem(400, "durable idempotency requires mounted storage")
	}
	hashInput := request
	hashInput.IdempotencyKey = ""
	raw, _ := json.Marshal(hashInput)
	digest := tokenDigest(string(raw))
	var record storedSession
	resumed := false
	if request.IdempotencyKey != "" {
		saved, status, e := m.storage.lookupRequest(project.ID, request.IdempotencyKey, digest)
		if e != nil {
			return Session{}, e
		}
		if status != "" {
			if status == "ready" {
				if result, ok := m.GetFor(ctx, saved.ID); ok {
					return result, nil
				}
				return Session{}, problem(410, "idempotent sandbox was closed or expired; use a new key for a new sandbox")
			}
			if status != "creating" {
				return Session{}, problem(410, "idempotent sandbox was closed; use a new key for a new sandbox")
			}
			record = saved
			resumed = true
		}
	}
	spec, kind, err := m.resolve(request)
	if err != nil {
		return Session{}, invalidStartError{err}
	}
	if _, err = m.startDocuments(ctx, request, spec); err != nil {
		return Session{}, invalidStartError{err}
	}
	if err = m.checkCapacity(project, record.ID); err != nil {
		return Session{}, err
	}
	if !resumed {
		id, e := newID()
		if e != nil {
			return Session{}, e
		}
		expiry, e := expiryFor(request.TTLSeconds, project)
		if e != nil {
			return Session{}, e
		}
		record = storedSession{ID: id, Kind: kind, Spec: spec, Started: time.Now().UTC(), Project: project.ID, Expires: expiry, Request: &request, RequestKey: request.IdempotencyKey, RequestDigest: digest}
		if m.storage != nil {
			if err = m.storage.create(record); err != nil {
				return Session{}, err
			}
		}
	}
	runtime, ready, err := m.provision(ctx, record)
	if err != nil {
		if m.storage != nil && record.RequestKey == "" {
			if e := m.storage.discard(record.ID); e != nil {
				return Session{}, fmt.Errorf("%w; cleanup pending: %v", err, e)
			}
		}
		return Session{}, err
	}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		_ = runtime.Close(context.Background())
		if m.storage != nil && record.RequestKey == "" {
			_ = m.storage.discard(record.ID)
		}
		return Session{}, fmt.Errorf("console is shutting down")
	}
	running := &runningSession{id: ready.ID, kind: ready.Kind, spec: ready.Spec, runtime: runtime, started: ready.Started, proxy: m.proxy, project: ready.Project, expires: ready.Expires}
	m.sessions[ready.ID] = running
	m.mu.Unlock()
	return running.view(m.registry), nil
}

// provision is shared by initial creation, same-key retry, and restart recovery.
// Before publication no external connection is returned, so incomplete private
// initialization can safely be rebuilt from its persisted request.
func (m *Manager) provision(ctx context.Context, record storedSession) (runtime *environment.Runtime, ready storedSession, err error) {
	if record.Request == nil {
		return nil, ready, fmt.Errorf("missing creation request")
	}
	documents, err := m.startDocuments(ctx, *record.Request, record.Spec)
	if err != nil {
		return nil, ready, err
	}
	if m.storage != nil {
		if err = os.RemoveAll(m.storage.directory(record.ID)); err != nil {
			return nil, ready, err
		}
		runtime, err = environment.StartWithStorage(ctx, record.Spec, m.registry, documents, environment.StorageOptions{Directory: m.storage.directory(record.ID)})
	} else {
		runtime, err = environment.StartWithDocuments(ctx, record.Spec, m.registry, m.proxyEnabled(), documents)
	}
	if err != nil {
		return nil, ready, err
	}
	active := runtime
	defer func() {
		if err != nil {
			_ = active.Close(context.Background())
		}
	}()
	ready = record
	ready.Request = nil
	ready.Tokens = map[string]string{}
	ready.Contracts = map[string]string{}
	ready.Metadata = runtime.Metadata
	ready.LogRunID = runtime.Requests.RunID
	for name, service := range runtime.Services {
		ready.Tokens[name] = service.Token
		ready.Contracts[name] = resourceContract(service.Resource)
	}
	if m.storage != nil {
		if err = m.storage.ready(ready); err != nil {
			return nil, ready, err
		}
	}
	return runtime, ready, nil
}
func (m *Manager) handleRenew(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TTLSeconds int64 `json:"ttlSeconds"`
	}
	if !decodeControl(w, r, &input) {
		return
	}
	session, ok := m.GetFor(r.Context(), r.PathValue("id"))
	if !ok {
		writeError(w, 404, fmt.Errorf("sandbox not found"))
		return
	}
	project, err := m.project(r.Context(), session.Project)
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	if input.TTLSeconds < 1 {
		writeError(w, 400, fmt.Errorf("positive renewal TTL required"))
		return
	}
	expiry, err := expiryFor(&input.TTLSeconds, project)
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	m.mu.Lock()
	running, ok := m.sessions[session.ID]
	if !ok || expired(running.expires, time.Now()) {
		m.mu.Unlock()
		writeError(w, 404, fmt.Errorf("sandbox expired or closed"))
		return
	}
	if m.storage != nil {
		err = m.storage.renew(session.ID, expiry)
	}
	if err == nil {
		running.expires = expiry
		session = running.view(m.registry)
	}
	m.mu.Unlock()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, m.present(session, r))
}
func (m *Manager) handleRequestStatus(w http.ResponseWriter, r *http.Request) {
	if m.storage == nil {
		writeError(w, 400, fmt.Errorf("request recovery requires mounted storage"))
		return
	}
	project, err := m.project(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	record, status, err := m.storage.lookupRequest(project.ID, r.PathValue("key"), "")
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	if status == "" {
		writeError(w, 404, fmt.Errorf("request not found"))
		return
	}
	if expired(record.Expires, time.Now()) {
		status = "expired"
	}
	out := map[string]any{"id": record.ID, "project": project.ID, "status": status, "idempotencyKey": r.PathValue("key")}
	if status == "ready" {
		if s, ok := m.GetFor(r.Context(), record.ID); ok {
			out["sandbox"] = m.present(s, r)
		}
	}
	writeJSON(w, 200, out)
}
func (m *Manager) startSweeper() {
	m.sweepStop = make(chan struct{})
	m.sweepDone = make(chan struct{})
	go func() {
		defer close(m.sweepDone)
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-m.sweepStop:
				return
			case now := <-timer.C:
				_ = m.expireSandboxes(now)
			}
		}
	}()
}
func (m *Manager) expireSandboxes(now time.Time) error {
	// Close and renewal share the manager lock; an expired decision is checked
	// again under that lock, so a successful renewal cannot be deleted by a stale sweep.
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return nil
	}
	for id, s := range m.sessions {
		if !expired(s.expires, now) {
			continue
		}
		if err := m.closeLocked(id); err != nil {
			return err
		}
	}
	return nil
}
