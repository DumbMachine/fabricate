package console

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/dumbmachine/fabricate/scenario"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dumbmachine/fabricate/environment"
	"github.com/dumbmachine/fabricate/httpresource"
)

const maxSessions = 16

type StartRequest struct {
	Kind     string                    `json:"kind"`
	ID       string                    `json:"id"`
	Services map[string]ServiceRequest `json:"services,omitempty"`
}

// ServiceRequest composes existing provider behavior with a catalog baseline or
// explicit state. No local filename, executable code or upstream credential.
type ServiceRequest struct {
	Resource string          `json:"resource"`
	Scenario string          `json:"scenario"`
	State    json.RawMessage `json:"state,omitempty"`
}

type Session struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Label           string           `json:"label"`
	Kind            string           `json:"kind"`
	Status          string           `json:"status"`
	Proxy           bool             `json:"proxy"`
	StartedAt       time.Time        `json:"startedAt"`
	RequestLog      string           `json:"requestLog"`
	Logs            string           `json:"logs,omitempty"`
	Services        []SessionService `json:"services"`
	Variables       []Variable       `json:"variables"`
	ProxyConnection *ProxyConnection `json:"proxyConnection,omitempty"`
	ConnectShell    string           `json:"connectShell"`
	AgentPrompt     string           `json:"agentPrompt"`
	Anchored        bool             `json:"anchored"`
}

type SessionService struct {
	Name          string   `json:"name"`
	Resource      string   `json:"resource"`
	DisplayName   string   `json:"displayName"`
	Scenario      string   `json:"scenario"`
	URL           string   `json:"url"`
	Token         string   `json:"token"`
	URLVariable   string   `json:"urlVariable"`
	TokenVariable string   `json:"tokenVariable"`
	ProviderHosts []string `json:"providerHosts"`
}

type Variable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ProxyConnection struct {
	URL    string   `json:"url"`
	CAPath string   `json:"caPath"`
	Hosts  []string `json:"hosts"`
}

type runningSession struct {
	id       string
	kind     string
	spec     environment.Spec
	runtime  *environment.Runtime
	started  time.Time
	proxy    bool
	anchored bool
}

type Manager struct {
	registry *httpresource.Registry

	mu           sync.Mutex
	closing      bool
	inflight     int
	sessions     map[string]*runningSession
	storage      *storage
	starts       sync.WaitGroup
	shutdownOnce sync.Once
	shutdownErr  error

	catalogOnce sync.Once
	catalog     Catalog
	catalogErr  error

	proxy     bool
	publicURL string
}

func New(registry *httpresource.Registry) *Manager {
	return &Manager{
		registry: registry,
		sessions: map[string]*runningSession{},
	}
}

func (m *Manager) Catalog() (Catalog, error) {
	m.catalogOnce.Do(func() {
		m.catalog, m.catalogErr = buildCatalog(m.registry)
	})
	return m.catalog, m.catalogErr
}

func (m *Manager) Start(ctx context.Context, request StartRequest) (_ Session, err error) {
	spec, kind, err := m.resolve(request)
	if err != nil {
		return Session{}, invalidStartError{err}
	}
	documents, err := m.startDocuments(ctx, request, spec)
	if err != nil {
		return Session{}, invalidStartError{err}
	}
	proxy := m.proxyEnabled()
	if err = m.reserve(); err != nil {
		return Session{}, err
	}
	defer m.releaseReservation()
	id, err := newID()
	if err != nil {
		return Session{}, err
	}
	record := storedSession{ID: id, Kind: kind, Spec: spec, Started: time.Now().UTC()}
	var runtime *environment.Runtime
	published := false
	if m.storage != nil {
		if err = m.storage.create(record); err != nil {
			return Session{}, fmt.Errorf("storage: reserve sandbox: %w", err)
		}
	}
	defer func() {
		if !published {
			if runtime != nil {
				_ = runtime.Close(context.Background())
			}
			if m.storage != nil {
				if e := m.storage.discard(id); e != nil {
					err = errors.Join(err, fmt.Errorf("storage: cleanup pending: %w", e))
				}
			}
		}
	}()
	if m.storage != nil {
		runtime, err = environment.StartWithStorage(ctx, spec, m.registry, documents, environment.StorageOptions{Directory: m.storage.directory(id)})
	} else {
		runtime, err = environment.StartWithDocuments(ctx, spec, m.registry, proxy, documents)
	}
	if err != nil {
		return Session{}, err
	}
	running := &runningSession{id: id, kind: kind, spec: spec, runtime: runtime, started: record.Started, proxy: proxy}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return Session{}, fmt.Errorf("console is shutting down")
	}
	if m.sessions[id] != nil {
		return Session{}, fmt.Errorf("sandbox id collision")
	}
	if m.storage != nil {
		record.Tokens = map[string]string{}
		record.Contracts = map[string]string{}
		record.Metadata = runtime.Metadata
		record.LogRunID = runtime.Requests.RunID
		for name, service := range runtime.Services {
			record.Tokens[name] = service.Token
			record.Contracts[name] = resourceContract(service.Resource)
		}
		if err = m.storage.ready(record); err != nil {
			return Session{}, fmt.Errorf("storage: publish sandbox: %w", err)
		}
	}
	m.sessions[id] = running
	published = true
	return running.view(m.registry), nil
}

// Adopt registers a runtime this process already started. The console can
// start more sandboxes beside it, and Close refuses to stop it. Shutdown
// still closes it when the owning command exits.
func (m *Manager) Adopt(spec environment.Spec, runtime *environment.Runtime, proxy bool, kind string) (Session, error) {
	if runtime == nil {
		return Session{}, fmt.Errorf("runtime is required")
	}
	if kind != "scenario" {
		kind = "environment"
	}
	id, err := newID()
	if err != nil {
		return Session{}, err
	}
	running := &runningSession{
		id: id, kind: kind, spec: spec, runtime: runtime,
		started: time.Now().UTC(), proxy: proxy, anchored: true,
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return Session{}, fmt.Errorf("console is shutting down")
	}
	if len(m.sessions)+m.inflight >= maxSessions {
		return Session{}, fmt.Errorf("too many running sandboxes; close one first")
	}
	if m.sessions[id] != nil {
		return Session{}, fmt.Errorf("sandbox id collision")
	}
	m.sessions[id] = running
	return running.view(m.registry), nil
}

func (m *Manager) reserve() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return fmt.Errorf("console is shutting down")
	}
	if len(m.sessions)+m.inflight >= maxSessions {
		return fmt.Errorf("too many running sandboxes; close one first")
	}
	m.inflight++
	m.starts.Add(1)
	return nil
}

func (m *Manager) releaseReservation() {
	m.mu.Lock()
	m.inflight--
	m.mu.Unlock()
	m.starts.Done()
}

func (m *Manager) resolve(request StartRequest) (environment.Spec, string, error) {
	if request.Kind != "composition" && len(request.Services) != 0 {
		return environment.Spec{}, "", fmt.Errorf("services require composition kind")
	}
	switch request.Kind {
	case "composition":
		spec := environment.Spec{APIVersion: environment.APIVersion, Kind: "Environment", Metadata: environment.Metadata{Name: request.ID}, Services: map[string]environment.ServiceSpec{}}
		for name, service := range request.Services {
			spec.Services[name] = environment.ServiceSpec{Resource: service.Resource, Scenario: service.Scenario}
		}
		if err := spec.Validate(); err != nil {
			return environment.Spec{}, "", err
		}
		return spec, "environment", nil
	case "environment":
		if strings.TrimSpace(request.ID) == "" {
			return environment.Spec{}, "", fmt.Errorf("environment id is required")
		}
		spec, err := officialEnvironment(request.ID)
		if err != nil {
			return environment.Spec{}, "", err
		}
		return spec, "environment", nil
	case "scenario":
		if strings.TrimSpace(request.ID) == "" {
			return environment.Spec{}, "", fmt.Errorf("scenario id is required")
		}
		spec, err := scenarioEnvironment(request.ID, m.registry)
		if err != nil {
			return environment.Spec{}, "", err
		}
		return spec, "scenario", nil
	default:
		return environment.Spec{}, "", fmt.Errorf("kind must be \"environment\", \"scenario\" or \"composition\"")
	}
}

func (m *Manager) List() []Session {
	m.mu.Lock()
	running := make([]*runningSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		running = append(running, session)
	}
	m.mu.Unlock()
	sort.Slice(running, func(i, j int) bool {
		if running[i].started.Equal(running[j].started) {
			return running[i].id < running[j].id
		}
		return running[i].started.After(running[j].started)
	})
	sessions := make([]Session, 0, len(running))
	for _, session := range running {
		sessions = append(sessions, session.view(m.registry))
	}
	return sessions
}

func (m *Manager) serviceURL(id, name string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return "", false
	}
	service := session.runtime.Services[name]
	if service == nil || service.URL == "" {
		return "", false
	}
	return service.URL, true
}

func (m *Manager) Get(id string) (Session, bool) {
	m.mu.Lock()
	session, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return Session{}, false
	}
	return session.view(m.registry), true
}

var errAnchored = errors.New("this sandbox is held by fab run and stays up until that command exits")

func (m *Manager) Close(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil
	}
	if session.anchored {
		return errAnchored
	}
	if m.storage != nil {
		if err := m.storage.markDeleting(id); err != nil {
			return fmt.Errorf("storage: mark deletion: %w", err)
		}
	}
	delete(m.sessions, id)
	ctx, cancel := environment.CloseTimeout()
	defer cancel()
	if err := session.runtime.Close(ctx); err != nil {
		return err
	}
	if m.storage != nil {
		return m.storage.discard(id)
	}
	return nil
}

func (m *Manager) Shutdown() error {
	m.shutdownOnce.Do(func() {
		m.mu.Lock()
		m.closing = true
		m.mu.Unlock()
		// Keep the directory lock until every in-flight creation has either been
		// published or cleaned up. A second owner must never race these writes.
		m.starts.Wait()
		m.mu.Lock()
		sessions := m.sessions
		m.sessions = map[string]*runningSession{}
		m.mu.Unlock()
		for _, session := range sessions {
			ctx, cancel := environment.CloseTimeout()
			err := session.runtime.Close(ctx)
			cancel()
			m.shutdownErr = errors.Join(m.shutdownErr, err)
		}
		if m.storage != nil {
			m.shutdownErr = errors.Join(m.shutdownErr, m.storage.close())
		}
	})
	return m.shutdownErr
}

func (s *runningSession) view(registry *httpresource.Registry) Session {
	values := s.runtime.Environment()
	variables := make([]Variable, 0, len(values))
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		variables = append(variables, Variable{Name: name, Value: values[name]})
	}
	services := make([]SessionService, 0, len(s.runtime.Services))
	for _, name := range serviceNames(s.spec) {
		service := s.runtime.Services[name]
		spec := s.spec.Services[name]
		prefix := variablePrefix(name)
		view := SessionService{
			Name:          name,
			Resource:      spec.Resource,
			DisplayName:   resourceDisplayName(registry, spec.Resource),
			Scenario:      spec.Scenario,
			URLVariable:   prefix + "_URL",
			TokenVariable: prefix + "_TOKEN",
			ProviderHosts: providerHosts(registry, spec.Resource),
		}
		if service != nil {
			view.URL = service.URL
			view.Token = service.Token
		}
		services = append(services, view)
	}
	session := Session{
		ID:         s.id,
		Name:       s.spec.Metadata.Name,
		Label:      sessionLabel(s.kind, s.spec, registry),
		Kind:       s.kind,
		Status:     "ready",
		Proxy:      s.proxy,
		Anchored:   s.anchored,
		StartedAt:  s.started,
		RequestLog: s.runtime.Requests.Path(),
		Services:   services,
		Variables:  variables,
	}
	if s.runtime.Proxy != nil {
		session.ProxyConnection = &ProxyConnection{
			URL:    s.runtime.Proxy.URL,
			CAPath: s.runtime.Proxy.CAPath,
			Hosts:  s.runtime.Proxy.InterceptedHosts(),
		}
	}
	session.ConnectShell = connectShell(session)
	session.AgentPrompt = agentPrompt(session)
	return session
}

func sessionLabel(kind string, spec environment.Spec, registry *httpresource.Registry) string {
	if kind == "scenario" {
		for _, name := range serviceNames(spec) {
			return resourceDisplayName(registry, spec.Services[name].Resource)
		}
	}
	return humanize(spec.Metadata.Name)
}

func variablePrefix(serviceName string) string {
	return "FAB_" + strings.ToUpper(strings.ReplaceAll(serviceName, "-", "_"))
}

func newID() (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("sandbox id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

func (m *Manager) startDocuments(ctx context.Context, request StartRequest, spec environment.Spec) (map[string]scenario.Document, error) {
	documents := map[string]scenario.Document{}
	for name, service := range spec.Services {
		resource, ok := m.registry.Get(service.Resource)
		if !ok {
			return nil, fmt.Errorf("unknown resource %q", service.Resource)
		}
		doc, err := resource.Scenario(service.Scenario)
		if err != nil {
			return nil, err
		}
		if state := request.Services[name].State; len(state) != 0 {
			doc.State = state
		}
		if err := resource.Scenarios().Validate(ctx, doc); err != nil {
			return nil, fmt.Errorf("service %q: %w", name, err)
		}
		documents[name] = doc
	}
	return documents, nil
}

// invalidStartError separates caller validation from provisioning failures.
type invalidStartError struct{ error }
