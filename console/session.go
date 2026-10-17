package console

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dumbmachine/fabricate/environment"
	"github.com/dumbmachine/fabricate/httpresource"
)

const maxSessions = 16

type StartRequest struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
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

	mu       sync.Mutex
	closing  bool
	inflight int
	sessions map[string]*runningSession

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

func (m *Manager) Start(ctx context.Context, request StartRequest) (Session, error) {
	spec, kind, err := m.resolve(request)
	if err != nil {
		return Session{}, err
	}
	proxy := m.proxyEnabled()
	if err := m.reserve(); err != nil {
		return Session{}, err
	}
	runtime, err := environment.Start(ctx, spec, m.registry, proxy)
	if err != nil {
		m.releaseReservation()
		return Session{}, err
	}
	id, err := newID()
	if err != nil {
		_ = runtime.Close(context.Background())
		m.releaseReservation()
		return Session{}, err
	}
	running := &runningSession{
		id: id, kind: kind, spec: spec, runtime: runtime,
		started: time.Now().UTC(), proxy: proxy,
	}
	m.mu.Lock()
	m.inflight--
	reason := ""
	switch {
	case m.closing:
		reason = "shutdown"
	case len(m.sessions) >= maxSessions:
		reason = "full"
	case m.sessions[id] != nil:
		reason = "collision"
	default:
		m.sessions[id] = running
	}
	m.mu.Unlock()
	if reason != "" {
		_ = runtime.Close(context.Background())
		switch reason {
		case "shutdown":
			return Session{}, fmt.Errorf("console is shutting down")
		case "collision":
			return Session{}, fmt.Errorf("sandbox id collision")
		default:
			return Session{}, fmt.Errorf("too many running sandboxes; close one first")
		}
	}
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
	return nil
}

func (m *Manager) releaseReservation() {
	m.mu.Lock()
	m.inflight--
	m.mu.Unlock()
}

func (m *Manager) resolve(request StartRequest) (environment.Spec, string, error) {
	switch request.Kind {
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
		return environment.Spec{}, "", fmt.Errorf("kind must be \"environment\" or \"scenario\"")
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
	session, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	if session.anchored {
		m.mu.Unlock()
		return errAnchored
	}
	delete(m.sessions, id)
	m.mu.Unlock()
	ctx, cancel := environment.CloseTimeout()
	defer cancel()
	return session.runtime.Close(ctx)
}

func (m *Manager) Shutdown() error {
	m.mu.Lock()
	m.closing = true
	sessions := m.sessions
	m.sessions = map[string]*runningSession{}
	m.mu.Unlock()
	var first error
	for _, session := range sessions {
		ctx, cancel := environment.CloseTimeout()
		err := session.runtime.Close(ctx)
		cancel()
		if first == nil && err != nil {
			first = err
		}
	}
	return first
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
